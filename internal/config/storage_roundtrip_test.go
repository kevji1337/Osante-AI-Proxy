package config

import (
	"testing"
)

// memStorage is an in-memory StorageAdapter for exercising Load/SaveToStorage
// without SQLite.
type memStorage struct {
	endpoints []StorageEndpoint
	kv        map[string]string
}

func newMemStorage() *memStorage {
	return &memStorage{kv: map[string]string{}}
}

func (m *memStorage) GetEndpoints() ([]StorageEndpoint, error) {
	out := make([]StorageEndpoint, len(m.endpoints))
	copy(out, m.endpoints)
	return out, nil
}

func (m *memStorage) SaveEndpoint(ep *StorageEndpoint) error {
	m.endpoints = append(m.endpoints, *ep)
	return nil
}

func (m *memStorage) UpdateEndpoint(ep *StorageEndpoint) error {
	for i := range m.endpoints {
		if m.endpoints[i].Name == ep.Name {
			m.endpoints[i] = *ep
			return nil
		}
	}
	return nil
}

func (m *memStorage) DeleteEndpoint(name string) error {
	for i := range m.endpoints {
		if m.endpoints[i].Name == name {
			m.endpoints = append(m.endpoints[:i], m.endpoints[i+1:]...)
			return nil
		}
	}
	return nil
}

// GetConfig mirrors the SQLite adapter: a missing key is ("", nil), not an error.
func (m *memStorage) GetConfig(key string) (string, error) {
	return m.kv[key], nil
}

func (m *memStorage) SetConfig(key, value string) error {
	m.kv[key] = value
	return nil
}

// TestLoadFromEmptyStorageKeepsDefaults guards the whole family of
// `if v, err := GetConfig(k); err == nil { field = v }` reads. GetConfig returns
// ("", nil) for a key that was never written, so any of them missing an emptiness
// check silently wipes the default — which is exactly what happened to Language.
func TestLoadFromEmptyStorageKeepsDefaults(t *testing.T) {
	cfg, err := LoadFromStorage(newMemStorage())
	if err != nil {
		t.Fatalf("LoadFromStorage: %v", err)
	}

	defaults := DefaultConfig()
	if got := cfg.GetLanguage(); got != defaults.Language {
		t.Errorf("Language = %q, want the default %q", got, defaults.Language)
	}
	if got := cfg.GetPort(); got != DefaultPort {
		t.Errorf("Port = %d, want %d", got, DefaultPort)
	}
	if got := cfg.GetTheme(); got == "" && defaults.Theme != "" {
		t.Errorf("Theme was wiped to empty, default is %q", defaults.Theme)
	}
	if cfg.ModelsCacheTTL == 0 {
		t.Error("ModelsCacheTTL = 0, which disables the cache entirely")
	}
}

func TestConfigRoundTripThroughStorage(t *testing.T) {
	store := newMemStorage()

	original := DefaultConfig()
	original.UpdatePort(14711)
	original.UpdateLogLevel(2)
	original.UpdateLanguage("en")
	original.UpdateEndpoints([]Endpoint{{
		Name:        "primary",
		APIUrl:      "https://api.example.com",
		AuthMode:    AuthModeTokenPool,
		Enabled:     true,
		Transformer: "claude",
		Model:       "claude-3-5-sonnet-20241022",
		Remark:      "notes",
	}})

	if err := original.SaveToStorage(store); err != nil {
		t.Fatalf("SaveToStorage: %v", err)
	}

	loaded, err := LoadFromStorage(store)
	if err != nil {
		t.Fatalf("LoadFromStorage: %v", err)
	}

	if got := loaded.GetPort(); got != 14711 {
		t.Errorf("Port = %d, want 14711", got)
	}
	if got := loaded.GetLogLevel(); got != 2 {
		t.Errorf("LogLevel = %d, want 2", got)
	}
	eps := loaded.GetEndpoints()
	if len(eps) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(eps))
	}
	got := eps[0]
	if got.Name != "primary" || got.APIUrl != "https://api.example.com" ||
		got.Transformer != "claude" || got.Model != "claude-3-5-sonnet-20241022" ||
		got.Remark != "notes" || !got.Enabled {
		t.Errorf("endpoint did not survive the round trip: %+v", got)
	}
}

func TestPortLockRejectsUpdates(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UpdatePort(14712)
	cfg.LockPort()

	cfg.UpdatePort(14713)
	if got := cfg.GetPort(); got != 14712 {
		t.Errorf("Port = %d after UpdatePort on a locked config, want 14712", got)
	}
	if !cfg.IsPortLocked() {
		t.Error("IsPortLocked() = false after LockPort")
	}
}

// TestGettersReturnCopies guards the fix for the shared-pointer getters: they used
// to hand out the live struct, so callers mutated config state with no lock while
// UpdateX wrote to the same fields.
func TestGettersReturnCopies(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UpdateProxy(&ProxyConfig{URL: "http://127.0.0.1:1080"})

	got := cfg.GetProxy()
	if got == nil {
		t.Fatal("GetProxy() = nil after UpdateProxy")
	}
	got.URL = "http://evil.example"

	if after := cfg.GetProxy(); after.URL != "http://127.0.0.1:1080" {
		t.Errorf("mutating the result of GetProxy() changed the config: %q", after.URL)
	}

	terminal := cfg.GetTerminal()
	if terminal == nil {
		t.Fatal("GetTerminal() = nil")
	}
	terminal.ProjectDirs = append(terminal.ProjectDirs, "/tmp/injected")
	terminal.SelectedTerminal = "mutated"
	if after := cfg.GetTerminal(); after.SelectedTerminal == "mutated" {
		t.Error("mutating the result of GetTerminal() changed the config")
	}
}

func TestGetProxyNilWhenUnset(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.GetProxy(); got != nil {
		t.Errorf("GetProxy() = %+v on a fresh config, want nil", got)
	}
}
