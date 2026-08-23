package storage

import (
	"path/filepath"
	"sync"
	"testing"
)

// TestPragmasApplyToEveryPooledConnection guards the DSN-based pragma setup:
// running "PRAGMA busy_timeout" through db.Exec only configured one pooled
// connection, so concurrent writers got SQLITE_BUSY instead of waiting.
func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	s, err := NewSQLiteStorage(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	const conns = 8
	var wg sync.WaitGroup
	results := make([]struct {
		busy int
		sync int
		err  error
	}, conns)

	// Hold every connection at once so the pool is forced to open all of them.
	start := make(chan struct{})
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			row := s.db.QueryRow("PRAGMA busy_timeout")
			if err := row.Scan(&results[i].busy); err != nil {
				results[i].err = err
				return
			}
			row = s.db.QueryRow("PRAGMA synchronous")
			results[i].err = row.Scan(&results[i].sync)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("conn %d: %v", i, r.err)
		}
		if r.busy != 5000 {
			t.Errorf("conn %d: busy_timeout = %d, want 5000", i, r.busy)
		}
		if r.sync != 1 {
			t.Errorf("conn %d: synchronous = %d, want 1 (NORMAL)", i, r.sync)
		}
	}
}
