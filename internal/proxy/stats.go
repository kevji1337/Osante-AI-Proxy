package proxy

import (
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
)

// DailyStats represents statistics for a single day
type DailyStats struct {
	Date         string `json:"date"` // Format: "2006-01-02"
	Requests     int    `json:"requests"`
	Errors       int    `json:"errors"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
}

// EndpointStats represents statistics for a single endpoint
type EndpointStats struct {
	Requests     int                    `json:"requests"`
	Errors       int                    `json:"errors"`
	InputTokens  int                    `json:"inputTokens"`
	OutputTokens int                    `json:"outputTokens"`
	LastUsed     time.Time              `json:"lastUsed"`
	DailyHistory map[string]*DailyStats `json:"dailyHistory"`
}

// StatsSnapshot is the serialisable view of current statistics served by /stats
// and by the web UI's SSE stats event. *Stats itself has no exported fields, so
// encoding it yielded an empty object.
type StatsSnapshot struct {
	TotalRequests int                       `json:"totalRequests"`
	Endpoints     map[string]*EndpointStats `json:"endpoints"`
}

// StatRecord is one increment to a day's counters for an endpoint.
type StatRecord struct {
	EndpointName string
	Date         string
	Requests     int
	Errors       int
	InputTokens  int
	OutputTokens int
	DeviceID     string
}

// StatsData is an endpoint's aggregate counters over some period.
type StatsData struct {
	Requests     int
	Errors       int
	InputTokens  int64
	OutputTokens int64
}

// StatsStorage is the persistence this package needs for statistics.
//
// It used to be declared with interface{} parameters, which forced every call
// through a chain of type assertions ending in reflect.Value.FieldByName — seven
// reflective field lookups per recorded request, twice per proxied request. The
// concrete types below make the adapter a plain struct conversion.
type StatsStorage interface {
	RecordDailyStat(stat StatRecord) error
	GetTotalStats() (int, map[string]StatsData, error)
}

// Stats records per-endpoint request statistics through StatsStorage.
//
// SQLite is the single source of truth; there is no in-memory aggregation. The
// previous version also carried a debounced save/load/reset path over an
// in-memory map plus a four-period event callback, none of which was reachable.
type Stats struct {
	storage  StatsStorage
	deviceID string
}

// NewStats creates a new Stats instance
func NewStats(storage StatsStorage, deviceID string) *Stats {
	return &Stats{
		storage:  storage,
		deviceID: deviceID,
	}
}

// RecordError records one failed request for an endpoint.
func (s *Stats) RecordError(endpointName string) {
	stat := StatRecord{
		EndpointName: endpointName,
		Date:         time.Now().Format("2006-01-02"),
		Errors:       1,
		DeviceID:     s.deviceID,
	}
	if err := s.storage.RecordDailyStat(stat); err != nil {
		logger.Error("Failed to record error: %v", err)
	}
}

// RecordSuccess records one successful request together with its token usage.
//
// Deliberately a single call: recording the request and its tokens separately
// meant two serialized SQLite UPSERTs against the same daily_stats row for one
// proxied request.
func (s *Stats) RecordSuccess(endpointName string, inputTokens, outputTokens int) {
	stat := StatRecord{
		EndpointName: endpointName,
		Date:         time.Now().Format("2006-01-02"),
		Requests:     1,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		DeviceID:     s.deviceID,
	}
	if err := s.storage.RecordDailyStat(stat); err != nil {
		logger.Error("Failed to record request: %v", err)
	}
}

// GetStats returns the total request count and per-endpoint totals.
func (s *Stats) GetStats() (int, map[string]*EndpointStats) {
	totalRequests, perEndpoint, err := s.storage.GetTotalStats()
	if err != nil {
		logger.Error("Failed to get stats: %v", err)
		return 0, make(map[string]*EndpointStats)
	}

	result := make(map[string]*EndpointStats, len(perEndpoint))
	for name, data := range perEndpoint {
		result[name] = &EndpointStats{
			Requests:     data.Requests,
			Errors:       data.Errors,
			InputTokens:  int(data.InputTokens),
			OutputTokens: int(data.OutputTokens),
			LastUsed:     time.Now(),
			DailyHistory: make(map[string]*DailyStats),
		}
	}
	return totalRequests, result
}
