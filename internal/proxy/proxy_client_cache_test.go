package proxy

import (
	"net/http"
	"testing"
	"time"
)

// TestProxyClientCacheReuseAndBound covers both halves of the transport cache:
// the same proxy URL must hand back the same client (a fresh Transport per
// request means no connection reuse at all), and the cache must not grow without
// limit, since its key is a user-editable config value.
func TestProxyClientCacheReuseAndBound(t *testing.T) {
	proxyClientsMu.Lock()
	evictProxyClientsLocked()
	proxyClientsMu.Unlock()
	t.Cleanup(func() {
		proxyClientsMu.Lock()
		evictProxyClientsLocked()
		proxyClientsMu.Unlock()
	})

	first, err := proxyClientFor("http://127.0.0.1:1080", 0)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := proxyClientFor("http://127.0.0.1:1080", 0)
	if err != nil {
		t.Fatalf("again: %v", err)
	}
	if first != again {
		t.Error("the same proxy URL produced two clients — connections cannot be reused")
	}
	if _, ok := first.Transport.(*http.Transport); !ok {
		t.Errorf("client transport is %T, want *http.Transport", first.Transport)
	}

	// Fill past the cap; the cache must clear rather than keep every entry.
	for i := 0; i < maxCachedProxyClients+3; i++ {
		if _, err := proxyClientFor("http://127.0.0.1:"+itoa(2000+i), 0); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	proxyClientsMu.Lock()
	size := len(proxyClients)
	proxyClientsMu.Unlock()
	if size > maxCachedProxyClients {
		t.Errorf("cache holds %d entries, cap is %d", size, maxCachedProxyClients)
	}
}

func TestProxyClientForRejectsBadURL(t *testing.T) {
	if _, err := proxyClientFor("ftp://example.com", time.Second); err == nil {
		t.Fatal("an unsupported proxy scheme was accepted")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
