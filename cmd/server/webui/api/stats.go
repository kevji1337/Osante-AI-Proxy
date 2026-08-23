package api

import (
	"net/http"
	"time"

	"github.com/kevji1337/Osante-AI-Proxy/internal/logger"
)

// handleStatsSummary returns overall statistics
func (h *Handler) handleStatsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	totalRequests, endpointStats := h.proxy.GetStats().GetStats()

	// Calculate totals
	totalErrors := 0
	var totalInputTokens int64 = 0
	var totalOutputTokens int64 = 0

	for _, stats := range endpointStats {
		totalErrors += stats.Errors
		totalInputTokens += int64(stats.InputTokens)
		totalOutputTokens += int64(stats.OutputTokens)
	}

	WriteSuccess(w, map[string]interface{}{
		"TotalRequests":     totalRequests,
		"TotalErrors":       totalErrors,
		"TotalInputTokens":  totalInputTokens,
		"TotalOutputTokens": totalOutputTokens,
		"Endpoints":         endpointStats,
	})
}

// handleStatsDaily returns today's statistics
func (h *Handler) handleStatsDaily(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	today := time.Now().Format("2006-01-02")
	stats, err := h.getStatsForPeriod(today, today)
	if err != nil {
		logger.Error("Failed to get daily stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get daily stats")
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"period": "daily",
		"date":   today,
		"stats":  stats,
	})
}

// handleStatsWeekly returns this week's statistics
func (h *Handler) handleStatsWeekly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	now := time.Now()
	// Get start of week (Monday)
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday
	}
	startOfWeek := now.AddDate(0, 0, -(weekday - 1))
	startDate := startOfWeek.Format("2006-01-02")
	endDate := now.Format("2006-01-02")

	stats, err := h.getStatsForPeriod(startDate, endDate)
	if err != nil {
		logger.Error("Failed to get weekly stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get weekly stats")
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"period":    "weekly",
		"startDate": startDate,
		"endDate":   endDate,
		"stats":     stats,
	})
}

// handleStatsMonthly returns this month's statistics
func (h *Handler) handleStatsMonthly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	startDate := startOfMonth.Format("2006-01-02")
	endDate := now.Format("2006-01-02")

	stats, err := h.getStatsForPeriod(startDate, endDate)
	if err != nil {
		logger.Error("Failed to get monthly stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get monthly stats")
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"period":    "monthly",
		"startDate": startDate,
		"endDate":   endDate,
		"stats":     stats,
	})
}

// handleStatsTrends returns trend comparison data
func (h *Handler) handleStatsTrends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Get today's stats
	todayStats, err := h.getStatsForPeriod(today, today)
	if err != nil {
		logger.Error("Failed to get today's stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get trend stats")
		return
	}

	// Get yesterday's stats
	yesterdayStats, err := h.getStatsForPeriod(yesterday, yesterday)
	if err != nil {
		logger.Error("Failed to get yesterday's stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get trend stats")
		return
	}

	// Calculate changes
	trends := map[string]interface{}{
		"todayVsYesterday": map[string]interface{}{
			"requests": map[string]interface{}{
				"today":     todayStats.TotalRequests,
				"yesterday": yesterdayStats.TotalRequests,
				"change":    calculatePercentChange(yesterdayStats.TotalRequests, todayStats.TotalRequests),
			},
			"errors": map[string]interface{}{
				"today":     todayStats.TotalErrors,
				"yesterday": yesterdayStats.TotalErrors,
				"change":    calculatePercentChange(yesterdayStats.TotalErrors, todayStats.TotalErrors),
			},
			"inputTokens": map[string]interface{}{
				"today":     todayStats.TotalInputTokens,
				"yesterday": yesterdayStats.TotalInputTokens,
				"change":    calculatePercentChange(int(yesterdayStats.TotalInputTokens), int(todayStats.TotalInputTokens)),
			},
			"outputTokens": map[string]interface{}{
				"today":     todayStats.TotalOutputTokens,
				"yesterday": yesterdayStats.TotalOutputTokens,
				"change":    calculatePercentChange(int(yesterdayStats.TotalOutputTokens), int(todayStats.TotalOutputTokens)),
			},
		},
	}

	WriteSuccess(w, trends)
}

// periodStats is the aggregate returned by getStatsForPeriod.
//
// It used to be a map[string]interface{}, which forced every consumer into
// unchecked type assertions (`stats["totalRequests"].(int)`) that would panic on
// the first shape change. The JSON field names are unchanged.
type periodStats struct {
	TotalRequests     int                       `json:"totalRequests"`
	TotalErrors       int                       `json:"totalErrors"`
	TotalSuccess      int                       `json:"totalSuccess"`
	TotalInputTokens  int64                     `json:"totalInputTokens"`
	TotalOutputTokens int64                     `json:"totalOutputTokens"`
	Endpoints         map[string]periodEndpoint `json:"endpoints"`
}

type periodEndpoint struct {
	Requests     int   `json:"requests"`
	Errors       int   `json:"errors"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

// getStatsForPeriod retrieves statistics for a date range.
//
// Aggregation happens in SQL: the previous implementation pulled the entire
// daily_stats table via GetAllStats() and filtered by date in Go, which is a
// full scan plus GROUP BY for what is already an indexed range query.
func (h *Handler) getStatsForPeriod(startDate, endDate string) (periodStats, error) {
	aggregated, err := h.storage.GetPeriodStatsAggregated(startDate, endDate)
	if err != nil {
		return periodStats{}, err
	}

	out := periodStats{Endpoints: make(map[string]periodEndpoint, len(aggregated))}
	for endpointName, st := range aggregated {
		if st == nil || st.Requests <= 0 {
			continue
		}
		out.Endpoints[endpointName] = periodEndpoint{
			Requests:     st.Requests,
			Errors:       st.Errors,
			InputTokens:  st.InputTokens,
			OutputTokens: st.OutputTokens,
		}
		out.TotalRequests += st.Requests
		out.TotalErrors += st.Errors
		out.TotalInputTokens += st.InputTokens
		out.TotalOutputTokens += st.OutputTokens
	}
	out.TotalSuccess = out.TotalRequests - out.TotalErrors
	return out, nil
}

// calculatePercentChange calculates the percentage change between two values
func calculatePercentChange(old, new int) float64 {
	if old == 0 {
		if new == 0 {
			return 0
		}
		return 100.0
	}
	return float64(new-old) / float64(old) * 100.0
}
