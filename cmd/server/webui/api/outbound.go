package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// validateOutboundURL checks a user-supplied endpoint URL before the admin API
// dereferences it server-side (model-list fetch, connectivity test).
//
// It only enforces the scheme: an LLM endpoint is always http(s), while file://,
// gopher:// and friends are pure SSRF primitives.
func validateOutboundURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("apiUrl is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid apiUrl: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("unsupported scheme %q (only http and https are allowed)", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("apiUrl has no host")
	}
	return u, nil
}

// dialGuard rejects connections to addresses that no real LLM endpoint lives on.
//
// Deliberately permissive about loopback and RFC1918: pointing this proxy at a
// local model server (Ollama, LM Studio, vLLM on the LAN) is a supported setup,
// so blocking private ranges outright would break legitimate configurations.
// What is blocked is the set that only ever shows up in SSRF attempts:
//
//   - link-local, which is how cloud metadata services are reached
//     (169.254.169.254, fe80::/10)
//   - the unspecified address and multicast
//
// The other half of this defense is LocalOnlyMiddleware: a third-party page can
// no longer reach these handlers at all, so it cannot use them to probe the
// user's network in the first place.
//
// The check runs in Dialer.Control, i.e. after DNS resolution on the concrete
// address, so a hostname that resolves to a blocked IP is caught too.
func dialGuard(network, address string, _ syscall.RawConn) error {
	if !strings.HasPrefix(network, "tcp") {
		return fmt.Errorf("refusing non-tcp network %q", network)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("refusing malformed address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refusing unresolvable address %q", host)
	}
	switch {
	case ip.IsUnspecified():
		return fmt.Errorf("refusing to connect to unspecified address %s", ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return fmt.Errorf("refusing to connect to link-local address %s (cloud metadata range)", ip)
	case ip.IsMulticast():
		return fmt.Errorf("refusing to connect to multicast address %s", ip)
	}
	return nil
}

// newOutboundClient builds an http.Client for admin-initiated outbound calls,
// with the dial guard installed and a bounded overall timeout.
func newOutboundClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   dialGuard,
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		},
	}
}
