package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	goreality "github.com/xtls/reality"
)

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

// buildRealityTLSConfig builds the standard crypto/tls config for the QUIC
// listener in REALITY mode. It presents the operator-owned h3_cert/h3_key
// pair when configured, otherwise Dest's real certificate chain signed with
// a throwaway key (only clients that skip CertificateVerify verification can
// complete the handshake).
func buildRealityTLSConfig(ctx context.Context, params *realityQUICParams) (*tls.Config, error) {
	tlsConf := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h3"},
	}
	if params.H3Cert != "" && params.H3Key != "" {
		cert, err := tls.LoadX509KeyPair(params.H3Cert, params.H3Key)
		if err != nil {
			return nil, fmt.Errorf("reality h3 cert/key: %w", err)
		}
		tlsConf.Certificates = []tls.Certificate{cert}
		return tlsConf, nil
	}

	cert, err := destCertChainTLS(ctx, params)
	if err != nil {
		return nil, err
	}
	tlsConf.Certificates = []tls.Certificate{cert}
	return tlsConf, nil
}

// destCertChainTLS fetches Dest's real certificate chain and pairs it with a
// freshly generated throwaway key of the matching type.
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

// newThrowawayKeyForCert returns a freshly generated private key whose type
// matches the certificate's public key, so the TLS 1.3 CertificateVerify
// signature algorithm is compatible with the served leaf certificate.
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
		// Fallback for unknown key types: Ed25519 is always supported by the
		// stock TLS stack.
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		return priv, err
	}
}
