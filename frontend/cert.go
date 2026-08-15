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
	"net"
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
			if i+1 >= len(args) {
				fatal("gencert", fmt.Errorf("-hosts requires a value"))
			}
			i++
			hosts = strings.Split(args[i], ",")
		case "-out":
			if i+1 >= len(args) {
				fatal("gencert", fmt.Errorf("-out requires a value"))
			}
			i++
			out = args[i]
		default:
			fmt.Fprintln(os.Stderr, "unknown arg:", args[i])
			os.Exit(1)
		}
	}
	var dnsNames []string
	var ipAddresses []net.IP
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			fatal("gencert", fmt.Errorf("hosts must not be empty"))
		}
		if ip := net.ParseIP(host); ip != nil {
			ipAddresses = append(ipAddresses, ip)
		} else {
			dnsNames = append(dnsNames, host)
		}
	}
	commonName := "naive.test"
	if len(dnsNames) > 0 {
		commonName = dnsNames[0]
	} else if len(ipAddresses) > 0 {
		commonName = ipAddresses[0].String()
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
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
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caTmpl, &leafKey.PublicKey, caKey)
	if err != nil {
		fatal("leaf cert", err)
	}

	writePEM := func(name, typ string, der []byte, mode os.FileMode) {
		path := filepath.Join(out, name)
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode); err != nil {
			fatal("write "+name, err)
		}
		if err := os.Chmod(path, mode); err != nil {
			fatal("chmod "+name, err)
		}
	}
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		fatal("marshal ca key", err)
	}
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		fatal("marshal server key", err)
	}
	writePEM("ca.pem", "CERTIFICATE", caDER, 0o644)
	writePEM("ca-key.pem", "EC PRIVATE KEY", caKeyDER, 0o600)
	writePEM("server.pem", "CERTIFICATE", leafDER, 0o644)
	writePEM("server-key.pem", "EC PRIVATE KEY", leafKeyDER, 0o600)
	fmt.Println("written ca.pem ca-key.pem server.pem server-key.pem to", out)
	fmt.Println("import ca.pem into the client trust store, then use server.pem/server-key.pem in frontend.toml [inbound.tls]")
}

func fatal(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
