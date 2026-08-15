// Package realitytest implements a REALITY test client (the client side of
// XTLS/REALITY) used to validate REALITY servers: it builds the authenticated
// ClientHello session id, verifies the server's temporary certificate, and
// detects relay-to-real-target situations.
//
// It is a test/reference implementation, not a production client; the
// production client is the patched official naiveproxy kernel.
package realitytest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	utls "github.com/sagernet/utls"
	"golang.org/x/crypto/hkdf"
)

// Client is an in-progress or completed REALITY client handshake.
type Client struct {
	UConn *utls.UConn

	AuthKey       []byte // HKDF-derived AuthKey for this connection
	Verified      bool   // temporary certificate passed the HMAC check
	PresentedLeaf []byte // DER of the leaf certificate the server presented
}

// Dial opens a TCP connection to addr and prepares the REALITY handshake.
// pubKey is the server's static X25519 public key; shortID is the 8-byte
// (right-zero-padded) short id. Handshake must be called afterwards.
func Dial(addr, serverName string, pubKey [32]byte, shortID [8]byte, timeout time.Duration) (*Client, error) {
	raw, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &Client{}
	cfg := &utls.Config{
		ServerName:             serverName,
		InsecureSkipVerify:     true,
		NextProtos:             []string{"h2"},
		MinVersion:             utls.VersionTLS13,
		SessionTicketsDisabled: true,
	}
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, chains [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("realitytest: no certificates presented")
		}
		c.PresentedLeaf = append([]byte(nil), rawCerts[0]...)
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return err
		}
		if pub, ok := cert.PublicKey.(ed25519.PublicKey); ok {
			h := hmac.New(sha512.New, c.AuthKey)
			h.Write(pub)
			if bytes.Equal(h.Sum(nil), cert.Signature) {
				c.Verified = true
				return nil
			}
		}
		return fmt.Errorf("realitytest: not a REALITY temporary certificate")
	}
	uConn := utls.UClient(raw, cfg, utls.HelloChrome_Auto)
	c.UConn = uConn
	if err := uConn.BuildHandshakeState(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("realitytest: BuildHandshakeState: %w", err)
	}
	hello := uConn.HandshakeState.Hello
	if hello.Raw == nil || len(hello.Random) != 32 {
		raw.Close()
		return nil, fmt.Errorf("realitytest: client hello not usable")
	}

	// Build the REALITY session id. The AAD is the serialized hello with the
	// session id field zeroed; the final patched hello is what gets hashed
	// into the transcript and sent on the wire.
	hello.SessionId = make([]byte, 32)
	copy(hello.Raw[39:], hello.SessionId)
	hello.SessionId[0], hello.SessionId[1], hello.SessionId[2] = 1, 0, 0 // client version 1.0.0
	binary.BigEndian.PutUint32(hello.SessionId[4:], uint32(time.Now().Unix()))
	copy(hello.SessionId[8:], shortID[:])

	pub, err := ecdh.X25519().NewPublicKey(pubKey[:])
	if err != nil {
		raw.Close()
		return nil, err
	}
	ecdheKey := uConn.HandshakeState.State13.EcdheKey
	if ecdheKey == nil {
		raw.Close()
		return nil, fmt.Errorf("realitytest: no client X25519 keyshare")
	}
	authKey, err := ecdheKey.ECDH(pub)
	if err != nil {
		raw.Close()
		return nil, err
	}
	if _, err := hkdf.New(sha256.New, authKey, hello.Random[:20], []byte("REALITY")).Read(authKey); err != nil {
		raw.Close()
		return nil, err
	}
	c.AuthKey = append([]byte(nil), authKey...)
	block, err := aes.NewCipher(authKey)
	if err != nil {
		raw.Close()
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		raw.Close()
		return nil, err
	}
	aead.Seal(hello.SessionId[:0], hello.Random[20:], hello.SessionId[:16], hello.Raw)
	copy(hello.Raw[39:], hello.SessionId)
	return c, nil
}

// Handshake completes the TLS handshake. On success (err == nil) the server
// proved possession of the static private key via the temporary certificate
// (Verified == true). When the server instead relays us to a real target
// site, the presented leaf is the target's certificate and the error is
// non-nil with Verified == false.
func (c *Client) Handshake() error {
	return c.UConn.HandshakeContext(context.Background())
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	if c.UConn != nil {
		return c.UConn.Close()
	}
	return nil
}
