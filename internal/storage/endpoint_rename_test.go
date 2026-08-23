package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateEndpointRenameCascades(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSQLiteStorage(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	ep := &Endpoint{Name: "old", APIUrl: "https://a.example", APIKey: "k", AuthMode: "token_pool", Enabled: true, Transformer: "claude"}
	if err := s.SaveEndpoint(ep); err != nil {
		t.Fatalf("save: %v", err)
	}
	if ep.ID == 0 {
		t.Fatal("SaveEndpoint did not set ID")
	}
	cred := &EndpointCredential{EndpointName: "old", ProviderType: "api_key", AccessToken: "tok", Status: "active", Enabled: true}
	if err := s.SaveEndpointCredential(cred); err != nil {
		t.Fatalf("cred: %v", err)
	}
	if err := s.RecordDailyStat(&DailyStat{EndpointName: "old", Date: time.Now().Format("2006-01-02"), Requests: 3, DeviceID: "dev"}); err != nil {
		t.Fatalf("stat: %v", err)
	}

	ep.Name = "new"
	ep.APIUrl = "https://b.example"
	if err := s.UpdateEndpoint(ep); err != nil {
		t.Fatalf("rename: %v", err)
	}

	eps, err := s.GetEndpoints()
	if err != nil || len(eps) != 1 {
		t.Fatalf("get: %v %d", err, len(eps))
	}
	if eps[0].Name != "new" || eps[0].APIUrl != "https://b.example" {
		t.Fatalf("rename lost: name=%q url=%q", eps[0].Name, eps[0].APIUrl)
	}
	creds, err := s.GetEndpointCredentials("new")
	if err != nil || len(creds) != 1 {
		t.Fatalf("credentials not moved: %v %d", err, len(creds))
	}
	st, err := s.GetEndpointTotalStats("new")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Requests != 3 {
		t.Fatalf("stats not moved: %+v", st)
	}
	// no-stats endpoint must not error (COALESCE fix)
	if _, err := s.GetEndpointTotalStats("does-not-exist"); err != nil {
		t.Fatalf("empty stats should not error: %v", err)
	}
}
