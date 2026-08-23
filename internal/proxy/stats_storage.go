package proxy

import (
	"github.com/kevji1337/Osante-AI-Proxy/internal/storage"
)

// sqliteStatsStorage adapts *storage.SQLiteStorage to StatsStorage.
//
// It lives here rather than in the storage package because only this side knows
// the proxy-local stat types. The previous arrangement put the adapter in
// storage, where it could not name those types without an import cycle, so both
// sides talked in interface{} and reconstructed the fields by reflection.
type sqliteStatsStorage struct {
	db *storage.SQLiteStorage
}

// NewSQLiteStatsStorage wraps a SQLite store as the statistics backend.
func NewSQLiteStatsStorage(db *storage.SQLiteStorage) StatsStorage {
	return &sqliteStatsStorage{db: db}
}

func (a *sqliteStatsStorage) RecordDailyStat(stat StatRecord) error {
	return a.db.RecordDailyStat(&storage.DailyStat{
		EndpointName: stat.EndpointName,
		Date:         stat.Date,
		Requests:     stat.Requests,
		Errors:       stat.Errors,
		InputTokens:  stat.InputTokens,
		OutputTokens: stat.OutputTokens,
		DeviceID:     stat.DeviceID,
	})
}

func (a *sqliteStatsStorage) GetTotalStats() (int, map[string]StatsData, error) {
	totalRequests, perEndpoint, err := a.db.GetTotalStats()
	if err != nil {
		return 0, nil, err
	}
	out := make(map[string]StatsData, len(perEndpoint))
	for name, st := range perEndpoint {
		if st == nil {
			continue
		}
		out[name] = StatsData{
			Requests:     st.Requests,
			Errors:       st.Errors,
			InputTokens:  st.InputTokens,
			OutputTokens: st.OutputTokens,
		}
	}
	return totalRequests, out, nil
}
