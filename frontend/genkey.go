package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

// generateKeypair creates a fresh X25519 key pair, returned as base64url strings.
func generateKeypair() (privB64, pubB64 string, err error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	privB64 = base64.RawURLEncoding.EncodeToString(priv.Bytes())
	pubB64 = base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
	return privB64, pubB64, nil
}

func runGenkey() {
	privB64, pubB64, err := generateKeypair()
	if err != nil {
		fmt.Fprintln(os.Stderr, "genkey:", err)
		os.Exit(1)
	}
	fmt.Printf("Private key: %s\n", privB64)
	fmt.Printf("Public key:  %s\n", pubB64)
	fmt.Println()
	fmt.Println("# add to frontend.toml:")
	fmt.Println("[inbound.reality]")
	fmt.Printf("private_key = %q\n", privB64)
	fmt.Println("short_ids = [\"0123456789abcdef\"]")
	fmt.Println("server_names = [\"www.example.com\"]")
	fmt.Println()
	fmt.Println("# client side (config.json reality block or naivereal share link):")
	fmt.Printf("public_key: %s\n", pubB64)
}
