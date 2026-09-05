package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
)

// originHandler makes the application-layer decision after standard TLS has
// authenticated the server and protected this request. There is deliberately no
// authorization cache keyed by address, CID, connection, or earlier request.
type originHandler struct {
	credentialHash [sha256.Size]byte
	website        http.Handler
	proxy          http.Handler
}

func newOriginHandler(cfg OriginConfig, proxy http.Handler) (*originHandler, func() error, error) {
	// Root.FS also confines symlink resolution to the configured website root.
	root, err := os.OpenRoot(cfg.WebRoot)
	if err != nil {
		return nil, nil, err
	}
	files := http.FileServerFS(root.FS())
	website := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A 2xx CONNECT response means a tunnel is established. FileServer can
		// otherwise return a file with status 200 for this method.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		files.ServeHTTP(w, r)
	})
	return &originHandler{
		credentialHash: sha256.Sum256([]byte(cfg.Username + ":" + cfg.Password)),
		website:        website,
		proxy:          proxy,
	}, root.Close, nil
}

func (h *originHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect && h.authorized(r) {
		h.proxy.ServeHTTP(w, r)
		return
	}
	h.website.ServeHTTP(w, r)
}

func (h *originHandler) authorized(r *http.Request) bool {
	values := r.Header.Values("Proxy-Authorization")
	if len(values) != 1 {
		return false
	}
	scheme, encoded, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return false
	}
	credential, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(credential)
	return subtle.ConstantTimeCompare(digest[:], h.credentialHash[:]) == 1
}
