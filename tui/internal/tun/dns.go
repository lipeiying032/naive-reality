// Package tun implements the TUN-mode data plane (Windows wintun + gVisor).
// This file contains the platform-independent DNS codec and DoH resolver.
package tun

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	flagQR    = 0x8000
	flagRD    = 0x0100
	flagRA    = 0x0080
	qtypeA    = 1
	qtypeAAAA = 28
)

// dnsHeader is the 12-byte DNS header layout.
type dnsHeader struct {
	ID      uint16
	Flags   uint16
	QDCount uint16
	ANCount uint16
	NSCount uint16
	ARCount uint16
}

// parseQuery extracts the first question from a DNS query packet.
func parseQuery(pkt []byte) (h dnsHeader, name string, qtype uint16, err error) {
	h, name, qtype, _, err = parseQueryWithEnd(pkt)
	return
}

func parseQueryWithEnd(pkt []byte) (h dnsHeader, name string, qtype uint16, questionEnd int, err error) {
	if len(pkt) < 12 {
		return h, "", 0, 0, fmt.Errorf("dns packet too short")
	}
	h.ID = binary.BigEndian.Uint16(pkt[0:])
	h.Flags = binary.BigEndian.Uint16(pkt[2:])
	h.QDCount = binary.BigEndian.Uint16(pkt[4:])
	h.ANCount = binary.BigEndian.Uint16(pkt[6:])
	if h.QDCount != 1 {
		return h, "", 0, 0, fmt.Errorf("dns query must contain exactly one question")
	}
	off := 12
	var labels []string
	for {
		if off >= len(pkt) {
			return h, "", 0, 0, fmt.Errorf("dns name overruns packet")
		}
		l := int(pkt[off])
		off++
		if l == 0 {
			break
		}
		if l&0xC0 == 0xC0 {
			return h, "", 0, 0, fmt.Errorf("compressed qname unsupported")
		}
		if off+l > len(pkt) {
			return h, "", 0, 0, fmt.Errorf("dns label overruns packet")
		}
		labels = append(labels, string(pkt[off:off+l]))
		off += l
	}
	if off+4 > len(pkt) {
		return h, "", 0, 0, fmt.Errorf("dns question overruns packet")
	}
	qtype = binary.BigEndian.Uint16(pkt[off:])
	name = strings.Join(labels, ".")
	return h, name, qtype, off + 4, nil
}

// buildResponse renders a DNS answer carrying the given A/AAAA records.
func buildResponse(query []byte, ips []net.IP, ttl uint32) ([]byte, error) {
	h, _, qtype, questionEnd, err := parseQueryWithEnd(query)
	if err != nil {
		return nil, err
	}
	filtered := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if (qtype == qtypeA && ip.To4() != nil) || (qtype == qtypeAAAA && ip.To4() == nil && ip.To16() != nil) {
			filtered = append(filtered, ip)
		}
	}
	ips = filtered
	resp := make([]byte, 0, 512)
	resp = append(resp, query[:12]...)
	flags := flagQR | flagRA | (h.Flags & flagRD) | (h.Flags & 0x7800)
	if len(ips) == 0 {
		flags |= 3 // NXDOMAIN
	}
	binary.BigEndian.PutUint16(resp[2:], flags)
	// Echo only the question. Additional records such as EDNS OPT must follow
	// the answer section, so they cannot be copied ahead of generated answers.
	resp = append(resp, query[12:questionEnd]...)
	binary.BigEndian.PutUint16(resp[8:], 0)
	binary.BigEndian.PutUint16(resp[10:], 0)
	if len(ips) == 0 {
		binary.BigEndian.PutUint16(resp[6:], 0)
		return resp, nil
	}
	binary.BigEndian.PutUint16(resp[6:], uint16(len(ips)))
	for _, ip := range ips {
		resp = append(resp, 0xC0, 0x0C) // name pointer to offset 12
		var typ uint16 = qtypeA
		var rdata []byte
		if v4 := ip.To4(); v4 != nil {
			rdata = v4
		} else {
			typ = qtypeAAAA
			rdata = ip.To16()
		}
		var rr [10]byte
		binary.BigEndian.PutUint16(rr[0:], typ)
		binary.BigEndian.PutUint16(rr[2:], 1) // class IN
		binary.BigEndian.PutUint32(rr[4:], ttl)
		binary.BigEndian.PutUint16(rr[8:], uint16(len(rdata)))
		resp = append(resp, rr[:]...)
		resp = append(resp, rdata...)
	}
	return resp, nil
}

// dohResolver resolves names via DNS-over-HTTPS through the tunnel.
type dohResolver struct {
	endpoints []string
	client    *http.Client
}

func newDoHResolver(dial func(network, addr string) (net.Conn, error), endpoints []string) *dohResolver {
	if len(endpoints) == 0 {
		endpoints = []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dial(network, addr)
		},
		ForceAttemptHTTP2: true,
	}
	return &dohResolver{endpoints: endpoints, client: &http.Client{Transport: tr, Timeout: 10 * time.Second}}
}

// lookup sends the DNS query to the DoH endpoints and returns A/AAAA records.
func (r *dohResolver) lookup(query []byte) ([]net.IP, error) {
	var lastErr error
	for _, ep := range r.endpoints {
		ips, err := r.lookupOnce(ep, query)
		if err == nil {
			return ips, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (r *dohResolver) lookupOnce(endpoint string, query []byte) ([]net.IP, error) {
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(string(query)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/dns-message")
	req.Header.Set("accept", "application/dns-message")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("doh status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return nil, err
	}
	return parseAnswers(body)
}

// parseAnswers extracts A/AAAA records from a DNS response.
func parseAnswers(pkt []byte) ([]net.IP, error) {
	if len(pkt) < 12 {
		return nil, fmt.Errorf("dns response too short")
	}
	anCount := int(binary.BigEndian.Uint16(pkt[6:]))
	off := 12
	// skip question section
	for {
		if off >= len(pkt) {
			return nil, fmt.Errorf("dns response overruns")
		}
		l := int(pkt[off])
		off++
		if l == 0 {
			break
		}
		if l&0xC0 == 0xC0 {
			off++
			break
		}
		off += l
	}
	off += 4
	var ips []net.IP
	for i := 0; i < anCount && off+10 <= len(pkt); i++ {
		if off >= len(pkt) {
			break
		}
		if pkt[off]&0xC0 == 0xC0 {
			off += 2
		} else {
			for off < len(pkt) && pkt[off] != 0 {
				off += int(pkt[off]) + 1
			}
			off++
		}
		if off+10 > len(pkt) {
			break
		}
		typ := binary.BigEndian.Uint16(pkt[off:])
		rdlen := int(binary.BigEndian.Uint16(pkt[off+8:]))
		off += 10
		if off+rdlen > len(pkt) {
			break
		}
		switch typ {
		case qtypeA:
			if rdlen == 4 {
				ips = append(ips, net.IP(append([]byte(nil), pkt[off:off+4]...)))
			}
		case qtypeAAAA:
			if rdlen == 16 {
				ips = append(ips, net.IP(append([]byte(nil), pkt[off:off+16]...)))
			}
		}
		off += rdlen
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no answers")
	}
	return ips, nil
}
