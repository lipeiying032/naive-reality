package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusRequiresBearerToken(t *testing.T) {
	s := &tunnelServer{cfg: &Config{Status: StatusConfig{Token: "secret"}}, stats: &Stats{}}
	tests := []struct {
		name       string
		target     string
		auth       string
		wantStatus int
	}{
		{"missing", "/stats", "", http.StatusForbidden},
		{"query token rejected", "/stats?token=secret", "", http.StatusForbidden},
		{"wrong bearer", "/stats", "Bearer wrong", http.StatusForbidden},
		{"valid bearer", "/stats", "Bearer secret", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			w := httptest.NewRecorder()
			s.statusHandler().ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK && !strings.Contains(w.Body.String(), `"active":0`) {
				t.Fatalf("unexpected body: %s", w.Body.String())
			}
		})
	}
}
