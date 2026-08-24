package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestParseImportCredentialsPayload covers the three shapes the import endpoint
// accepts. Users paste whatever their token dumper produced, so all three have to
// keep working — and anything else has to fail with a message rather than import
// nothing silently.
func TestParseImportCredentialsPayload(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantItems int
		wantErr   bool
	}{
		{
			name:      "wrapped in items with options",
			body:      `{"items":[{"access_token":"a"},{"access_token":"b"}],"overwrite":true,"remark":"batch"}`,
			wantItems: 2,
		},
		{
			name:      "bare array",
			body:      `[{"access_token":"a"},{"access_token":"b"},{"access_token":"c"}]`,
			wantItems: 3,
		},
		{
			name:      "single object",
			body:      `{"access_token":"only"}`,
			wantItems: 1,
		},
		{name: "empty items list", body: `{"items":[]}`, wantErr: true},
		{name: "object without a token", body: `{"remark":"no token here"}`, wantErr: true},
		{name: "not JSON", body: `nonsense`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, items, err := parseImportCredentialsPayload([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %d items", len(items))
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(items) != tc.wantItems {
				t.Errorf("items = %d, want %d", len(items), tc.wantItems)
			}
			if req == nil {
				t.Fatal("request options are nil")
			}
		})
	}

	// The options only exist on the wrapped form; make sure they are read.
	req, _, err := parseImportCredentialsPayload([]byte(`{"items":[{"access_token":"a"}],"overwrite":true,"remark":"batch"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !req.Overwrite || req.Remark != "batch" {
		t.Errorf("options lost: overwrite=%v remark=%q", req.Overwrite, req.Remark)
	}
}

func TestMaskToken(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"short":                   "****",
		"exactlyten":              "****",
		"sk-abcdefghijklmnop":     "sk-abc...mnop",
		"  sk-abcdefghijklmnop  ": "sk-abc...mnop",
	}
	for token, want := range cases {
		if got := maskToken(token); got != want {
			t.Errorf("maskToken(%q) = %q, want %q", token, got, want)
		}
	}
}

func TestImportCredentialsEndpoint(t *testing.T) {
	h, s := newTestHandler(t)
	createEndpoint(t, h, "ep") // seeds sk-ep as the first pool token

	rec := doJSON(t, h, "POST", "/api/endpoints/ep/credentials/import",
		`{"items":[{"access_token":"tok-a","email":"a@example.com"},{"access_token":"tok-b"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}

	data := decodeData(t, rec.Body.Bytes())
	if got := data["created"]; got != float64(2) {
		t.Errorf("created = %v, want 2 (body %s)", got, rec.Body.String())
	}
	if got := poolTokens(t, s, "ep"); len(got) != 3 {
		t.Errorf("pool = %v, want 3 tokens", got)
	}

	// Re-importing the same account without overwrite must skip, not duplicate.
	rec = doJSON(t, h, "POST", "/api/endpoints/ep/credentials/import",
		`{"items":[{"access_token":"tok-a2","email":"a@example.com"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-import: %d %s", rec.Code, rec.Body.String())
	}
	if got := decodeData(t, rec.Body.Bytes())["skipped"]; got != float64(1) {
		t.Errorf("skipped = %v, want 1", got)
	}

	// With overwrite it updates the existing row instead of adding one.
	before := len(poolTokens(t, s, "ep"))
	rec = doJSON(t, h, "POST", "/api/endpoints/ep/credentials/import",
		`{"items":[{"access_token":"tok-a3","email":"a@example.com"}],"overwrite":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("overwrite import: %d %s", rec.Code, rec.Body.String())
	}
	if got := decodeData(t, rec.Body.Bytes())["updated"]; got != float64(1) {
		t.Errorf("updated = %v, want 1", got)
	}
	if after := len(poolTokens(t, s, "ep")); after != before {
		t.Errorf("pool size changed on overwrite: %d -> %d", before, after)
	}

	// An item with no token is reported as failed rather than silently dropped.
	rec = doJSON(t, h, "POST", "/api/endpoints/ep/credentials/import", `{"items":[{"email":"x@example.com"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("bad item import: %d %s", rec.Code, rec.Body.String())
	}
	data = decodeData(t, rec.Body.Bytes())
	if got := data["failed"]; got != float64(1) {
		t.Errorf("failed = %v, want 1", got)
	}
	if errs, ok := data["errors"].([]interface{}); !ok || len(errs) == 0 {
		t.Errorf("no error detail was reported: %s", rec.Body.String())
	}

	if rec := doJSON(t, h, "POST", "/api/endpoints/missing/credentials/import", `{"items":[{"access_token":"x"}]}`); rec.Code != http.StatusNotFound {
		t.Errorf("import into an unknown endpoint = %d, want 404", rec.Code)
	}
}

func TestListCredentialsMasksTokens(t *testing.T) {
	h, _ := newTestHandler(t)
	createEndpoint(t, h, "ep")
	doJSON(t, h, "POST", "/api/endpoints/ep/credentials/import",
		`{"items":[{"access_token":"sk-supersecrettoken-1234","refresh_token":"rt-supersecret-9876"}]}`)

	rec := doJSON(t, h, "GET", "/api/endpoints/ep/credentials", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"sk-supersecrettoken-1234", "rt-supersecret-9876"} {
		if strings.Contains(body, secret) {
			t.Errorf("the credential listing leaked %q in full: %s", secret, body)
		}
	}
	if !strings.Contains(body, "...") {
		t.Errorf("tokens do not look masked: %s", body)
	}
}

func TestUpdateAndDeleteCredential(t *testing.T) {
	h, s := newTestHandler(t)
	createEndpoint(t, h, "ep")

	creds, err := s.GetEndpointCredentials("ep")
	if err != nil || len(creds) != 1 {
		t.Fatalf("seed: %v (%d creds)", err, len(creds))
	}
	id := strconv.FormatInt(creds[0].ID, 10)

	rec := doJSON(t, h, "PATCH", "/api/endpoints/ep/credentials/"+id, `{"remark":"renamed","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	updated, _ := s.GetCredentialByID(creds[0].ID)
	if updated.Remark != "renamed" || updated.Enabled {
		t.Errorf("patch did not apply: remark=%q enabled=%v", updated.Remark, updated.Enabled)
	}

	// An empty access token would put a dead credential into rotation.
	if rec := doJSON(t, h, "PATCH", "/api/endpoints/ep/credentials/"+id, `{"accessToken":"   "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty accessToken = %d, want 400", rec.Code)
	}
	// A malformed timestamp must be rejected, not stored as a zero time.
	if rec := doJSON(t, h, "PATCH", "/api/endpoints/ep/credentials/"+id, `{"expiresAt":"not-a-date"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid expiresAt = %d, want 400", rec.Code)
	}
	// A credential of another endpoint must not be reachable through this one.
	createEndpoint(t, h, "other")
	if rec := doJSON(t, h, "PATCH", "/api/endpoints/other/credentials/"+id, `{"remark":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("cross-endpoint patch = %d, want 404", rec.Code)
	}
	if rec := doJSON(t, h, "PATCH", "/api/endpoints/ep/credentials/999999", `{"remark":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown credential = %d, want 404", rec.Code)
	}
	if rec := doJSON(t, h, "PATCH", "/api/endpoints/ep/credentials/abc", `{"remark":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric id = %d, want 400", rec.Code)
	}

	rec = doJSON(t, h, "DELETE", "/api/endpoints/ep/credentials/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if got := poolTokens(t, s, "ep"); len(got) != 0 {
		t.Errorf("credential survived the delete: %v", got)
	}
}

func TestCredentialStatsEndpoint(t *testing.T) {
	h, _ := newTestHandler(t)
	createEndpoint(t, h, "ep")

	rec := doJSON(t, h, "GET", "/api/endpoints/ep/credentials/stats", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data["total"] != float64(1) || env.Data["active"] != float64(1) {
		t.Errorf("stats = %v, want total=1 active=1", env.Data)
	}
}
