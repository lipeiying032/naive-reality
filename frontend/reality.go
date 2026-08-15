package main

import (
	"context"
	"crypto/ecdh"
	"fmt"
	"net"
	"time"

	reality "github.com/xtls/reality"
)

// buildRealityConfig constructs the xtls/reality server Config from our settings.
func buildRealityConfig(cfg *Config, dialContext func(ctx context.Context, network, address string) (net.Conn, error)) (*reality.Config, error) {
	privBytes, err := parseRealityPrivateKey(cfg.Inbound.Reality.PrivateKey)
	if err != nil {
		return nil, err
	}
	// Validate that the private key material is usable.
	priv, err := ecdh.X25519().NewPrivateKey(privBytes)
	if err != nil {
		return nil, fmt.Errorf("inbound.reality.private_key: %w", err)
	}
	_ = priv

	names := make(map[string]bool, len(cfg.Inbound.Reality.ServerNames))
	for _, n := range cfg.Inbound.Reality.ServerNames {
		names[n] = true
	}
	ids, err := parseShortIDs(cfg.Inbound.Reality.ShortIDs)
	if err != nil {
		return nil, err
	}

	rc := &reality.Config{
		ServerNames:            names,
		PrivateKey:             privBytes,
		ShortIds:               ids,
		MaxTimeDiff:            time.Duration(cfg.Inbound.Reality.MaxTimeDiff) * time.Millisecond,
		SessionTicketsDisabled: true,
		MinVersion:             reality.VersionTLS13,
		MaxVersion:             reality.VersionTLS13,
		NextProtos:             []string{"h2"},
		DialContext:            dialContext,
		// The fork pre-dials the relay target for every connection: the
		// unauthenticated ClientHello is mirrored to it and fully relayed.
		Type: "tcp",
		Dest: cfg.Inbound.Reality.Target,
	}
	return rc, nil
}
