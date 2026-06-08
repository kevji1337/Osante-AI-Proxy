package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestStorage creates a fresh on-disk SQLite database in t.TempDir() with
// the proxy schema initialized. Each test gets an isolated DB so they can
// safely run in parallel.
func newTestStorage(t *testing.T) *SQLiteStorage {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "osante-test.db")
	s, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage failed: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		_ = os.Remove(path)
	})
	return s
}

// TestCredentialUsageLimitRoundTrip is the regression test for the headline
// failover flow: two pool tokens, the first hits a usage limit, the next
// selection must return the second token, and after the cooldown expires the
// first token is usable again.
func TestCredentialUsageLimitRoundTrip(t *testing.T) {
	s := newTestStorage(t)
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	endpointName := "Freemodel"

	for i, token := range []string{"token-A", "token-B"} {
		cred := &EndpointCredential{
			EndpointName: endpointName,
			ProviderType: "api_key",
			AccountID:    "acct-" + token,
			AccessToken:  token,
			Status:       credentialStatusActive,
			Enabled:      true,
			Remark:       "test",
		}
		if err := s.SaveEndpointCredential(cred); err != nil {
			t.Fatalf("SaveEndpointCredential[%d] failed: %v", i, err)
		}
	}

	// 1. First selection — should hand out one of the two active tokens.
	first, err := s.GetUsableEndpointCredential(endpointName, now)
	if err != nil || first == nil {
		t.Fatalf("first GetUsableEndpointCredential: cred=%v err=%v", first, err)
	}
	firstID := first.ID
	if firstID == 0 {
		t.Fatalf("first selection returned credential without ID: %+v", first)
	}

	// 2. Mark the selected credential as usage-limited for 1h.
	cooldownUntil := now.Add(time.Hour)
	if err := s.MarkCredentialUsageLimit(firstID, "Usage limit reached", cooldownUntil, now); err != nil {
		t.Fatalf("MarkCredentialUsageLimit failed: %v", err)
	}

	// 3. Next selection — must NOT return the cooled-down token. It must
	//    return the *other* token from the same endpoint pool.
	second, err := s.GetUsableEndpointCredential(endpointName, now.Add(time.Minute))
	if err != nil || second == nil {
		t.Fatalf("second GetUsableEndpointCredential: cred=%v err=%v", second, err)
	}
	if second.ID == firstID {
		t.Fatalf("after cooldown of token id=%d, second selection returned the SAME token", firstID)
	}

	// 4. While both should still be usable in the abstract, after also
	//    cooling down the second, selection must return nil — pool exhausted.
	if err := s.MarkCredentialUsageLimit(second.ID, "Usage limit reached", cooldownUntil, now); err != nil {
		t.Fatalf("MarkCredentialUsageLimit second failed: %v", err)
	}
	exhausted, err := s.GetUsableEndpointCredential(endpointName, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("exhausted GetUsableEndpointCredential errored: %v", err)
	}
	if exhausted != nil {
		t.Fatalf("pool should be exhausted, got cred id=%d", exhausted.ID)
	}

	// 5. After cooldown expires (now > cooldownUntil), tokens are usable again.
	revived, err := s.GetUsableEndpointCredential(endpointName, cooldownUntil.Add(time.Minute))
	if err != nil || revived == nil {
		t.Fatalf("after cooldown expiry, expected revived token, got cred=%v err=%v", revived, err)
	}
}

// TestCredentialAuthFailureInvalidatesToken: a 401/403 marks the credential
// invalid and removes it from the usable pool, mirroring the production
// behavior in handleFinalStatus.
func TestCredentialAuthFailureInvalidatesToken(t *testing.T) {
	s := newTestStorage(t)
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	endpointName := "Test"
	cred := &EndpointCredential{
		EndpointName: endpointName,
		ProviderType: "api_key",
		AccountID:    "acct",
		AccessToken:  "bad-token",
		Status:       credentialStatusActive,
		Enabled:      true,
	}
	if err := s.SaveEndpointCredential(cred); err != nil {
		t.Fatalf("SaveEndpointCredential: %v", err)
	}

	first, err := s.GetUsableEndpointCredential(endpointName, now)
	if err != nil || first == nil {
		t.Fatalf("initial selection: %v / %+v", err, first)
	}

	if err := s.MarkCredentialFailure(first.ID, 401, "Unauthorized", now); err != nil {
		t.Fatalf("MarkCredentialFailure: %v", err)
	}

	again, err := s.GetUsableEndpointCredential(endpointName, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("post-401 selection errored: %v", err)
	}
	if again != nil {
		t.Fatalf("post-401 selection should be nil, got cred id=%d status=%s", again.ID, again.Status)
	}
}

// TestCredentialSuccessClearsCooldown: after a successful request the
// credential is restored to active even if it was previously in cooldown.
func TestCredentialSuccessClearsCooldown(t *testing.T) {
	s := newTestStorage(t)
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	endpointName := "Test"
	cred := &EndpointCredential{
		EndpointName: endpointName,
		ProviderType: "api_key",
		AccountID:    "acct",
		AccessToken:  "recovering-token",
		Status:       credentialStatusActive,
		Enabled:      true,
	}
	if err := s.SaveEndpointCredential(cred); err != nil {
		t.Fatalf("SaveEndpointCredential: %v", err)
	}

	first, err := s.GetUsableEndpointCredential(endpointName, now)
	if err != nil || first == nil {
		t.Fatalf("initial selection: %v / %+v", err, first)
	}

	if err := s.MarkCredentialUsageLimit(first.ID, "Usage limit reached", now.Add(time.Hour), now); err != nil {
		t.Fatalf("MarkCredentialUsageLimit: %v", err)
	}
	if err := s.MarkCredentialSuccess(first.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkCredentialSuccess: %v", err)
	}

	recovered, err := s.GetUsableEndpointCredential(endpointName, now.Add(3*time.Minute))
	if err != nil || recovered == nil {
		t.Fatalf("after success, expected usable cred, got cred=%v err=%v", recovered, err)
	}
	if recovered.ID != first.ID {
		t.Fatalf("expected the same recovered cred id=%d, got id=%d", first.ID, recovered.ID)
	}
}
