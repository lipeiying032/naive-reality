package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runGencert generates a private CA plus a leaf certificate for the given
// hosts. Useful for local testing and for tls-mode deployments behind a
// private CA. Usage: naivereal-frontend gencert [-hosts a.test,b.test] [-out dir]
func runGencert(args []string) {
	hosts := []string{"naive.test"}
	out := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-hosts":
			i++
			hosts = strings.Split(args[i], ",")
		case "-out":
			i++
			out = args[i]
		default:
			fmt.Fprintln(os.Stderr, "unknown arg:", args[i])
			os.Exit(1)
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}

	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatal("ca key", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "naivereal test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		fatal("ca cert", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatal("leaf key", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     hosts,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caTmpl, &leafKey.PublicKey, caKey)
	if err != nil {
		fatal("leaf cert", err)
	}

	writePEM := func(name, typ string, der []byte) {
		if err := os.WriteFile(filepath.Join(out, name), pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o644); err != nil {
			fatal("write "+name, err)
		}
	}
	caKeyDER, _ := x509.MarshalECPrivateKey(caKey)
	leafKeyDER, _ := x509.MarshalECPrivateKey(leafKey)
	writePEM("ca.pem", "CERTIFICATE", caDER)
	writePEM("ca-key.pem", "EC PRIVATE KEY", caKeyDER)
	writePEM("server.pem", "CERTIFICATE", leafDER)
	writePEM("server-key.pem", "EC PRIVATE KEY", leafKeyDER)
	fmt.Println("written ca.pem ca-key.pem server.pem server-key.pem to", out)
	fmt.Println("import ca.pem into the client trust store, then use server.pem/server-key.pem in frontend.toml [inbound.tls]")
}

func fatal(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
