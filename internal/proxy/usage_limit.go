package proxy

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// defaultUsageLimitCooldown is applied when an endpoint reports a usage limit
// but no reset time can be parsed from the error body.
const defaultUsageLimitCooldown = 5 * time.Hour

// usageLimitReason is the cooldown reason recorded for FreeModel usage limits.
const usageLimitReason = "Usage limit reached"

// usageLimitResetRe best-effort matches messages like:
//
//	"Usage limit reached, will reset on tomorrow at 3:37 AM (UTC+8)"
var usageLimitResetRe = regexp.MustCompile(`(?i)reset(?:\s+on)?\s+(today|tomorrow)?\s*at\s+(\d{1,2}):(\d{2})\s*(am|pm)?\s*(?:\(\s*utc\s*([+-]\d{1,2})\s*\))?`)

// endpointRuntimeState is the in-memory, non-persisted state used to drive the
// failover logic and the UI status badges for a single endpoint.
type endpointRuntimeState struct {
	cooldownUntil  time.Time
	cooldownReason string
	lastError      string
	lastErrorAt    time.Time
	hasError       bool
}

// EndpointRuntime is an exported snapshot of an endpoint's runtime state.
type EndpointRuntime struct {
	CooldownUntil  time.Time
	CooldownReason string
	LastError      string
	LastErrorAt    time.Time
	HasError       bool
}

// isUsageLimitError reports whether an upstream response is a FreeModel-style
// usage-limit rejection (HTTP 402 with a "usage limit" message in the body).
func isUsageLimitError(statusCode int, body string) bool {
	if statusCode != http.StatusPaymentRequired {
		return false
	}
	lower := strings.ToLower(body)
	return strings.Contains(lower, "usage limit reached") || strings.Contains(lower, "usage limit")
}

// parseUsageLimitCooldown returns the time until which an endpoint should be
// considered unavailable. It makes a best-effort attempt to read the reset time
// from the body ("will reset ... at H:MM AM (UTC+N)") and otherwise falls back
// to the default 5h cooldown. The result is bounded to (now, now+24h].
func parseUsageLimitCooldown(body string, now time.Time) time.Time {
	fallback := now.Add(defaultUsageLimitCooldown)
	if !strings.Contains(strings.ToLower(body), "will reset") {
		return fallback
	}

	m := usageLimitResetRe.FindStringSubmatch(body)
	if m == nil {
		return fallback
	}

	dayWord := strings.ToLower(m[1])
	hour, _ := strconv.Atoi(m[2])
	minute, _ := strconv.Atoi(m[3])
	ampm := strings.ToLower(m[4])

	if ampm == "pm" && hour < 12 {
		hour += 12
	}
	if ampm == "am" && hour == 12 {
		hour = 0
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return fallback
	}

	loc := time.UTC
	if m[5] != "" {
		if offsetHours, err := strconv.Atoi(m[5]); err == nil && offsetHours >= -14 && offsetHours <= 14 {
			loc = time.FixedZone("UTC"+m[5], offsetHours*3600)
		}
	}

	nowInLoc := now.In(loc)
	reset := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), hour, minute, 0, 0, loc)
	switch {
	case dayWord == "tomorrow":
		reset = reset.AddDate(0, 0, 1)
	case !reset.After(nowInLoc):
		// "today" (or unspecified) but the time already passed -> next day.
		reset = reset.AddDate(0, 0, 1)
	}

	until := reset.UTC()
	if !until.After(now) || until.Sub(now) > 24*time.Hour {
		return fallback
	}
	return until
}

// stateForEndpoint returns the runtime state for an endpoint, creating it on
// first use. Callers must hold stateMu.
func (p *Proxy) stateForEndpoint(name string) *endpointRuntimeState {
	st, ok := p.endpointStates[name]
	if !ok {
		st = &endpointRuntimeState{}
		p.endpointStates[name] = st
	}
	return st
}

// setEndpointCooldown marks an endpoint unavailable until the given time and
// records why (shown in the UI as the cooldown reason).
func (p *Proxy) setEndpointCooldown(name string, until time.Time, reason string) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	st := p.stateForEndpoint(name)
	st.cooldownUntil = until
	st.cooldownReason = reason
}

// recordEndpointError remembers the last upstream error for an endpoint so the
// UI can display it and surface an "error" status.
func (p *Proxy) recordEndpointError(name, msg string) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	st := p.stateForEndpoint(name)
	st.lastError = msg
	st.lastErrorAt = time.Now().UTC()
	st.hasError = true
}

// clearEndpointError clears the error flag after a successful request. The
// cooldown (if any) is left untouched because it is cleared by its own expiry.
func (p *Proxy) clearEndpointError(name string) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if st, ok := p.endpointStates[name]; ok {
		st.hasError = false
	}
}

// endpointInCooldown reports whether the endpoint is currently in cooldown,
// lazily clearing expired cooldowns.
func (p *Proxy) endpointInCooldown(name string) bool {
	now := time.Now().UTC()

	p.stateMu.RLock()
	st, ok := p.endpointStates[name]
	var until time.Time
	if ok {
		until = st.cooldownUntil
	}
	p.stateMu.RUnlock()
	if !ok || until.IsZero() {
		return false
	}
	if now.Before(until) {
		return true
	}

	p.stateMu.Lock()
	if st, ok := p.endpointStates[name]; ok && !st.cooldownUntil.IsZero() && !now.Before(st.cooldownUntil) {
		st.cooldownUntil = time.Time{}
		st.cooldownReason = ""
	}
	p.stateMu.Unlock()
	return false
}

// EndpointRuntimeSnapshot returns a copy of the runtime state for every endpoint
// that has any recorded state. Used by the web UI to render status badges.
func (p *Proxy) EndpointRuntimeSnapshot() map[string]EndpointRuntime {
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()

	out := make(map[string]EndpointRuntime, len(p.endpointStates))
	for name, st := range p.endpointStates {
		out[name] = EndpointRuntime{
			CooldownUntil:  st.cooldownUntil,
			CooldownReason: st.cooldownReason,
			LastError:      st.lastError,
			LastErrorAt:    st.lastErrorAt,
			HasError:       st.hasError,
		}
	}
	return out
}

// ClearAllCooldowns wipes endpoint-level cooldowns and error flags. Token
// pool cooldowns persisted in SQLite are NOT touched here; use
// ClearAllTokenCooldowns for those. Returns number of endpoints affected.
func (p *Proxy) ClearAllCooldowns() int {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	n := 0
	for _, st := range p.endpointStates {
		if !st.cooldownUntil.IsZero() || st.hasError {
			n++
		}
		st.cooldownUntil = time.Time{}
		st.cooldownReason = ""
		st.hasError = false
		st.lastError = ""
	}
	return n
}
