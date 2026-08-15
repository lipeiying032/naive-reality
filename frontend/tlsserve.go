package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
)

// runTLSServe runs a minimal TLS 1.3 server (a stand-in for a real target
// website) used by CI e2e and local REALITY testing. It completes handshakes
// and answers with a tiny page, then closes.
// Usage: naivereal-frontend tlsserve -cert c -key k -listen 127.0.0.1:1443
func runTLSServe(args []string) {
	certPath, keyPath, listen := "", "", "127.0.0.1:1443"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-cert":
			if i+1 >= len(args) {
				fatal("tlsserve", fmt.Errorf("-cert requires a value"))
			}
			i++
			certPath = args[i]
		case "-key":
			if i+1 >= len(args) {
				fatal("tlsserve", fmt.Errorf("-key requires a value"))
			}
			i++
			keyPath = args[i]
		case "-listen":
			if i+1 >= len(args) {
				fatal("tlsserve", fmt.Errorf("-listen requires a value"))
			}
			i++
			listen = args[i]
		default:
			fmt.Fprintln(os.Stderr, "unknown arg:", args[i])
			os.Exit(1)
		}
	}
	if certPath == "" || keyPath == "" {
		fmt.Fprintln(os.Stderr, "usage: tlsserve -cert c.pem -key k.pem [-listen addr]")
		os.Exit(1)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		fatal("load cert", err)
	}
	ln, err := tls.Listen("tcp", listen, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2", "http/1.1"},
	})
	if err != nil {
		fatal("listen", err)
	}
	fmt.Println("tlsserve listening on", listen)
	const page = "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			tc := c.(*tls.Conn)
			if err := tc.Handshake(); err != nil {
				return
			}
			// read whatever arrives, then answer with a tiny page
			buf := make([]byte, 4096)
			for {
				if _, err := tc.Read(buf); err != nil {
					return
				}
				io.WriteString(tc, page)
			}
		}(c)
	}
}
