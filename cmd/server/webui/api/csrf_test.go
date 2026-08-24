package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLocalOnlyMiddleware covers the CSRF / DNS-rebinding guard on the
// unauthenticated admin API. The API has no login by design, so these header
// checks are the only thing standing between a page the user visits and a POST
// to http://127.0.0.1:12710/api/endpoints.
func TestLocalOnlyMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("reached"))
	})
	handler := LocalOnlyMiddleware(next)

	tests := []struct {
		name        string
		method      string
		host        string
		headers     map[string]string
		wantStatus  int
		wantReached bool
	}{
		{
			name: "plain GET from a non-browser client", method: http.MethodGet,
			wantStatus: http.StatusOK, wantReached: true,
		},
		{
			name: "first-party GET from the UI", method: http.MethodGet,
			headers:    map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://127.0.0.1:12710"},
			wantStatus: http.StatusOK, wantReached: true,
		},
		{
			name: "cross-site fetch", method: http.MethodGet,
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "foreign Origin", method: http.MethodGet,
			headers:    map[string]string{"Origin": "http://evil.example"},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "rebound Host header", method: http.MethodGet,
			host:       "evil.example",
			wantStatus: http.StatusForbidden,
		},
		{
			// A JSON body sent as text/plain is a CORS "simple request": no
			// preflight, so without this check the write would land.
			name: "mutating request smuggled as text/plain", method: http.MethodPost,
			headers:    map[string]string{"Content-Type": "text/plain"},
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name: "mutating request declaring JSON", method: http.MethodPost,
			headers:    map[string]string{"Content-Type": "application/json; charset=utf-8"},
			wantStatus: http.StatusOK, wantReached: true,
		},
		{
			// Several handlers take no body at all (toggle, reorder).
			name: "mutating request with no Content-Type", method: http.MethodPost,
			wantStatus: http.StatusOK, wantReached: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tc.method, "http://127.0.0.1:12710/api/endpoints", strings.NewReader("{}"))
			if tc.host != "" {
				req.Host = tc.host
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if reached := strings.Contains(rec.Body.String(), "reached"); reached != tc.wantReached {
				t.Errorf("handler reached = %v, want %v", reached, tc.wantReached)
			}
		})
	}
}

func TestIsLoopbackHostHeader(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:12710": true,
		"127.0.0.1":       true,
		"localhost:12710": true,
		"LOCALHOST":       true,
		"[::1]:12710":     true,
		"127.5.5.5":       true,
		"192.168.1.10":    false,
		"evil.example":    false,
		"":                false,
	}
	for host, want := range cases {
		if got := isLoopbackHostHeader(host); got != want {
			t.Errorf("isLoopbackHostHeader(%q) = %v, want %v", host, got, want)
		}
	}
}
