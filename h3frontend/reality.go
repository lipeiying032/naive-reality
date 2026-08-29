package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
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

// realityCertSource keeps the certificate chain presented to authenticated
// clients. Its signer replaces only the CertificateVerify signature with the
// per-connection REALITY server proof, leaving the certificate bytes identical
// to the borrowed destination certificate.
type realityCertSource struct {
	chain  [][]byte
	public crypto.PublicKey
}

func newRealityCertSource(ctx context.Context, params *realityQUICParams) (*realityCertSource, error) {
	var cert tls.Certificate
	if params.H3Cert != "" && params.H3Key != "" {
		var err error
		cert, err = tls.LoadX509KeyPair(params.H3Cert, params.H3Key)
		if err != nil {
			return nil, fmt.Errorf("reality h3 cert/key: %w", err)
		}
	} else {
		var err error
		cert, err = destCertChainTLS(ctx, params)
		if err != nil {
			return nil, err
		}
	}
	if len(cert.Certificate) == 0 {
		return nil, errors.New("reality: certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("reality: parse certificate: %w", err)
	}
	return &realityCertSource{chain: cert.Certificate, public: leaf.PublicKey}, nil
}

func (s *realityCertSource) certificate(authKey []byte) tls.Certificate {
	return tls.Certificate{
		Certificate: s.chain,
		PrivateKey:  realityProofSigner{public: s.public, authKey: authKey},
	}
}

// realityProofSigner makes crypto/tls send the REALITY proof as the
// CertificateVerify signature. The signature is not a valid signature for the
// borrowed certificate's public key; that mismatch is the inherent cost of
// borrowing a certificate chain without its private key.
type realityProofSigner struct {
	public  crypto.PublicKey
	authKey []byte
}

func (s realityProofSigner) Public() crypto.PublicKey {
	return s.public
}

func (s realityProofSigner) Sign(_ io.Reader, _ []byte, _ crypto.SignerOpts) ([]byte, error) {
	if len(s.authKey) != 32 {
		return nil, errors.New("reality: invalid auth key for server proof")
	}
	return quicServerProof(s.authKey), nil
}

// destCertChainTLS fetches Dest's real certificate chain and pairs it with a
// throwaway key of the matching type. Only the signature length/type matters to
// crypto/tls; REALITY clients verify the embedded per-connection proof instead.
func destCertChainTLS(ctx context.Context, params *realityQUICParams) (tls.Certificate, error) {
	fc := &goreality.Config{
		Dest:           params.Dest,
		DestServerName: params.DestServerName,
	}
	chain := goreality.GetDestCertChain(ctx, fc)
	if len(chain) == 0 {
		return tls.Certificate{}, fmt.Errorf("reality: failed to fetch dest certificate chain for %q", params.Dest)
	}
	priv, err := newThrowawayKeyForCert(chain[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("reality: throwaway key for dest cert: %w", err)
	}
	return tls.Certificate{Certificate: chain, PrivateKey: priv}, nil
}

func newThrowawayKeyForCert(der []byte) (crypto.Signer, error) {
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	switch pub := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		return rsa.GenerateKey(rand.Reader, 2048)
	case *ecdsa.PublicKey:
		return ecdsa.GenerateKey(pub.Curve, rand.Reader)
	case ed25519.PublicKey:
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		return priv, err
	default:
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		return priv, err
	}
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
// REALITY mode. GetCertificate presents the configured certificate chain (the
// borrowed Dest chain by default) and selects a per-connection signer that
// places the server proof in the CertificateVerify signature.
func buildRealityTLSConfig(ctx context.Context, params *realityQUICParams) (*tls.Config, *realityAuthSource, error) {
	authSource := &realityAuthSource{}
	certSource, err := newRealityCertSource(ctx, params)
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
			cert := certSource.certificate(authKey)
			return &cert, nil
		},
	}
	return tlsConf, authSource, nil
}
