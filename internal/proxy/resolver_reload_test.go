package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
)

// TestResolverFollowsUpdateConfig guards the endpoint resolver against the stale
// config bug: New() used to hand it cfg.GetEndpoints as a bound method value,
// which pinned the original *config.Config. After UpdateConfig (any endpoint
// created, renamed or toggled through the admin API) the resolver kept answering
// from the pre-update snapshot, so X-CCN-Endpoint / ?endpoint= / @endpoint model
// prefixes returned "does not exist or is disabled" until the process restarted.
func TestResolverFollowsUpdateConfig(t *testing.T) {
	first := config.DefaultConfig()
	first.UpdateEndpoints([]config.Endpoint{{
		Name: "one", APIUrl: "https://one.example", AuthMode: config.AuthModeTokenPool,
		Enabled: true, Transformer: "claude",
	}})

	p := New(first, &noopStatsStorage{}, nil, "test")

	updated := config.DefaultConfig()
	updated.UpdateEndpoints([]config.Endpoint{
		{Name: "one", APIUrl: "https://one.example", AuthMode: config.AuthModeTokenPool, Enabled: true, Transformer: "claude"},
		{Name: "two", APIUrl: "https://two.example", AuthMode: config.AuthModeTokenPool, Enabled: true, Transformer: "claude"},
	})
	if err := p.UpdateConfig(updated); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), "POST", "/v1/messages", strings.NewReader(`{"model":"claude-3-5-sonnet"}`))
	req.Header.Set("X-CCN-Endpoint", "two")

	ep, _, err := p.resolver.ResolveEndpoint(req, []byte(`{"model":"claude-3-5-sonnet"}`))
	if err != nil {
		t.Fatalf("resolving an endpoint added by UpdateConfig failed: %v", err)
	}
	if ep == nil || ep.Name != "two" {
		t.Fatalf("resolved endpoint = %v, want \"two\"", ep)
	}
}
