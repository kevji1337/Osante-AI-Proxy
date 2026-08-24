package proxy

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
)

// newPaymentRequiredFixture builds a proxy backed by real SQLite storage with one
// token-pool endpoint holding n tokens, plus the 402 attempt to feed it.
func newPaymentRequiredFixture(t *testing.T, tokens int, authMode string, body string) (*Proxy, *storage.SQLiteStorage, *proxyRequestContext, *endpointAttempt) {
	t.Helper()

	s, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name: "ep", APIUrl: "https://api.example.com", AuthMode: authMode,
		Enabled: true, Transformer: "claude",
	}})

	p := New(cfg, &noopStatsStorage{}, s, "test")

	var credID int64
	for i := 0; i < tokens; i++ {
		cred := &storage.EndpointCredential{
			EndpointName: "ep", ProviderType: "api_key", AccessToken: "tok",
			Status: "active", Enabled: true,
		}
		if err := s.SaveEndpointCredential(cred); err != nil {
			t.Fatalf("seed token %d: %v", i, err)
		}
		if i == 0 {
			credID = cred.ID
		}
	}

	attempt := &endpointAttempt{
		endpoint:     cfg.GetEndpoints()[0],
		authMode:     authMode,
		credentialID: credID,
		response: &http.Response{
			StatusCode: http.StatusPaymentRequired,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		},
	}
	return p, s, &proxyRequestContext{}, attempt
}

// TestPaymentRequiredCoolsDownTheTokenNotTheEndpoint is the core of the 402
// handling: with a token pool, a usage limit belongs to the one token that hit it.
// Cooling the whole endpoint would strand the remaining tokens.
func TestPaymentRequiredCoolsDownTheTokenNotTheEndpoint(t *testing.T) {
	const body = `{"error":"Usage limit reached, will reset on today at 14:00"}`
	p, s, reqCtx, attempt := newPaymentRequiredFixture(t, 3, config.AuthModeTokenPool, body)

	result := p.handlePaymentRequired(reqCtx, attempt)

	if result != attemptResultRetrySameEndpoint {
		t.Errorf("result = %v, want attemptResultRetrySameEndpoint — the pool should advance to the next token", result)
	}

	// The offending token is in cooldown...
	stats, err := s.GetTokenPoolStats("ep")
	if err != nil {
		t.Fatalf("pool stats: %v", err)
	}
	if stats.Cooldown != 1 {
		t.Errorf("cooldown tokens = %d, want 1: %+v", stats.Cooldown, stats)
	}
	if stats.Active != 2 {
		t.Errorf("active tokens = %d, want the other 2 still usable: %+v", stats.Active, stats)
	}

	// ...and the endpoint itself is not, so the retry can use it.
	rt := p.EndpointRuntimeSnapshot()["ep"]
	if !rt.CooldownUntil.IsZero() {
		t.Errorf("endpoint was put in cooldown until %v, stranding the remaining tokens", rt.CooldownUntil)
	}

	// The upstream body is remembered so an exhausted loop can replay the real error.
	if reqCtx.lastUpstreamStatus != http.StatusPaymentRequired {
		t.Errorf("lastUpstreamStatus = %d, want 402", reqCtx.lastUpstreamStatus)
	}
	if !strings.Contains(string(reqCtx.lastUpstreamBody), "Usage limit reached") {
		t.Errorf("upstream body was not retained: %q", reqCtx.lastUpstreamBody)
	}
}

// TestPaymentRequiredCoolsDownTheEndpointWithoutAPool covers the other branch:
// with no token to blame, the endpoint takes the cooldown and the loop moves on.
func TestPaymentRequiredCoolsDownTheEndpointWithoutAPool(t *testing.T) {
	const body = `{"error":"Usage limit reached, will reset on tomorrow at 09:00"}`
	p, _, reqCtx, attempt := newPaymentRequiredFixture(t, 0, config.AuthModeAPIKey, body)

	result := p.handlePaymentRequired(reqCtx, attempt)

	if result != attemptResultRetryNextEndpoint {
		t.Errorf("result = %v, want attemptResultRetryNextEndpoint", result)
	}
	rt := p.EndpointRuntimeSnapshot()["ep"]
	if rt.CooldownUntil.IsZero() {
		t.Fatal("endpoint was not put in cooldown despite a usage limit and no pool")
	}
	if !rt.CooldownUntil.After(time.Now().UTC()) {
		t.Errorf("cooldown is in the past: %v", rt.CooldownUntil)
	}
	if rt.CooldownReason == "" {
		t.Error("cooldown has no reason recorded, so the UI cannot explain it")
	}
}

// TestPaymentRequiredWithoutUsageLimit covers a 402 that is not a quota message —
// a billing failure, say. It must not be mistaken for a usage limit, because the
// cooldown for those is hours long.
func TestPaymentRequiredWithoutUsageLimit(t *testing.T) {
	const body = `{"error":{"message":"card declined"}}`
	p, _, reqCtx, attempt := newPaymentRequiredFixture(t, 2, config.AuthModeTokenPool, body)

	result := p.handlePaymentRequired(reqCtx, attempt)

	if result != attemptResultRetryNextEndpoint {
		t.Errorf("result = %v, want attemptResultRetryNextEndpoint", result)
	}
	rt := p.EndpointRuntimeSnapshot()["ep"]
	if !rt.CooldownUntil.IsZero() {
		t.Errorf("a non-quota 402 set a usage-limit cooldown until %v", rt.CooldownUntil)
	}
	if !rt.HasError {
		t.Error("the failure was not recorded against the endpoint at all")
	}
}

// TestComputeMaxRetriesScalesWithTheTokenPool pins the retry budget: it has to be
// big enough to walk every usable token, and bounded enough that an unreachable
// upstream does not spin forever.
func TestComputeMaxRetriesScalesWithTheTokenPool(t *testing.T) {
	s, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = s.Close() }()

	cfg := config.DefaultConfig()
	p := New(cfg, &noopStatsStorage{}, s, "test")

	apiKeyEndpoints := []config.Endpoint{
		{Name: "a", AuthMode: config.AuthModeAPIKey, Enabled: true},
		{Name: "b", AuthMode: config.AuthModeAPIKey, Enabled: true},
	}
	if got := p.computeMaxRetries(apiKeyEndpoints); got != 4 {
		t.Errorf("two api_key endpoints = %d retries, want len*2 = 4", got)
	}

	if got := p.computeMaxRetries(nil); got != 0 {
		t.Errorf("no endpoints = %d retries, want 0", got)
	}

	// A pool with 4 usable tokens adds 3 extra attempts on top of the base 2.
	pooled := []config.Endpoint{{Name: "pool", AuthMode: config.AuthModeTokenPool, Enabled: true}}
	for i := 0; i < 4; i++ {
		if err := s.SaveEndpointCredential(&storage.EndpointCredential{
			EndpointName: "pool", ProviderType: "api_key", AccessToken: "tok",
			Status: "active", Enabled: true,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if got := p.computeMaxRetries(pooled); got != 5 {
		t.Errorf("one pool endpoint with 4 tokens = %d retries, want 2 + 3 = 5", got)
	}
}
