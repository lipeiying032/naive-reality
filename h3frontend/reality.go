package main

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	goreality "github.com/xtls/reality"
)

var quicServerProofContext = []byte("naivereal QUIC REALITY server proof v1")

// realityAuthSource connects the packet-level ClientHello precheck to the
// crypto/tls certificate callback. quic-go supplies a fake net.Conn carrying
// the QUIC connection's remote address to that callback.
type realityAuthSource struct {
	mu     sync.RWMutex
	lookup func(net.Addr) ([]byte, bool)
}

func (s *realityAuthSource) setLookup(lookup func(net.Addr) ([]byte, bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookup = lookup
}

func (s *realityAuthSource) AuthKeyFor(addr net.Addr) ([]byte, bool) {
	if s == nil || addr == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lookup == nil {
		return nil, false
	}
	return s.lookup(addr)
}

// quicServerProof proves server possession of the REALITY secret for this
// connection. AuthKey came from ECDH(client ephemeral, server REALITY private)
// in the precheck, so only the authenticated server can produce it.
func quicServerProof(authKey []byte) []byte {
	mac := hmac.New(sha512.New, authKey)
	_, _ = mac.Write(quicServerProofContext)
	return mac.Sum(nil)
}

// realityCertIssuer issues one short-lived leaf per authenticated connection.
// The proof is placed in SubjectKeyId; the client checks the exact expected
// 64 bytes before accepting the REALITY handshake.
type realityCertIssuer struct {
	template *x509.Certificate
	priv     ed25519.PrivateKey
}

func newRealityCertIssuer(ctx context.Context, params *realityQUICParams) (*realityCertIssuer, error) {
	var leafDER []byte
	if params.H3Cert != "" && params.H3Key != "" {
		cert, err := tls.LoadX509KeyPair(params.H3Cert, params.H3Key)
		if err != nil {
			return nil, fmt.Errorf("reality h3 cert/key: %w", err)
		}
		leafDER = cert.Certificate[0]
	} else {
		fc := &goreality.Config{
			Dest:           params.Dest,
			DestServerName: params.DestServerName,
		}
		chain := goreality.GetDestCertChain(ctx, fc)
		if len(chain) == 0 {
			return nil, fmt.Errorf("reality: failed to fetch dest certificate chain for %q", params.Dest)
		}
		leafDER = chain[0]
	}
	template, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, fmt.Errorf("reality: parse certificate template: %w", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &realityCertIssuer{template: template, priv: priv}, nil
}

func (i *realityCertIssuer) certificate(proof []byte) (tls.Certificate, error) {
	template := *i.template
	template.PublicKey = i.priv.Public()
	template.SubjectKeyId = proof
	template.SignatureAlgorithm = x509.PureEd25519
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, i.priv.Public(), i.priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: i.priv}, nil
}

// realityQUICParams carries the REALITY-over-QUIC server parameters used by
// the precheck/relay wrapper and the TLS listener. It mirrors the reference
// h3-reality-deploy RealityQUICParams (MIT/Xray-core).
type realityQUICParams struct {
	PrivateKey   []byte
	ShortIds     map[[8]byte]bool
	ServerNames  map[string]bool
	MinClientVer []byte
	MaxClientVer []byte
	MaxTimeDiff  time.Duration

	Dest           string
	DestServerName string

	H3Cert string
	H3Key  string

	FallbackTimeout time.Duration
}

// buildRealityParams decodes and validates the REALITY block into the
// parameters consumed by the precheck and TLS layers.
func buildRealityParams(cfg *Config) (*realityQUICParams, error) {
	priv, err := parseRealityPrivateKey(cfg.Reality.PrivateKey)
	if err != nil {
		return nil, err
	}
	shortIDs, err := parseShortIDs(cfg.Reality.ShortIDs)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(cfg.Reality.ServerNames))
	for _, n := range cfg.Reality.ServerNames {
		names[n] = true
	}
	fallbackTimeout, err := time.ParseDuration(cfg.Reality.FallbackTimeout)
	if err != nil {
		return nil, fmt.Errorf("reality.fallback_timeout: %w", err)
	}
	return &realityQUICParams{
		PrivateKey:      priv,
		ShortIds:        shortIDs,
		ServerNames:     names,
		MaxTimeDiff:     time.Duration(cfg.Reality.MaxTimeDiff) * time.Millisecond,
		Dest:            cfg.Reality.Dest,
		DestServerName:  cfg.Reality.DestServerName,
		H3Cert:          cfg.Reality.H3Cert,
		H3Key:           cfg.Reality.H3Key,
		FallbackTimeout: fallbackTimeout,
	}, nil
}

// buildRealityTLSConfig builds the crypto/tls config for the QUIC listener in
// REALITY mode. GetCertificate issues a short-lived leaf for each flow that
// passed precheck; its SubjectKeyId carries the per-connection server proof.
// The operator's h3_cert/h3_key (or Dest's real leaf) supplies only the
// certificate identity template.
func buildRealityTLSConfig(ctx context.Context, params *realityQUICParams) (*tls.Config, *realityAuthSource, error) {
	authSource := &realityAuthSource{}
	issuer, err := newRealityCertIssuer(ctx, params)
	if err != nil {
		return nil, nil, err
	}
	tlsConf := &tls.Config{
		MinVersion:             tls.VersionTLS13,
		NextProtos:             []string{"h3"},
		SessionTicketsDisabled: true,
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if info == nil || info.Conn == nil {
				return nil, errors.New("reality: no connection for certificate callback")
			}
			authKey, ok := authSource.AuthKeyFor(info.Conn.RemoteAddr())
			if !ok {
				return nil, fmt.Errorf("reality: no authenticated QUIC flow for %s", info.Conn.RemoteAddr())
			}
			cert, err := issuer.certificate(quicServerProof(authKey))
			if err != nil {
				return nil, err
			}
			return &cert, nil
		},
	}
	return tlsConf, authSource, nil
}
