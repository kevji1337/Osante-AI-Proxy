package config

import "testing"

// TestApplyEndpointAuthModeRules pins the normalisation every write path relies on:
// the admin API, config load, and the startup migration all run an endpoint through
// this before storing it.
func TestApplyEndpointAuthModeRules(t *testing.T) {
	tests := []struct {
		name            string
		in              Endpoint
		wantAuthMode    string
		wantAPIUrl      string
		wantTransformer string
		wantAPIKeyEmpty bool
	}{
		{
			name:         "api_key keeps its key",
			in:           Endpoint{AuthMode: AuthModeAPIKey, APIUrl: "https://api.anthropic.com/", APIKey: "sk-x", Transformer: "claude"},
			wantAuthMode: AuthModeAPIKey,
			// The trailing slash is always trimmed so URL joins do not double up.
			wantAPIUrl:      "https://api.anthropic.com",
			wantTransformer: "claude",
		},
		{
			name:            "token_pool clears the endpoint key",
			in:              Endpoint{AuthMode: AuthModeTokenPool, APIUrl: "https://api.example.com", APIKey: "sk-x", Transformer: "claude"},
			wantAuthMode:    AuthModeTokenPool,
			wantAPIUrl:      "https://api.example.com",
			wantTransformer: "claude",
			wantAPIKeyEmpty: true,
		},
		{
			// Legacy databases stored the codex backend as a plain token_pool +
			// openai2 endpoint; it has to migrate to codex_token_pool, which pins
			// both the URL and the transformer.
			name:            "legacy codex endpoint migrates",
			in:              Endpoint{AuthMode: AuthModeTokenPool, APIUrl: "https://chatgpt.com/backend-api/codex", APIKey: "sk-x", Transformer: "openai2"},
			wantAuthMode:    AuthModeCodexTokenPool,
			wantAPIUrl:      CodexTokenPoolAPIURL,
			wantTransformer: CodexTokenPoolTransformer,
			wantAPIKeyEmpty: true,
		},
		{
			name:            "unknown auth mode falls back to api_key",
			in:              Endpoint{AuthMode: "something-else", APIUrl: "https://api.example.com", APIKey: "sk-x", Transformer: "claude"},
			wantAuthMode:    AuthModeAPIKey,
			wantAPIUrl:      "https://api.example.com",
			wantTransformer: "claude",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ep := tc.in
			ApplyEndpointAuthModeRules(&ep)

			if ep.AuthMode != tc.wantAuthMode {
				t.Errorf("AuthMode = %q, want %q", ep.AuthMode, tc.wantAuthMode)
			}
			if ep.APIUrl != tc.wantAPIUrl {
				t.Errorf("APIUrl = %q, want %q", ep.APIUrl, tc.wantAPIUrl)
			}
			if ep.Transformer != tc.wantTransformer {
				t.Errorf("Transformer = %q, want %q", ep.Transformer, tc.wantTransformer)
			}
			if got := ep.APIKey == ""; got != tc.wantAPIKeyEmpty {
				t.Errorf("APIKey empty = %v, want %v (key=%q)", got, tc.wantAPIKeyEmpty, ep.APIKey)
			}
		})
	}

	// Must not panic on a nil endpoint: several call sites pass a pointer straight
	// out of a lookup that can miss.
	ApplyEndpointAuthModeRules(nil)
}

func TestIsTokenPoolAuthMode(t *testing.T) {
	for mode, want := range map[string]bool{
		AuthModeTokenPool:      true,
		AuthModeCodexTokenPool: true,
		"  TOKEN_POOL  ":       true,
		AuthModeAPIKey:         false,
		"":                     false,
		"nonsense":             false,
	} {
		if got := IsTokenPoolAuthMode(mode); got != want {
			t.Errorf("IsTokenPoolAuthMode(%q) = %v, want %v", mode, got, want)
		}
	}
}

func TestValidateRejectsBadConfigs(t *testing.T) {
	t.Run("port out of range", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Port = 70000
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() accepted port 70000")
		}
	})

	t.Run("no endpoints", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.UpdateEndpoints([]Endpoint{})
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() accepted a config with no endpoints")
		}
	})

	t.Run("api_key endpoint without a key", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.UpdateEndpoints([]Endpoint{{
			Name: "ep", APIUrl: "https://api.example.com", AuthMode: AuthModeAPIKey, Enabled: true,
		}})
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() accepted an api_key endpoint with an empty key")
		}
	})

	t.Run("token_pool endpoint needs no key", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.UpdateEndpoints([]Endpoint{{
			Name: "ep", APIUrl: "https://api.example.com", AuthMode: AuthModeTokenPool, Enabled: true,
		}})
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() rejected a keyless token_pool endpoint: %v", err)
		}
	})

	t.Run("empty transformer defaults to claude", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.UpdateEndpoints([]Endpoint{{
			Name: "ep", APIUrl: "https://api.example.com", AuthMode: AuthModeTokenPool, Enabled: true,
		}})
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if got := cfg.GetEndpoints()[0].Transformer; got != "claude" {
			t.Errorf("Transformer = %q, want claude", got)
		}
	})
}
