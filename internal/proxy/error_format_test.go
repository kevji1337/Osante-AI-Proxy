package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteProxyError checks that writeProxyError emits a well-formed
// Anthropic-style JSON envelope and the right Content-Type. Hermes and
// similar clients try to parse the body as JSON to surface a useful
// error message; without this we get an empty "Error:" field and an
// AssertionError on the client side.
func TestWriteProxyError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeProxyError(rec, http.StatusServiceUnavailable, "overloaded_error", "All endpoints failed")

	if got, want := rec.Code, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not JSON: %v\nbody=%q", err, rec.Body.String())
	}
	if payload.Type != "error" {
		t.Errorf("outer type = %q, want %q", payload.Type, "error")
	}
	if payload.Error.Type != "overloaded_error" {
		t.Errorf("error.type = %q, want %q", payload.Error.Type, "overloaded_error")
	}
	if payload.Error.Message != "All endpoints failed" {
		t.Errorf("error.message = %q, want %q", payload.Error.Message, "All endpoints failed")
	}
}

// TestErrTypeForStatus pins the HTTP-status → Anthropic-error-type mapping.
// Used by writeLastUpstreamError when the upstream body is not JSON and we
// have to invent an error envelope on its behalf.
func TestErrTypeForStatus(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "invalid_request_error"},
		{http.StatusUnauthorized, "authentication_error"},
		{http.StatusForbidden, "permission_error"},
		{http.StatusNotFound, "not_found_error"},
		{http.StatusRequestEntityTooLarge, "request_too_large"},
		{http.StatusTooManyRequests, "rate_limit_error"},
		{http.StatusPaymentRequired, "rate_limit_error"},
		{http.StatusInternalServerError, "overloaded_error"},
		{http.StatusBadGateway, "overloaded_error"},
		{http.StatusServiceUnavailable, "overloaded_error"},
		{http.StatusGatewayTimeout, "overloaded_error"},
		{http.StatusTeapot, "api_error"}, // anything we don't special-case
	}
	for _, c := range cases {
		if got := errTypeForStatus(c.status); got != c.want {
			t.Errorf("errTypeForStatus(%d) = %q, want %q", c.status, got, c.want)
		}
	}
}

// TestWriteLastUpstreamError_HTMLBody — when the upstream body is Cloudflare
// HTML or plain text, we MUST synthesize a JSON envelope. Forwarding the
// HTML directly is exactly what was making Hermes blow up with an
// AssertionError ("Error:" with nothing parseable after it).
func TestWriteLastUpstreamError_HTMLBody(t *testing.T) {
	htmlBody := []byte(`<!DOCTYPE html><html><body><h1>502 Bad Gateway</h1><p>cloudflare</p></body></html>`)

	p := &Proxy{}
	reqCtx := &proxyRequestContext{
		lastUpstreamStatus: http.StatusBadGateway,
		lastUpstreamBody:   htmlBody,
		lastUpstreamHeader: http.Header{},
	}
	rec := httptest.NewRecorder()
	if !p.writeLastUpstreamError(rec, reqCtx) {
		t.Fatal("writeLastUpstreamError returned false; expected true")
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "{") {
		t.Fatalf("body is not JSON: %q", rec.Body.String())
	}

	var payload struct {
		Type  string `json:"type"`
		Error struct{ Type, Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not parseable as JSON: %v", err)
	}
	if payload.Type != "error" || payload.Error.Type != "overloaded_error" {
		t.Errorf("unexpected envelope: %+v", payload)
	}
	if !strings.Contains(payload.Error.Message, "502") && !strings.Contains(payload.Error.Message, "Bad Gateway") {
		t.Errorf("message should mention upstream body content; got %q", payload.Error.Message)
	}
}

// TestWriteLastUpstreamError_JSONBody — when the upstream IS JSON, forward
// as-is so the original error type/message reaches the client.
func TestWriteLastUpstreamError_JSONBody(t *testing.T) {
	jsonBody := []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"quota exceeded"}}`)
	p := &Proxy{}
	reqCtx := &proxyRequestContext{
		lastUpstreamStatus: http.StatusTooManyRequests,
		lastUpstreamBody:   jsonBody,
		lastUpstreamHeader: http.Header{"Content-Type": []string{"application/json"}},
	}
	rec := httptest.NewRecorder()
	if !p.writeLastUpstreamError(rec, reqCtx) {
		t.Fatal("writeLastUpstreamError returned false")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if !strings.Contains(rec.Body.String(), "quota exceeded") {
		t.Fatalf("expected original body to be forwarded; got %q", rec.Body.String())
	}
}

// TestExtractUpstreamErrorMessage covers every upstream JSON shape we've
// seen in the wild. The non-Anthropic shapes MUST be parseable so
// writeLastUpstreamError can rewrap them — without this, Hermes/Claude
// Code see the original JSON, fail to find error.message in their schema,
// and crash with AssertionError + a blank "Error:" field.
func TestExtractUpstreamErrorMessage(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantMsg     string
		wantIsAnthropic bool
	}{
		{
			name:            "anthropic shape",
			body:            `{"type":"error","error":{"type":"rate_limit_error","message":"quota exceeded"}}`,
			wantMsg:         "quota exceeded",
			wantIsAnthropic: true,
		},
		{
			name:            "freemodel flat error string",
			body:            `{"error":"Usage limit reached, will reset on today at 10:32 PM (UTC+8)"}`,
			wantMsg:         "Usage limit reached, will reset on today at 10:32 PM (UTC+8)",
			wantIsAnthropic: false,
		},
		{
			name:            "openai nested error object",
			body:            `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`,
			wantMsg:         "You exceeded your current quota",
			wantIsAnthropic: false,
		},
		{
			name:            "generic message field",
			body:            `{"message":"something went wrong"}`,
			wantMsg:         "something went wrong",
			wantIsAnthropic: false,
		},
		{
			name:            "generic detail field",
			body:            `{"detail":"Bad gateway"}`,
			wantMsg:         "Bad gateway",
			wantIsAnthropic: false,
		},
		{
			name:            "cloudflare-style with title",
			body:            `{"type":"https://developers.cloudflare.com/...","title":"Error 502: Bad gateway","status":502,"detail":"the origin..."}`,
			// `type` is non-"error" string, so we fall through; `detail` wins
			// over `title` in our lookup order.
			wantMsg:         "the origin...",
			wantIsAnthropic: false,
		},
		{
			name:            "anthropic type but missing message",
			body:            `{"type":"error","error":{"type":"api_error"}}`,
			wantMsg:         "",
			wantIsAnthropic: false,
		},
		{
			name:            "unparseable",
			body:            `not json at all`,
			wantMsg:         "",
			wantIsAnthropic: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, isAnthropic := extractUpstreamErrorMessage([]byte(c.body))
			if msg != c.wantMsg {
				t.Errorf("msg = %q, want %q", msg, c.wantMsg)
			}
			if isAnthropic != c.wantIsAnthropic {
				t.Errorf("isAnthropic = %v, want %v", isAnthropic, c.wantIsAnthropic)
			}
		})
	}
}

// TestWriteLastUpstreamError_FreemodelJSON — the exact regression that
// re-broke Hermes today. Freemodel returns `{"error":"Usage limit
// reached..."}` with status 402. Old behaviour: forward as-is, Hermes
// can't find error.message, AssertionError. New behaviour: rewrap into
// Anthropic envelope with the message preserved.
func TestWriteLastUpstreamError_FreemodelJSON(t *testing.T) {
	body := []byte(`{"error":"Usage limit reached, will reset on today at 10:32 PM (UTC+8)"}`)
	p := &Proxy{}
	reqCtx := &proxyRequestContext{
		lastUpstreamStatus: http.StatusPaymentRequired,
		lastUpstreamBody:   body,
		lastUpstreamHeader: http.Header{"Content-Type": []string{"application/json"}},
	}
	rec := httptest.NewRecorder()
	if !p.writeLastUpstreamError(rec, reqCtx) {
		t.Fatal("writeLastUpstreamError returned false")
	}
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPaymentRequired)
	}

	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON: %v\nbody=%q", err, rec.Body.String())
	}
	if payload.Type != "error" {
		t.Errorf("outer type = %q, want %q", payload.Type, "error")
	}
	if payload.Error.Type != "rate_limit_error" {
		t.Errorf("error.type = %q, want %q (402→rate_limit)", payload.Error.Type, "rate_limit_error")
	}
	if !strings.Contains(payload.Error.Message, "Usage limit reached") {
		t.Errorf("error.message lost original text: %q", payload.Error.Message)
	}
}
