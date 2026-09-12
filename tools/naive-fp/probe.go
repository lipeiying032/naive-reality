package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
)

// report is the normalised, diffable description of what a probe observed.
//
// Fields are deliberately strings and ordered slices rather than maps where the
// order carries the signal: server-library classifiers score on the encoding
// order of the transport-parameter block, so it must survive into the JSON.
type report struct {
	URL      string `json:"url"`
	Host     string `json:"host"`
	ProbeAt  string `json:"probe_at"`
	Duration string `json:"duration"`

	// Handshake holds properties of the completed QUIC handshake.
	Handshake handshake `json:"handshake"`
	// TransportParameters is the peer's parameter block in wire order.
	TransportParameters []param `json:"transport_parameters"`

	// HTTP holds what an origin-role observer can see at the application layer.
	HTTP httpObservation `json:"http"`

	// Errors records non-fatal observations (e.g. a request that was refused).
	Errors []string `json:"errors,omitempty"`
}

type handshake struct {
	QUICVersion    string `json:"quic_version"`
	VersionHex     string `json:"quic_version_hex"`
	ALPN           string `json:"alpn"`
	TLSVersion     string `json:"tls_version"`
	CipherSuite    string `json:"cipher_suite"`
	ServerName     string `json:"server_name"`
	Used0RTT       bool   `json:"used_0rtt"`
	GSO            bool   `json:"gso"`
}

// param is one transport parameter as encoded, retaining raw bytes so that a
// re-serialisation cannot hide an encoding difference.
type param struct {
	Index   int    `json:"index"`
	ID      string `json:"id"`
	IDDec   uint64 `json:"id_dec"`
	Value   string `json:"value"`
	ValueHex string `json:"value_hex"`
	Len     int    `json:"len"`
	// Decoded is set for parameters whose value is a single varint.
	Decoded *uint64 `json:"decoded,omitempty"`
}

type httpObservation struct {
	Status        int                 `json:"status"`
	Proto         string              `json:"proto"`
	AltSvc        string              `json:"alt_svc,omitempty"`
	ContentLength int64               `json:"content_length"`
	ContentType   string              `json:"content_type,omitempty"`
	Headers       map[string][]string `json:"headers"`
}

type probeOptions struct {
	url       string
	authority string
	insecure  bool
	path      string
	alpn      string
}

func probe(opts probeOptions) (*report, error) {
	u, err := url.Parse(opts.url)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("url scheme must be https, got %q", u.Scheme)
	}
	authority := opts.authority
	if authority == "" {
		authority = u.Host
	}
	reqPath := opts.path
	if opts.path == "/" && u.Path != "" {
		reqPath = u.Path
	}

	rep := &report{
		URL:     opts.url,
		Host:    u.Host,
		ProbeAt: time.Now().UTC().Format(time.RFC3339),
	}

	var observed *quic.Conn
	tlsConf := &tls.Config{
		ServerName:         u.Hostname(),
		InsecureSkipVerify: opts.insecure, //nolint:gosec // probe-only escape hatch
		NextProtos:         []string{opts.alpn},
	}
	tr := &http3.Transport{
		TLSClientConfig: tlsConf,
		QUICConfig: &quic.Config{
			HandshakeIdleTimeout: 10 * time.Second,
			MaxIdleTimeout:       10 * time.Second,
			// Keep the transport-parameter capture scoped to this handshake.
			Tracer: nil,
		},
		Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			conn, err := quic.DialAddr(ctx, addr, tlsCfg, cfg)
			observed = conn
			return conn, err
		},
	}
	defer tr.Close()

	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, "https://"+authority+reqPath, nil)
	if err != nil {
		return rep, fmt.Errorf("build request: %w", err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		rep.Duration = time.Since(start).String()
		captureConnection(rep, observed)
		return rep, fmt.Errorf("round trip: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		rep.Errors = append(rep.Errors, "read body: "+readErr.Error())
	}
	rep.Duration = time.Since(start).String()

	captureConnection(rep, observed)
	rep.HTTP = httpObservation{
		Status:        resp.StatusCode,
		Proto:         resp.Proto,
		AltSvc:        resp.Header.Get("Alt-Svc"),
		ContentLength: int64(len(body)),
		ContentType:   resp.Header.Get("Content-Type"),
		Headers:       normaliseHeaders(resp.Header),
	}
	return rep, nil
}

// captureConnection records everything observable about the completed handshake.
func captureConnection(rep *report, conn *quic.Conn) {
	if conn == nil {
		rep.Errors = append(rep.Errors, "no QUIC connection was established")
		return
	}
	cs := conn.ConnectionState()
	rep.Handshake = handshake{
		QUICVersion:    cs.Version.String(),
		VersionHex:     fmt.Sprintf("0x%08x", uint32(cs.Version)),
		ALPN:           cs.TLS.NegotiatedProtocol,
		TLSVersion:     tlsVersionName(cs.TLS.Version),
		CipherSuite:    tls.CipherSuiteName(cs.TLS.CipherSuite),
		ServerName:     cs.TLS.ServerName,
		Used0RTT:       cs.Used0RTT,
		GSO:            cs.GSO,
	}
	for i, p := range quic.CapturedTransportParameters() {
		item := param{
			Index:    i,
			ID:       paramName(p.ID),
			IDDec:    p.ID,
			Value:    printable(p.Value),
			ValueHex: fmt.Sprintf("%x", p.Value),
			Len:      len(p.Value),
		}
		if v, ok := decodeVarint(p.Value); ok {
			item.Decoded = &v
		}
		rep.TransportParameters = append(rep.TransportParameters, item)
	}
	if len(rep.TransportParameters) == 0 {
		rep.Errors = append(rep.Errors, "no transport parameters were captured")
	}
}

func normaliseHeaders(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, v := range h {
		vals := append([]string(nil), v...)
		sort.Strings(vals)
		out[strings.ToLower(k)] = vals
	}
	return out
}

// decodeVarint reports the value of a parameter encoded as a single QUIC varint.
func decodeVarint(b []byte) (uint64, bool) {
	if len(b) == 0 || len(b) > 8 {
		return 0, false
	}
	v, err := readVarint(b)
	if err != nil || len(b) != varintLen(b[0]) {
		return 0, false
	}
	return v, true
}

func printable(b []byte) string {
	const maxLen = 64
	truncated := false
	if len(b) > maxLen {
		b, truncated = b[:maxLen], true
	}
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('.')
		}
	}
	if truncated {
		sb.WriteString("...")
	}
	return sb.String()
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}

// paramName maps the RFC 9000 / extension codepoints that matter for
// implementation fingerprinting. Unknown ids are reported numerically so that a
// GREASE parameter cannot be silently dropped.
func paramName(id uint64) string {
	switch id {
	case 0x0:
		return "original_destination_connection_id"
	case 0x1:
		return "max_idle_timeout"
	case 0x2:
		return "stateless_reset_token"
	case 0x3:
		return "max_udp_payload_size"
	case 0x4:
		return "initial_max_data"
	case 0x5:
		return "initial_max_stream_data_bidi_local"
	case 0x6:
		return "initial_max_stream_data_bidi_remote"
	case 0x7:
		return "initial_max_stream_data_uni"
	case 0x8:
		return "initial_max_streams_bidi"
	case 0x9:
		return "initial_max_streams_uni"
	case 0xa:
		return "ack_delay_exponent"
	case 0xb:
		return "max_ack_delay"
	case 0xc:
		return "disable_active_migration"
	case 0xd:
		return "preferred_address"
	case 0xe:
		return "active_connection_id_limit"
	case 0xf:
		return "initial_source_connection_id"
	case 0x10:
		return "retry_source_connection_id"
	case 0x11:
		return "version_information"
	case 0x20:
		return "max_datagram_frame_size"
	case 0x3127:
		return "google_initial_rtt"
	case 0x3128:
		return "google_connection_options"
	case 0xff04de1a:
		return "grease_version_information"
	}
	// RFC 9000 §18.1 reserves 31*N+27 for GREASE.
	if id >= 27 && (id-27)%31 == 0 {
		return fmt.Sprintf("GREASE(0x%x)", id)
	}
	return fmt.Sprintf("unknown(0x%x)", id)
}

func varintLen(b byte) int {
	switch b >> 6 {
	case 0:
		return 1
	case 1:
		return 2
	case 2:
		return 4
	default:
		return 8
	}
}

func readVarint(b []byte) (uint64, error) {
	l := varintLen(b[0])
	if len(b) < l {
		return 0, fmt.Errorf("short varint")
	}
	var v uint64
	switch l {
	case 1:
		v = uint64(b[0] & 0x3f)
	case 2:
		v = uint64(b[0]&0x3f)<<8 | uint64(b[1])
	case 4:
		for i := 0; i < 4; i++ {
			v = v<<8 | uint64(b[i])
		}
		v &= 0x3fffffff
	default:
		for i := 0; i < 8; i++ {
			v = v<<8 | uint64(b[i])
		}
		v &= 0x3fffffffffffffff
	}
	return v, nil
}
