package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
)

// failingResponseTransformer accepts requests but cannot make sense of the
// upstream response — the shape a truncated/unknown upstream body produces.
type failingResponseTransformer struct{}

func (t *failingResponseTransformer) Name() string { return "test_transform_failure" }

func (t *failingResponseTransformer) TransformRequest(claudeReq []byte) ([]byte, error) {
	return claudeReq, nil
}

func (t *failingResponseTransformer) TransformResponse(targetResp []byte, isStreaming bool) ([]byte, error) {
	return nil, errors.New("upstream body not understood")
}

func (t *failingResponseTransformer) TransformResponseWithContext(targetResp []byte, isStreaming bool, ctx *transformer.StreamContext) ([]byte, error) {
	return t.TransformResponse(targetResp, isStreaming)
}

// TestUpstream200TransformFailureFailsOver guards the worst failure mode this
// proxy had: an upstream 200 whose body could not be transformed fell through to
// handleFinalStatus, which re-read the already-consumed body and emitted HTTP 200
// with an empty payload. The client saw a successful but blank answer, no
// failover happened and no error was recorded.
func TestUpstream200TransformFailureFailsOver(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "one",
		APIUrl:      "https://example.com",
		AuthMode:    config.AuthModeTokenPool,
		Enabled:     true,
		Transformer: "openai2",
	}})

	p := &Proxy{
		stats:            NewStats(&noopStatsStorage{}, "test"),
		endpointStates:   make(map[string]*endpointRuntimeState),
		activeRequests:   make(map[string]int),
		usageLimitLogged: make(map[string]time.Time),
	}
	p.config.Store(cfg)

	reqCtx := &proxyRequestContext{}
	attempt := &endpointAttempt{
		endpoint:    cfg.GetEndpoints()[0],
		transformer: &failingResponseTransformer{},
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"unexpected":"shape"}`)),
		},
	}

	w := &recordingWriter{ResponseRecorder: httptest.NewRecorder()}
	result := p.handleAttemptResponse(w, reqCtx, attempt)

	if result != attemptResultRetryNextEndpoint {
		t.Errorf("result = %v, want attemptResultRetryNextEndpoint (%v)", result, attemptResultRetryNextEndpoint)
	}
	// Nothing may reach the client here: the retry loop still owns the response,
	// and writeExhaustedResponse produces the real error if every endpoint fails.
	// Emitting a bare 200 at this point was exactly the bug.
	if w.wroteHeader || w.wroteBody {
		t.Fatalf("response was written to the client (header=%v body=%v, status=%d) instead of failing over",
			w.wroteHeader, w.wroteBody, w.Code)
	}
	if rt, ok := p.EndpointRuntimeSnapshot()["one"]; !ok || !rt.HasError {
		t.Errorf("endpoint error was not recorded: %+v", rt)
	}
}

// recordingWriter tracks whether the handler wrote anything at all, which a bare
// httptest.ResponseRecorder cannot tell us (its Code is pre-seeded with 200).
type recordingWriter struct {
	*httptest.ResponseRecorder
	wroteHeader bool
	wroteBody   bool
}

func (w *recordingWriter) WriteHeader(code int) {
	w.wroteHeader = true
	w.ResponseRecorder.WriteHeader(code)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	if len(b) > 0 {
		w.wroteBody = true
	}
	return w.ResponseRecorder.Write(b)
}

// noopStatsStorage swallows stat writes so tests need no database.
type noopStatsStorage struct{}

func (noopStatsStorage) RecordDailyStat(StatRecord) error { return nil }
func (noopStatsStorage) GetTotalStats() (int, map[string]StatsData, error) {
	return 0, map[string]StatsData{}, nil
}
