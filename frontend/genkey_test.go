package main

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestGenerateKeypairRoundtrip(t *testing.T) {
	privB64, pubB64, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	privBytes, err := base64.RawURLEncoding.DecodeString(privB64)
	if err != nil || len(privBytes) != 32 {
		t.Fatalf("private key decode: %v (len %d)", err, len(privBytes))
	}
	priv, err := ecdh.X25519().NewPrivateKey(privBytes)
	if err != nil {
		t.Fatal(err)
	}
	wantPub := base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
	if wantPub != pubB64 {
		t.Errorf("public key mismatch: got %s want %s", pubB64, wantPub)
	}
}

// RFC 7748 X25519 test vector: base point multiplication.
func TestX25519RFC7748Vector(t *testing.T) {
	scalar, _ := hex.DecodeString("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
	want, _ := hex.DecodeString("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	priv, err := ecdh.X25519().NewPrivateKey(scalar)
	if err != nil {
		t.Fatal(err)
	}
	got := priv.PublicKey().Bytes()
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("pub = %x, want %x", got, want)
	}
}
