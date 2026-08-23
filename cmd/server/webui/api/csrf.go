package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// LocalOnlyMiddleware rejects cross-site and rebound requests to the admin API.
//
// The API is unauthenticated by design (single user, loopback bind), but
// loopback does NOT protect against the user's own browser: any page they visit
// can POST to http://127.0.0.1:12710/api/... A JSON body sent as
// Content-Type: text/plain is a CORS "simple request", so no preflight is made
// and the write lands even though the attacker cannot read the response. DNS
// rebinding gives the same primitive with a hostile Host header.
//
// Three cheap checks close that off without introducing a login:
//   - Host must be a loopback address or literal "localhost".
//   - Sec-Fetch-Site, when the browser sends it, must be same-origin/same-site/
//     none. Non-browser clients (curl, scripts) send nothing and are allowed.
//   - Origin, when present, must itself be loopback.
//
// Mutating verbs additionally have to declare JSON, which is what actually
// defeats the simple-request bypass: application/json forces a preflight, and
// the preflight is then rejected by the checks above.
func LocalOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHostHeader(r.Host) {
			WriteError(w, http.StatusForbidden, "Host header is not loopback (possible DNS rebinding)")
			return
		}

		switch r.Header.Get("Sec-Fetch-Site") {
		case "", "same-origin", "same-site", "none":
			// Either a non-browser client or a first-party request.
		default:
			WriteError(w, http.StatusForbidden, "Cross-site requests are not allowed")
			return
		}

		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !isLoopbackHostHeader(u.Host) {
				WriteError(w, http.StatusForbidden, "Cross-origin requests are not allowed")
				return
			}
		}

		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			ct := strings.TrimSpace(r.Header.Get("Content-Type"))
			// An empty Content-Type is accepted: several handlers take no body
			// at all (toggle, reorder, clear-cooldowns) and the UI sends none.
			if ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
				WriteError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// isLoopbackHostHeader reports whether a Host-style "host[:port]" value refers
// to this machine's loopback interface.
func isLoopbackHostHeader(host string) bool {
	h := host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		h = parsed
	}
	h = strings.Trim(strings.TrimSpace(h), "[]")
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
