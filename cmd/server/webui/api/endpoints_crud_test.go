package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// decodeData pulls the "data" object out of the WriteSuccess envelope.
func decodeData(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var env struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
		Error   string                 `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, body)
	}
	if env.Error != "" {
		t.Fatalf("API returned an error: %s", env.Error)
	}
	return env.Data
}

func createEndpoint(t *testing.T, h *Handler, name string) {
	t.Helper()
	rec := doJSON(t, h, "POST", "/api/endpoints",
		`{"name":"`+name+`","apiUrl":"https://api.example.com","apiKey":"sk-`+name+`","transformer":"claude","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create %s: status %d body %s", name, rec.Code, rec.Body.String())
	}
}

func TestCreateEndpointValidation(t *testing.T) {
	h, _ := newTestHandler(t)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"missing name", `{"apiUrl":"https://api.example.com","transformer":"claude"}`, http.StatusBadRequest},
		{"missing apiUrl", `{"name":"ep","transformer":"claude"}`, http.StatusBadRequest},
		{"malformed JSON", `{"name":`, http.StatusBadRequest},
		{"valid", `{"name":"ep","apiUrl":"https://api.example.com","transformer":"claude","enabled":true}`, http.StatusOK},
		{"duplicate name", `{"name":"ep","apiUrl":"https://api.example.com","transformer":"claude","enabled":true}`, http.StatusConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, h, "POST", "/api/endpoints", tc.body)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestCreateEndpointForcesTokenPool pins the one auth mode this build supports:
// whatever the client sends, the endpoint is stored as token_pool with an empty
// APIKey column, and the key it supplied lives in the pool instead.
func TestCreateEndpointForcesTokenPool(t *testing.T) {
	h, s := newTestHandler(t)

	rec := doJSON(t, h, "POST", "/api/endpoints",
		`{"name":"ep","apiUrl":"https://api.example.com/","apiKey":"sk-live","authMode":"api_key","transformer":"claude","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	stored, err := s.GetEndpoints()
	if err != nil || len(stored) != 1 {
		t.Fatalf("GetEndpoints: %v (%d rows)", err, len(stored))
	}
	if stored[0].AuthMode != "token_pool" {
		t.Errorf("AuthMode = %q, want token_pool despite the client asking for api_key", stored[0].AuthMode)
	}
	if stored[0].APIKey != "" {
		t.Errorf("APIKey column = %q, want empty for a token-pool endpoint", stored[0].APIKey)
	}
	// The trailing slash must be trimmed so URL joins do not double up.
	if stored[0].APIUrl != "https://api.example.com" {
		t.Errorf("APIUrl = %q, want the trailing slash trimmed", stored[0].APIUrl)
	}
	if got := poolTokens(t, s, "ep"); len(got) != 1 || got[0] != "sk-live" {
		t.Errorf("pool = %v, want [sk-live]", got)
	}
}

func TestToggleEndpoint(t *testing.T) {
	h, s := newTestHandler(t)
	createEndpoint(t, h, "ep")

	rec := doJSON(t, h, "PATCH", "/api/endpoints/ep/toggle", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle off: %d %s", rec.Code, rec.Body.String())
	}
	if data := decodeData(t, rec.Body.Bytes()); data["enabled"] != false {
		t.Errorf("response says enabled=%v, want false", data["enabled"])
	}
	stored, _ := s.GetEndpoints()
	if stored[0].Enabled {
		t.Error("endpoint is still enabled in storage")
	}

	rec = doJSON(t, h, "PATCH", "/api/endpoints/ep/toggle", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle on: %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = s.GetEndpoints()
	if !stored[0].Enabled {
		t.Error("endpoint was not re-enabled in storage")
	}

	if rec := doJSON(t, h, "PATCH", "/api/endpoints/missing/toggle", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("toggling an unknown endpoint = %d, want 404", rec.Code)
	}
}

func TestReorderEndpoints(t *testing.T) {
	h, s := newTestHandler(t)
	for _, name := range []string{"first", "second", "third"} {
		createEndpoint(t, h, name)
	}

	rec := doJSON(t, h, "POST", "/api/endpoints/reorder", `{"names":["third","first","second"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}

	// GetEndpoints orders by sort_order, so the returned order is the new one.
	stored, err := s.GetEndpoints()
	if err != nil {
		t.Fatalf("GetEndpoints: %v", err)
	}
	got := []string{stored[0].Name, stored[1].Name, stored[2].Name}
	want := []string{"third", "first", "second"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}

	// An unknown name in the list must be ignored, not fail the request.
	if rec := doJSON(t, h, "POST", "/api/endpoints/reorder", `{"names":["nope","third"]}`); rec.Code != http.StatusOK {
		t.Errorf("reorder with an unknown name = %d, want 200", rec.Code)
	}
}

// TestDeleteEndpointRemovesItsTokens guards the cascade: credentials are keyed by
// endpoint name with no foreign key, so a delete that forgets them leaves orphaned
// tokens that no UI can reach.
func TestDeleteEndpointRemovesItsTokens(t *testing.T) {
	h, s := newTestHandler(t)
	createEndpoint(t, h, "ep")

	if got := poolTokens(t, s, "ep"); len(got) != 1 {
		t.Fatalf("expected the seeded token before delete, got %v", got)
	}

	rec := doJSON(t, h, "DELETE", "/api/endpoints/ep", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	stored, _ := s.GetEndpoints()
	if len(stored) != 0 {
		t.Errorf("endpoint still present after delete: %+v", stored)
	}
	if got := poolTokens(t, s, "ep"); len(got) != 0 {
		t.Errorf("credentials survived the delete: %v", got)
	}
}

func TestListEndpointsMasksKeys(t *testing.T) {
	h, _ := newTestHandler(t)
	createEndpoint(t, h, "ep")

	rec := doJSON(t, h, "GET", "/api/endpoints", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// The pool holds sk-ep; the listing must never echo a full key.
	if strings.Contains(body, "sk-ep") {
		t.Errorf("the listing leaked a full API key: %s", body)
	}
	if !strings.Contains(body, `"tokenPools"`) || !strings.Contains(body, `"states"`) {
		t.Errorf("listing is missing tokenPools/states: %s", body)
	}
}
