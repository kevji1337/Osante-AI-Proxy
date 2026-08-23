package storage

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestGetUsableEndpointCredentialRotatesUnderConcurrency guards the atomic
// claim: selection used to only read the pool, so parallel requests all got the
// same token and one token's quota was burned while the rest sat idle.
func TestGetUsableEndpointCredentialRotatesUnderConcurrency(t *testing.T) {
	s, err := NewSQLiteStorage(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	const tokens = 4
	for i := 0; i < tokens; i++ {
		cred := &EndpointCredential{
			EndpointName: "ep",
			ProviderType: "api_key",
			AccessToken:  "tok",
			Status:       "active",
			Enabled:      true,
		}
		if err := s.SaveEndpointCredential(cred); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	picked := make([]int64, tokens)
	for i := 0; i < tokens; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cred, err := s.GetUsableEndpointCredential("ep", time.Now().UTC().Add(time.Duration(i)*time.Millisecond))
			if err != nil || cred == nil {
				t.Errorf("select %d: cred=%v err=%v", i, cred, err)
				return
			}
			picked[i] = cred.ID
		}(i)
	}
	wg.Wait()

	seen := map[int64]int{}
	for _, id := range picked {
		seen[id]++
	}
	if len(seen) < 2 {
		t.Fatalf("no rotation: %d concurrent selections all returned the same token (%v)", tokens, picked)
	}
}
