package proxy

// In-flight request deduplication for GitLab Duo.
//
// Claude Code occasionally retries the same /v1/messages request in parallel
// (we have seen pairs of identical workflows created within ~100 ms). Each one
// burns a GitLab Duo credit. We deduplicate by hashing endpoint + token +
// conversation body: if a second identical request arrives while the first is
// still in flight, it waits for the first one's result instead of starting its
// own workflow.

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// duoInflightResult is the shared outcome of an in-flight Duo request.
type duoInflightResult struct {
	answer string
	err    error
}

// duoInflight is a single in-flight request other goroutines can wait on.
type duoInflight struct {
	done   chan struct{}
	result duoInflightResult
}

// duoInflightRegistry tracks in-flight Duo requests by content key.
type duoInflightRegistry struct {
	mu    sync.Mutex
	calls map[string]*duoInflight
}

var globalDuoInflight = &duoInflightRegistry{
	calls: make(map[string]*duoInflight),
}

// computeInflightKey returns a stable key for deduplicating identical requests.
// Different tokens/endpoints/goals never collide. A 256-bit SHA hash is plenty
// for a process-local map.
func computeInflightKey(endpointName, apiKey, goal string) string {
	h := sha256.New()
	h.Write([]byte(endpointName))
	h.Write([]byte{0})
	h.Write([]byte(apiKey))
	h.Write([]byte{0})
	h.Write([]byte(goal))
	return hex.EncodeToString(h.Sum(nil))
}

// acquire returns (inflight, isLeader). When isLeader is true the caller must
// perform the actual work and then call publish() with the result. When false
// the caller waits on inflight.done and reads inflight.result.
func (r *duoInflightRegistry) acquire(key string) (*duoInflight, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.calls[key]; ok {
		return existing, false
	}
	infl := &duoInflight{done: make(chan struct{})}
	r.calls[key] = infl
	return infl, true
}

// publish stores the result, removes the entry, and wakes waiters.
func (r *duoInflightRegistry) publish(key string, infl *duoInflight, result duoInflightResult) {
	r.mu.Lock()
	delete(r.calls, key)
	r.mu.Unlock()
	infl.result = result
	close(infl.done)
}
