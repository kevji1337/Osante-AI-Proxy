package api

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/proxy"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
)

func newTestHandler(t *testing.T) (*Handler, *storage.SQLiteStorage) {
	t.Helper()
	s, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints(nil)
	p := proxy.New(cfg, proxy.NewSQLiteStatsStorage(s), s, "test")
	return NewHandler(cfg, p, s), s
}

func poolTokens(t *testing.T, s *storage.SQLiteStorage, endpoint string) []string {
	t.Helper()
	creds, err := s.GetEndpointCredentials(endpoint)
	if err != nil {
		t.Fatalf("credentials: %v", err)
	}
	out := make([]string, 0, len(creds))
	for _, c := range creds {
		out = append(out, c.AccessToken)
	}
	return out
}

func doJSON(t *testing.T, h *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1:12710"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestEndpointAPIKeyReachesTokenPool guards the whole point of the api-key field
// in the endpoint form. Token Pool is the only auth mode, so
// ApplyEndpointAuthModeRules always clears Endpoint.APIKey — a key typed into the
// form is only meaningful as a pool token. Create honored that; update accepted
// the key, wiped it and answered "endpoint updated" while storing nothing.
func TestEndpointAPIKeyReachesTokenPool(t *testing.T) {
	h, s := newTestHandler(t)

	rec := doJSON(t, h, "POST", "/api/endpoints",
		`{"name":"ep","apiUrl":"https://api.example.com","apiKey":"sk-first","transformer":"claude","enabled":true}`)
	if rec.Code != 200 {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	if got := poolTokens(t, s, "ep"); len(got) != 1 || got[0] != "sk-first" {
		t.Fatalf("after create pool = %v, want [sk-first]", got)
	}

	// The regression: a new key supplied while editing must be stored too.
	rec = doJSON(t, h, "PUT", "/api/endpoints/ep",
		`{"name":"ep","apiUrl":"https://api.example.com","apiKey":"sk-second","transformer":"claude","enabled":true}`)
	if rec.Code != 200 {
		t.Fatalf("update: status %d body %s", rec.Code, rec.Body.String())
	}
	got := poolTokens(t, s, "ep")
	if len(got) != 2 {
		t.Fatalf("after update pool = %v, want both keys — the key was dropped again", got)
	}

	// Re-submitting the same key must not pile up duplicates.
	doJSON(t, h, "PUT", "/api/endpoints/ep",
		`{"name":"ep","apiUrl":"https://api.example.com","apiKey":"sk-second","transformer":"claude","enabled":true}`)
	if got := poolTokens(t, s, "ep"); len(got) != 2 {
		t.Fatalf("duplicate token stored: %v", got)
	}

	// The UI leaves "****" in the field when the key is unchanged, and the API
	// hands out masked keys; neither may end up in rotation.
	for _, masked := range []string{"****", "****abcd"} {
		doJSON(t, h, "PUT", "/api/endpoints/ep",
			`{"name":"ep","apiUrl":"https://api.example.com","apiKey":"`+masked+`","transformer":"claude","enabled":true}`)
		if got := poolTokens(t, s, "ep"); len(got) != 2 {
			t.Fatalf("masked key %q was stored as a token: %v", masked, got)
		}
	}
}

func TestLooksLikeMaskedKey(t *testing.T) {
	for key, want := range map[string]bool{
		"****":         true,
		"****abcd":     true,
		"  ****abcd  ": true,
		"sk-live-1234": false,
		"":             false,
	} {
		if got := looksLikeMaskedKey(key); got != want {
			t.Errorf("looksLikeMaskedKey(%q) = %v, want %v", key, got, want)
		}
	}
}
