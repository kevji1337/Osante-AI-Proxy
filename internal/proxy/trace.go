package proxy

import (
	"sync"
	"sync/atomic"
	"time"
)

// TracePhase is a single timing breakpoint inside a request's lifecycle.
type TracePhase string

const (
	PhaseReceived       TracePhase = "received"        // request arrived at the proxy
	PhaseTransformed    TracePhase = "transformed"     // request body transformed for upstream
	PhaseUpstreamSent   TracePhase = "upstream_sent"   // http.Client.Do returned headers
	PhaseFirstByte      TracePhase = "first_byte"      // first byte of upstream body read
	PhaseLastByte       TracePhase = "last_byte"       // upstream stream ended
	PhaseClientSent     TracePhase = "client_sent"     // last byte forwarded to client
	PhaseDone           TracePhase = "done"            // request finished (success or terminal failure)
)

// TraceMark is one (phase, monotonic ns since start) pair.
type TraceMark struct {
	Phase   TracePhase `json:"phase"`
	OffsetMs int64     `json:"offset_ms"`
}

// TraceRecord is a single request's timing slice. Field names are JSON
// snake_case for fetch from the admin UI.
type TraceRecord struct {
	ID             int64       `json:"id"`
	StartUnixMs    int64       `json:"start_unix_ms"`
	Endpoint       string      `json:"endpoint,omitempty"`
	Model          string      `json:"model,omitempty"`
	ClientFormat   string      `json:"client_format,omitempty"`
	Transformer    string      `json:"transformer,omitempty"`
	Method         string      `json:"method,omitempty"`
	Path           string      `json:"path,omitempty"`
	StatusCode     int         `json:"status_code"`
	BytesIn        int         `json:"bytes_in"`
	BytesOut       int         `json:"bytes_out"`
	InputTokens    int         `json:"input_tokens,omitempty"`
	OutputTokens   int         `json:"output_tokens,omitempty"`
	Streaming      bool        `json:"streaming"`
	PinnedEndpoint bool        `json:"pinned_endpoint,omitempty"`
	Err            string      `json:"err,omitempty"`
	Marks          []TraceMark `json:"marks"`
	TotalMs        int64       `json:"total_ms"`
}

// TraceRing is a tiny lock-protected bounded log of recent request traces.
// It exists purely to back the "Live request inspector" admin view —
// nothing else in the proxy reads from it, so failures here must NEVER block
// real request handling.
type TraceRing struct {
	mu       sync.RWMutex
	cap      int
	records  []TraceRecord
	nextID   atomic.Int64
}

// NewTraceRing constructs a ring with the given capacity (records older than
// that are dropped). cap < 1 defaults to 64.
func NewTraceRing(cap int) *TraceRing {
	if cap < 1 {
		cap = 64
	}
	return &TraceRing{cap: cap, records: make([]TraceRecord, 0, cap)}
}

// Begin allocates a fresh in-progress trace and returns its handle. The
// caller flows the handle through the request lifecycle, calling Mark at each
// phase and Finalize at the end.
func (r *TraceRing) Begin() *TraceHandle {
	id := r.nextID.Add(1)
	return &TraceHandle{
		ring:  r,
		start: time.Now(),
		rec: TraceRecord{
			ID:          id,
			StartUnixMs: time.Now().UnixMilli(),
			Marks:       make([]TraceMark, 0, 6),
		},
	}
}

// Snapshot returns a copy of the most recent records, newest first.
func (r *TraceRing) Snapshot(limit int) []TraceRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := len(r.records)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]TraceRecord, 0, limit)
	for i := n - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, r.records[i])
	}
	return out
}

func (r *TraceRing) push(rec TraceRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) >= r.cap {
		// Drop oldest; copy in place to avoid growing the underlying array.
		copy(r.records, r.records[1:])
		r.records = r.records[:len(r.records)-1]
	}
	r.records = append(r.records, rec)
}

// TraceHandle is the per-request mutable trace handed to proxy code. All
// mutation is single-threaded inside one request, so no locking is needed
// until Finalize hands the snapshot off to the ring.
type TraceHandle struct {
	ring  *TraceRing
	start time.Time
	rec   TraceRecord
	done  bool
}

// Mark stamps a phase against the elapsed wallclock since Begin. Safe to call
// on a nil handle (handy when tracing was disabled).
func (h *TraceHandle) Mark(phase TracePhase) {
	if h == nil || h.done {
		return
	}
	h.rec.Marks = append(h.rec.Marks, TraceMark{
		Phase:    phase,
		OffsetMs: time.Since(h.start).Milliseconds(),
	})
}

// SetMeta fills in the descriptive fields once we know them. Each setter is
// nil-safe so missing meta doesn't break the call site.
func (h *TraceHandle) SetMeta(method, path, clientFormat string) {
	if h == nil {
		return
	}
	h.rec.Method = method
	h.rec.Path = path
	h.rec.ClientFormat = clientFormat
}

func (h *TraceHandle) SetEndpoint(name, transformer, model string, pinned bool) {
	if h == nil {
		return
	}
	h.rec.Endpoint = name
	h.rec.Transformer = transformer
	h.rec.Model = model
	h.rec.PinnedEndpoint = pinned
}

func (h *TraceHandle) SetStreaming(s bool) {
	if h == nil {
		return
	}
	h.rec.Streaming = s
}

func (h *TraceHandle) SetBytes(in, out int) {
	if h == nil {
		return
	}
	h.rec.BytesIn = in
	h.rec.BytesOut = out
}

func (h *TraceHandle) SetTokens(in, out int) {
	if h == nil {
		return
	}
	h.rec.InputTokens = in
	h.rec.OutputTokens = out
}

func (h *TraceHandle) SetStatus(code int) {
	if h == nil {
		return
	}
	h.rec.StatusCode = code
}

func (h *TraceHandle) SetError(err string) {
	if h == nil {
		return
	}
	h.rec.Err = err
}

// Finalize stamps total elapsed and ships the record to the ring. Subsequent
// Mark/Set calls are no-ops.
func (h *TraceHandle) Finalize() {
	if h == nil || h.done {
		return
	}
	h.done = true
	h.rec.TotalMs = time.Since(h.start).Milliseconds()
	h.Mark(PhaseDone)
	if h.ring != nil {
		h.ring.push(h.rec)
	}
}
