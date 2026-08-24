package storage

import "time"

type Endpoint struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	APIUrl      string    `json:"apiUrl"`
	APIKey      string    `json:"apiKey"`
	AuthMode    string    `json:"authMode"`
	Enabled     bool      `json:"enabled"`
	Transformer string    `json:"transformer"`
	Model       string    `json:"model"`
	Remark      string    `json:"remark"`
	SortOrder   int       `json:"sortOrder"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type EndpointCredential struct {
	ID            int64                 `json:"id"`
	EndpointName  string                `json:"endpointName"`
	ProviderType  string                `json:"providerType"`
	AccountID     string                `json:"accountId,omitempty"`
	Email         string                `json:"email,omitempty"`
	AccessToken   string                `json:"accessToken,omitempty"`
	RefreshToken  string                `json:"refreshToken,omitempty"`
	IDToken       string                `json:"idToken,omitempty"`
	LastRefresh   *time.Time            `json:"lastRefresh,omitempty"`
	ExpiresAt     *time.Time            `json:"expiresAt,omitempty"`
	Status        string                `json:"status"`
	Enabled       bool                  `json:"enabled"`
	FailureCount  int                   `json:"failureCount"`
	CooldownUntil *time.Time            `json:"cooldownUntil,omitempty"`
	LastCheckedAt *time.Time            `json:"lastCheckedAt,omitempty"`
	LastUsedAt    *time.Time            `json:"lastUsedAt,omitempty"`
	LastError     string                `json:"lastError,omitempty"`
	Remark        string                `json:"remark,omitempty"`
	RateLimits    *CredentialRateLimits `json:"rateLimits,omitempty"`
	Usage         *CredentialUsage      `json:"usage,omitempty"`
	CreatedAt     time.Time             `json:"createdAt"`
	UpdatedAt     time.Time             `json:"updatedAt"`
}

type CodexRateLimitWindow struct {
	UsedPercent   float64 `json:"usedPercent"`
	WindowMinutes *int64  `json:"windowMinutes,omitempty"`
	ResetsAt      *int64  `json:"resetsAt,omitempty"`
}

type CodexCreditsSnapshot struct {
	HasCredits bool   `json:"hasCredits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
}

type CodexRateLimitSnapshot struct {
	LimitID   string                `json:"limitId,omitempty"`
	LimitName string                `json:"limitName,omitempty"`
	Primary   *CodexRateLimitWindow `json:"primary,omitempty"`
	Secondary *CodexRateLimitWindow `json:"secondary,omitempty"`
	Credits   *CodexCreditsSnapshot `json:"credits,omitempty"`
	PlanType  string                `json:"planType,omitempty"`
}

type CodexRateLimitsData struct {
	Snapshot  *CodexRateLimitSnapshot           `json:"snapshot,omitempty"`
	ByLimitID map[string]CodexRateLimitSnapshot `json:"byLimitId,omitempty"`
	Source    string                            `json:"source,omitempty"`
}

type CredentialRateLimits struct {
	CredentialID int64                `json:"credentialId"`
	Status       string               `json:"status"`
	Error        string               `json:"error,omitempty"`
	UpdatedAt    *time.Time           `json:"updatedAt,omitempty"`
	Data         *CodexRateLimitsData `json:"data,omitempty"`
}

type CredentialUsage struct {
	CredentialID int64      `json:"credentialId"`
	Requests     int        `json:"requests"`
	Errors       int        `json:"errors"`
	InputTokens  int        `json:"inputTokens"`
	OutputTokens int        `json:"outputTokens"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

type TokenPoolStats struct {
	Total       int `json:"total"`
	Active      int `json:"active"`
	Expiring    int `json:"expiring"`
	Expired     int `json:"expired"`
	Invalid     int `json:"invalid"`
	Cooldown    int `json:"cooldown"`
	Disabled    int `json:"disabled"`
	NeedRefresh int `json:"needRefresh"`
}

type DailyStat struct {
	ID           int64
	EndpointName string
	Date         string
	Requests     int
	Errors       int
	InputTokens  int
	OutputTokens int
	DeviceID     string
	CreatedAt    time.Time
}

type EndpointStats struct {
	Requests     int
	Errors       int
	InputTokens  int64
	OutputTokens int64
}
