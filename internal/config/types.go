package config

import "sync"

// Endpoint represents a single API endpoint configuration
type Endpoint struct {
	Name        string `json:"name"`
	APIUrl      string `json:"apiUrl"`
	APIKey      string `json:"apiKey"`
	AuthMode    string `json:"authMode,omitempty"`
	Enabled     bool   `json:"enabled"`
	Transformer string `json:"transformer,omitempty"` // Transformer type: claude, openai, gemini, deepseek
	Model       string `json:"model,omitempty"`       // Target model name for non-Claude APIs
	Remark      string `json:"remark,omitempty"`      // Optional remark for the endpoint
}

// WebDAVConfig represents WebDAV synchronization configuration
type WebDAVConfig struct {
	URL        string `json:"url"`        // WebDAV server URL
	Username   string `json:"username"`   // Username
	Password   string `json:"password"`   // Password
	ConfigPath string `json:"configPath"` // Config backup path (default /Osante Proxy/config)
	StatsPath  string `json:"statsPath"`  // Stats backup path (default /Osante Proxy/stats)
}

// LocalBackupConfig represents local backup configuration
type LocalBackupConfig struct {
	Dir string `json:"dir"` // Local directory to store backups
}

// S3BackupConfig represents S3-compatible backup configuration
type S3BackupConfig struct {
	Endpoint       string `json:"endpoint"`
	Region         string `json:"region,omitempty"`
	Bucket         string `json:"bucket"`
	Prefix         string `json:"prefix,omitempty"`
	AccessKey      string `json:"accessKey"`
	SecretKey      string `json:"secretKey"`
	SessionToken   string `json:"sessionToken,omitempty"`
	UseSSL         bool   `json:"useSSL"`
	ForcePathStyle bool   `json:"forcePathStyle"`
}

// BackupConfig represents backup/sync configuration across providers
type BackupConfig struct {
	Provider string             `json:"provider"` // webdav | local | s3
	Local    *LocalBackupConfig `json:"local,omitempty"`
	S3       *S3BackupConfig    `json:"s3,omitempty"`
}

// UpdateConfig represents update configuration
type UpdateConfig struct {
	AutoCheck      bool   `json:"autoCheck"`      // Auto check for updates
	CheckInterval  int    `json:"checkInterval"`  // Check interval in hours
	LastCheckTime  string `json:"lastCheckTime"`  // Last check time (RFC3339)
	SkippedVersion string `json:"skippedVersion"` // Skipped version
}

// TerminalConfig represents terminal launcher configuration
type TerminalConfig struct {
	SelectedTerminal string   `json:"selectedTerminal"` // Selected terminal ID
	ProjectDirs      []string `json:"projectDirs"`      // Project directories
	ClaudeCommand    string   `json:"claudeCommand"`    // Custom launcher command, defaults to "claude"
}

// ProxyConfig represents HTTP proxy configuration
type ProxyConfig struct {
	URL string `json:"url"` // Proxy URL, e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080
}

// Config represents the application configuration
type Config struct {
	Port                      int             `json:"port"`
	PortLocked                bool            `json:"-"` // CLI forced port, cannot be changed via API
	Endpoints                 []Endpoint      `json:"endpoints"`
	LogLevel                  int             `json:"logLevel"`                            // 0=DEBUG, 1=INFO, 2=WARN, 3=ERROR
	Language                  string          `json:"language"`                            // UI language: en
	Theme                     string          `json:"theme"`                               // UI theme: light, dark
	ThemeAuto                 bool            `json:"themeAuto"`                           // Auto switch theme based on time
	AutoLightTheme            string          `json:"autoLightTheme,omitempty"`            // Theme to use in daytime when auto mode is on
	AutoDarkTheme             string          `json:"autoDarkTheme,omitempty"`             // Theme to use in nighttime when auto mode is on
	WindowWidth               int             `json:"windowWidth"`                         // Window width in pixels
	WindowHeight              int             `json:"windowHeight"`                        // Window height in pixels
	CloseWindowBehavior       string          `json:"closeWindowBehavior,omitempty"`       // "quit", "minimize", "ask"
	ClaudeNotificationEnabled bool            `json:"claudeNotificationEnabled"`           // Enable Claude Code task completion notification
	ClaudeNotificationType    string          `json:"claudeNotificationType"`              // Notification type: toast, dialog, disabled
	ModelsCacheTTL            int             `json:"modelsCacheTTL,omitempty"`            // /v1/models cache TTL in minutes, default 30
	ModelsCacheRefreshEnabled bool            `json:"modelsCacheRefreshEnabled,omitempty"` // Enable ?refresh=true parameter, default false
	WebDAV                    *WebDAVConfig   `json:"webdav,omitempty"`                    // WebDAV synchronization config
	Backup                    *BackupConfig   `json:"backup,omitempty"`                    // Backup/sync configuration
	Update                    *UpdateConfig   `json:"update,omitempty"`                    // Update configuration
	Terminal                  *TerminalConfig `json:"terminal,omitempty"`                  // Terminal launcher config
	Proxy                     *ProxyConfig    `json:"proxy,omitempty"`                     // HTTP proxy config
	CodexProxy                *ProxyConfig    `json:"codexProxy,omitempty"`                // Codex dedicated proxy config
	mu                        sync.RWMutex
}
