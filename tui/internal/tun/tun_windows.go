//go:build windows

package tun

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	nicID   = 1
	readBuf = 2048
)

// Config holds TUN settings.
type Config struct {
	Gateway   string
	Subnet    string
	MTU       int
	DoH       []string
	ExcludeIP []string
	Dial      func(network, addr string) (net.Conn, error)
}

// Device is a Windows TUN device backed by wintun and a gVisor netstack.
type Device struct {
	dev    tun.Device
	stk    *stack.Stack
	ep     *channel.Endpoint
	ctx    context.Context
	cancel context.CancelFunc
	ifName string
	cfg    Config

	resolver *dohResolver
	mu       sync.Mutex
	closed   bool
}

// Create builds the TUN device, assigns the virtual subnet and installs
// split-default routes (0.0.0.0/1 + 128.0.0.0/1) plus physical-gateway
// routes for ExcludeIP. Requires administrator privileges.
func Create(cfg Config) (*Device, error) {
	if cfg.Dial == nil {
		return nil, fmt.Errorf("tun: Dial is required")
	}
	if cfg.Gateway == "" {
		cfg.Gateway = "198.18.0.1"
	}
	if cfg.Subnet == "" {
		cfg.Subnet = "198.18.0.0/15"
	}
	if cfg.MTU == 0 {
		cfg.MTU = 1500
	}
	dev, err := tun.CreateTUN("naivereal", cfg.MTU)
	if err != nil {
		return nil, fmt.Errorf("create wintun device: %w", err)
	}
	ifName, err := dev.Name()
	if err != nil {
		dev.Close()
		return nil, err
	}
	d := &Device{dev: dev, ifName: ifName, cfg: cfg}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	if err := d.setupStack(); err != nil {
		d.Close()
		return nil, err
	}
	if err := d.setupRoutes(); err != nil {
		d.Close()
		return nil, fmt.Errorf("setup routes: %w", err)
	}
	return d, nil
}

func (d *Device) setupStack() error {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	ep := channel.New(readBuf, uint32(d.cfg.MTU), "")
	if err := s.CreateNIC(nicID, ep); err != nil {
		return fmt.Errorf("create nic: %s", err)
	}
	s.SetPromiscuousMode(nicID, true)
	s.SetSpoofing(nicID, true)

	_, subnet, err := parseCIDR(d.cfg.Subnet)
	if err != nil {
		return err
	}
	gatewayAddr := tcpip.AddrFrom4Slice(net.ParseIP(d.cfg.Gateway).To4())
	protoAddr := tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: gatewayAddr.WithPrefix(),
	}
	if err := s.AddProtocolAddress(nicID, protoAddr, stack.AddressProperties{}); err != nil {
		return fmt.Errorf("add address: %s", err)
	}
	s.SetRouteTable([]tcpip.Route{
		{Destination: subnet, NIC: nicID},
	})

	// TCP forwarder: one proxy connection per TCP flow.
	fwd := tcp.NewForwarder(s, 0, 1024, d.handleTCP)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, fwd.HandlePacket)

	// UDP: fake DNS on the gateway address (port 53).
	ufwd := udp.NewForwarder(s, d.handleUDP)
	s.SetTransportProtocolHandler(udp.ProtocolNumber, ufwd.HandlePacket)

	d.stk, d.ep = s, ep
	d.resolver = newDoHResolver(d.cfg.Dial, d.cfg.DoH)
	d.startPumps()
	return nil
}

func parseCIDR(cidr string) (tcpip.Address, tcpip.Subnet, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return tcpip.Address{}, tcpip.Subnet{}, fmt.Errorf("parse subnet %q: %w", cidr, err)
	}
	addr := tcpip.AddrFrom4Slice(ip.To4())
	subnet, err := tcpip.NewSubnet(addr, tcpip.MaskFromBytes(ipnet.Mask))
	if err != nil {
		return tcpip.Address{}, tcpip.Subnet{}, err
	}
	return addr, subnet, nil
}

func (d *Device) handleTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	var wq waiter.Queue
	ep, err := r.CreateEndpoint(&wq)
	if err != nil {
		r.Complete(true)
		return
	}
	r.Complete(false)
	conn := gonet.NewTCPConn(&wq, ep)
	go func() {
		defer conn.Close()
		ctx, cancel := context.WithTimeout(d.ctx, 15*time.Second)
		defer cancel()
		up, err := d.cfg.Dial("tcp", fmt.Sprintf("%s:%d", id.LocalAddress, id.LocalPort))
		if err != nil {
			return
		}
		defer up.Close()
		splice(ctx, conn, up)
	}()
}

// handleUDP answers DNS queries on the virtual gateway (port 53) via DoH.
// Everything else is dropped (documented v1 limitation).
func (d *Device) handleUDP(r *udp.ForwarderRequest) bool {
	id := r.ID()
	if id.LocalPort != 53 {
		return false
	}
	var wq waiter.Queue
	ep, err := r.CreateEndpoint(&wq)
	if err != nil {
		return false
	}
	go func() {
		defer ep.Close()
		var buf bytes.Buffer
		res, rerr := ep.Read(&buf, tcpip.ReadOptions{NeedRemoteAddr: true})
		if rerr != nil {
			return
		}
		ips, lerr := d.resolver.lookup(buf.Bytes())
		if lerr != nil {
			ips = nil
		}
		resp, berr := buildResponse(buf.Bytes(), ips, 300)
		if berr != nil {
			return
		}
		ep.Write(bytes.NewReader(resp), tcpip.WriteOptions{To: &res.RemoteAddr})
	}()
	return true
}

func (d *Device) startPumps() {
	go func() {
		for {
			bufs := make([][]byte, 1)
			sizes := make([]int, 1)
			bufs[0] = make([]byte, d.cfg.MTU+80)
			n, err := d.dev.Read(bufs, sizes, 0)
			if err != nil {
				return
			}
			if n == 0 {
				continue
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithData(bufs[0][:n]),
			})
			d.ep.InjectInbound(header.IPv4ProtocolNumber, pkt)
		}
	}()
	go func() {
		for {
			pkt := d.ep.ReadContext(d.ctx)
			if pkt == nil {
				return
			}
			buf := pkt.ToBuffer()
			view := buf.Flatten()
			if len(view) > 0 {
				if _, err := d.dev.Write([][]byte{view}, 0); err != nil {
					pkt.DecRef()
					return
				}
			}
			pkt.DecRef()
		}
	}()
}

// setupRoutes assigns the gateway address to the wintun interface and adds
// split-default routes, excluding the server IPs via the physical gateway.
func (d *Device) setupRoutes() error {
	if net.ParseIP(d.cfg.Gateway) == nil {
		return fmt.Errorf("invalid gateway %q", d.cfg.Gateway)
	}
	_, ipnet, err := net.ParseCIDR(d.cfg.Subnet)
	if err != nil {
		return err
	}
	mask := net.IP(ipnet.Mask).String()
	run := func(name string, args ...string) error {
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %v (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run("netsh", "interface", "ip", "set", "address", "name="+d.ifName, "static", d.cfg.Gateway, mask, "none"); err != nil {
		return err
	}
	phyGW := physicalGateway()
	for _, ip := range d.cfg.ExcludeIP {
		if phyGW != "" {
			run("route", "add", ip, "mask", "255.255.255.255", phyGW, "metric", "5")
		}
	}
	for _, cidr := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		parts := strings.SplitN(cidr, "/", 2)
		ip := parts[0]
		m := net.IP(net.CIDRMask(mustAtoi(parts[1]), 32)).String()
		if err := run("route", "add", ip, "mask", m, d.cfg.Gateway, "metric", "5"); err != nil {
			return err
		}
	}
	return nil
}

func (d *Device) teardownRoutes() {
	run := func(name string, args ...string) {
		exec.Command(name, args...).Run()
	}
	for _, cidr := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		parts := strings.SplitN(cidr, "/", 2)
		run("route", "delete", parts[0], "mask", net.IP(net.CIDRMask(mustAtoi(parts[1]), 32)).String())
	}
	phyGW := physicalGateway()
	for _, ip := range d.cfg.ExcludeIP {
		if phyGW != "" {
			run("route", "delete", ip, "mask", "255.255.255.255", phyGW)
		}
	}
	run("netsh", "interface", "ip", "delete", "address", "name="+d.ifName, "gateway=all")
}

// Close tears down routes and releases the device.
func (d *Device) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()
	d.cancel()
	d.teardownRoutes()
	if d.stk != nil {
		d.stk.Destroy()
	}
	if d.dev != nil {
		return d.dev.Close()
	}
	return nil
}

// splice copies bytes bidirectionally until one side ends or ctx is done.
func splice(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		copyUntil(ctx, b, a)
		done <- struct{}{}
	}()
	go func() {
		copyUntil(ctx, a, b)
		done <- struct{}{}
	}()
	<-done
	b.Close()
	a.Close()
}

func copyUntil(ctx context.Context, dst, src net.Conn) {
	go func() {
		<-ctx.Done()
		src.SetDeadline(time.Now())
		dst.SetDeadline(time.Now())
	}()
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func mustAtoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// physicalGateway parses the default gateway from "route print 0.0.0.0".
func physicalGateway() string {
	out, err := exec.Command("route", "print", "0.0.0.0").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "0.0.0.0" && fields[1] == "0.0.0.0" && net.ParseIP(fields[2]) != nil {
			return fields[2]
		}
	}
	return ""
}
