package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
)

// TestRetryLoopStopsWhenTheClientHangsUp is the point of the whole change: the
// attempt context used to descend from context.Background(), so a client pressing
// Esc stopped nothing. The loop kept walking endpoints and tokens, spending real
// upstream quota on a reply nobody would read — up to
// len(endpoints)*2 + (usable tokens - 1) calls, each up to ResponseHeaderTimeout.
func TestRetryLoopStopsWhenTheClientHangsUp(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		// Fail so the loop wants to retry, and stall long enough that the test can
		// cancel mid-flight.
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream down"}}`))
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	endpoints := make([]config.Endpoint, 0, 4)
	for _, name := range []string{"one", "two", "three", "four"} {
		endpoints = append(endpoints, config.Endpoint{
			Name: name, APIUrl: upstream.URL, APIKey: "sk-test",
			AuthMode: config.AuthModeAPIKey, Enabled: true, Transformer: "claude",
		})
	}
	cfg.UpdateEndpoints(endpoints)

	p := New(cfg, &noopStatsStorage{}, nil, "test")

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		p.handleProxyRequest(rec, req)
		close(done)
	}()

	// Let one attempt get under way, then hang up like a client would.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handleProxyRequest did not return after the client canceled")
	}

	// Without the fix this reaches maxRetries (8 for four api_key endpoints).
	if got := upstreamCalls.Load(); got > 3 {
		t.Errorf("upstream was called %d times after the client hung up; the loop is not honoring cancellation", got)
	}
}

// TestClientCancellationIsNotBlamedOnTheEndpoint covers the other half: a
// canceled attempt fails at the transport, which looks exactly like an endpoint
// problem. Recording it would put a healthy endpoint into the error state and mark
// its token as failing because the user pressed Esc.
func TestClientCancellationIsNotBlamedOnTheEndpoint(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never answer on our own: the only outcome is the client's cancellation.
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release)

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name: "stalling", APIUrl: upstream.URL, APIKey: "sk-test",
		AuthMode: config.AuthModeAPIKey, Enabled: true, Transformer: "claude",
	}})

	p := New(cfg, &noopStatsStorage{}, nil, "test")

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	go func() {
		p.handleProxyRequest(httptest.NewRecorder(), req)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handleProxyRequest did not return after the client canceled")
	}

	for name, rt := range p.EndpointRuntimeSnapshot() {
		if rt.HasError {
			t.Errorf("endpoint %q was marked failed because the client canceled: %q", name, rt.LastError)
		}
	}
}

// TestRequestContextForMergesBothCancellations pins the two-sided contract: the
// attempt dies with the client, and also with an endpoint-level switch.
func TestRequestContextForMergesBothCancellations(t *testing.T) {
	p := New(config.DefaultConfig(), &noopStatsStorage{}, nil, "test")

	t.Run("client cancellation", func(t *testing.T) {
		clientCtx, cancelClient := context.WithCancel(context.Background())
		ctx, release := p.requestContextFor(clientCtx, "ep")
		defer release()

		cancelClient()
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("attempt context did not follow the client")
		}
	})

	t.Run("endpoint cancellation", func(t *testing.T) {
		ctx, release := p.requestContextFor(context.Background(), "ep2")
		defer release()

		p.cancelEndpointRequests("ep2")
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("attempt context did not follow the endpoint switch")
		}
	})

	t.Run("nil client context falls back to the endpoint context", func(t *testing.T) {
		// clientContext() returns nil when the request context is unavailable (a
		// reqCtx built in a test), and requestContextFor documents that fallback.
		// The nil goes through a variable so this stays a deliberate case rather
		// than a literal staticcheck flags.
		var noClient context.Context
		ctx, release := p.requestContextFor(noClient, "ep3")
		defer release()
		if ctx == nil {
			t.Fatal("requestContextFor returned a nil context")
		}
		if err := ctx.Err(); err != nil {
			t.Errorf("fresh context is already done: %v", err)
		}
	})
}
