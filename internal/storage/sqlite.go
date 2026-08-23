package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kevji1337/Osante-AI-Proxy/internal/config"
	_ "modernc.org/sqlite"
)

type SQLiteStorage struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex
}

func NewSQLiteStorage(dbPath string) (*SQLiteStorage, error) {
	// busy_timeout and synchronous are CONNECTION-scoped pragmas: running them
	// through db.Exec only configures whichever pooled connection happened to
	// serve that call, leaving the rest at busy_timeout=0 (instant SQLITE_BUSY
	// under concurrent writes) and synchronous=FULL (slower on every write).
	// Passing them in the DSN makes modernc.org/sqlite apply them to every
	// connection it opens. journal_mode=WAL is persisted in the file itself, but
	// declaring it here too keeps all three in one place.
	dsn := dbPath + "?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// SQLite tolerates exactly one writer. Capping the pool keeps writes queued
	// in Go (fair, no lock spinning) instead of racing for the file lock.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)

	s := &SQLiteStorage{
		db:     db,
		dbPath: dbPath,
	}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

// initSchema, migrateSortOrder, migrateAuthMode live in schema.go.

func (s *SQLiteStorage) GetEndpoints() ([]Endpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, name, api_url, api_key, auth_mode, enabled, transformer, model, remark, sort_order, created_at, updated_at FROM endpoints ORDER BY sort_order ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var endpoints []Endpoint
	for rows.Next() {
		var ep Endpoint
		if err := rows.Scan(&ep.ID, &ep.Name, &ep.APIUrl, &ep.APIKey, &ep.AuthMode, &ep.Enabled, &ep.Transformer, &ep.Model, &ep.Remark, &ep.SortOrder, &ep.CreatedAt, &ep.UpdatedAt); err != nil {
			return nil, err
		}
		normalizeEndpointAuthMode(&ep)
		endpoints = append(endpoints, ep)
	}

	return endpoints, rows.Err()
}

func (s *SQLiteStorage) SaveEndpoint(ep *Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalizeEndpointAuthMode(ep)

	result, err := s.db.Exec(`INSERT INTO endpoints (name, api_url, api_key, auth_mode, enabled, transformer, model, remark, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ep.Name, ep.APIUrl, ep.APIKey, ep.AuthMode, ep.Enabled, ep.Transformer, ep.Model, ep.Remark, ep.SortOrder)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	ep.ID = id
	return nil
}

// UpdateEndpoint updates an endpoint in place.
//
// When ep.ID is set the row is addressed by id, which is what makes renames
// work: the WHERE clause must not use the *new* name, because that matches
// nothing and the UPDATE silently affects 0 rows — the admin API then reported
// success while every edit in the same request (url, model, enabled, remark)
// was dropped.
//
// A rename also has to carry the endpoint's data along. endpoint_name is a
// plain TEXT column in endpoint_credentials, credential_usage and daily_stats
// with no foreign key, so without the cascade the token pool and the whole
// statistics history would be orphaned.
func (s *SQLiteStorage) UpdateEndpoint(ep *Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalizeEndpointAuthMode(ep)

	if ep.ID <= 0 {
		// Legacy path: address by name. Used by ConfigStorageAdapter, which has
		// no id to pass; renames never come through here.
		res, err := s.db.Exec(`UPDATE endpoints SET api_url=?, api_key=?, auth_mode=?, enabled=?, transformer=?, model=?, remark=?, sort_order=?, updated_at=CURRENT_TIMESTAMP WHERE name=?`,
			ep.APIUrl, ep.APIKey, ep.AuthMode, ep.Enabled, ep.Transformer, ep.Model, ep.Remark, ep.SortOrder, ep.Name)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return fmt.Errorf("endpoint %q not found", ep.Name)
		}
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var oldName string
	if err := tx.QueryRow(`SELECT name FROM endpoints WHERE id=?`, ep.ID).Scan(&oldName); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("endpoint id %d not found", ep.ID)
		}
		return err
	}

	if _, err := tx.Exec(`UPDATE endpoints SET name=?, api_url=?, api_key=?, auth_mode=?, enabled=?, transformer=?, model=?, remark=?, sort_order=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		ep.Name, ep.APIUrl, ep.APIKey, ep.AuthMode, ep.Enabled, ep.Transformer, ep.Model, ep.Remark, ep.SortOrder, ep.ID); err != nil {
		return fmt.Errorf("failed to update endpoint %q: %w", oldName, err)
	}

	if oldName != ep.Name {
		if err := renameEndpointReferences(tx, oldName, ep.Name); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// renameEndpointReferences moves the credential pool, per-credential usage and
// daily statistics of oldName over to newName inside an open transaction.
func renameEndpointReferences(tx *sql.Tx, oldName, newName string) error {
	if _, err := tx.Exec(`UPDATE endpoint_credentials SET endpoint_name=?, updated_at=CURRENT_TIMESTAMP WHERE endpoint_name=?`, newName, oldName); err != nil {
		return fmt.Errorf("failed to move credentials to %q: %w", newName, err)
	}
	if _, err := tx.Exec(`UPDATE credential_usage SET endpoint_name=? WHERE endpoint_name=?`, newName, oldName); err != nil {
		return fmt.Errorf("failed to move credential usage to %q: %w", newName, err)
	}

	// daily_stats has UNIQUE(endpoint_name, date, device_id), so a plain UPDATE
	// would fail whenever the new name already has rows for the same days
	// (leftovers from an endpoint that used to carry that name). Merge instead,
	// then drop the old rows.
	if _, err := tx.Exec(`
		INSERT INTO daily_stats (endpoint_name, date, requests, errors, input_tokens, output_tokens, device_id)
		SELECT ?, date, requests, errors, input_tokens, output_tokens, device_id
		FROM daily_stats WHERE endpoint_name=?
		ON CONFLICT(endpoint_name, date, device_id) DO UPDATE SET
			requests      = requests + excluded.requests,
			errors        = errors + excluded.errors,
			input_tokens  = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens`, newName, oldName); err != nil {
		return fmt.Errorf("failed to move daily stats to %q: %w", newName, err)
	}
	if _, err := tx.Exec(`DELETE FROM daily_stats WHERE endpoint_name=?`, oldName); err != nil {
		return fmt.Errorf("failed to clean up old daily stats for %q: %w", oldName, err)
	}
	return nil
}

func (s *SQLiteStorage) DeleteEndpoint(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(`
		DELETE FROM credential_rate_limits
		WHERE credential_id IN (
			SELECT id FROM endpoint_credentials WHERE endpoint_name=?
		)
	`, name); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM endpoint_credentials WHERE endpoint_name=?`, name); err != nil {
		return err
	}

	_, err := s.db.Exec(`DELETE FROM endpoints WHERE name=?`, name)
	return err
}

func (s *SQLiteStorage) RecordDailyStat(stat *DailyStat) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO daily_stats (endpoint_name, date, requests, errors, input_tokens, output_tokens, device_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(endpoint_name, date, device_id) DO UPDATE SET
			requests = requests + excluded.requests,
			errors = errors + excluded.errors,
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens
	`, stat.EndpointName, stat.Date, stat.Requests, stat.Errors, stat.InputTokens, stat.OutputTokens, stat.DeviceID)

	return err
}

func (s *SQLiteStorage) GetDailyStats(endpointName, startDate, endDate string) ([]DailyStat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, endpoint_name, date, SUM(requests), SUM(errors), SUM(input_tokens), SUM(output_tokens), device_id, created_at
		FROM daily_stats WHERE endpoint_name=? AND date>=? AND date<=? GROUP BY date ORDER BY date DESC`

	rows, err := s.db.Query(query, endpointName, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var stats []DailyStat
	for rows.Next() {
		var stat DailyStat
		if err := rows.Scan(&stat.ID, &stat.EndpointName, &stat.Date, &stat.Requests, &stat.Errors, &stat.InputTokens, &stat.OutputTokens, &stat.DeviceID, &stat.CreatedAt); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}

	return stats, rows.Err()
}

func (s *SQLiteStorage) GetConfig(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var value string
	err := s.db.QueryRow(`SELECT value FROM app_config WHERE key=?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *SQLiteStorage) SetConfig(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`INSERT INTO app_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`, key, value)
	return err
}

func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}

func (s *SQLiteStorage) GetTotalStats() (int, map[string]*EndpointStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT endpoint_name, SUM(requests), SUM(errors), SUM(input_tokens), SUM(output_tokens)
		FROM daily_stats GROUP BY endpoint_name`

	rows, err := s.db.Query(query)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]*EndpointStats)
	totalRequests := 0

	for rows.Next() {
		var endpointName string
		var requests, errors int
		var inputTokens, outputTokens int64

		if err := rows.Scan(&endpointName, &requests, &errors, &inputTokens, &outputTokens); err != nil {
			return 0, nil, err
		}

		result[endpointName] = &EndpointStats{
			Requests:     requests,
			Errors:       errors,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		}
		totalRequests += requests
	}

	return totalRequests, result, rows.Err()
}

func (s *SQLiteStorage) GetEndpointTotalStats(endpointName string) (*EndpointStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// A bare SUM() over zero rows yields one row of NULLs, not ErrNoRows, so the
	// ErrNoRows branch never fired and Scan into *int failed outright for every
	// endpoint that has no stats yet. COALESCE gives the zeros we actually want.
	query := `SELECT COALESCE(SUM(requests), 0), COALESCE(SUM(errors), 0),
			COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM daily_stats WHERE endpoint_name=?`

	var requests, errors int
	var inputTokens, outputTokens int64

	err := s.db.QueryRow(query, endpointName).Scan(&requests, &errors, &inputTokens, &outputTokens)
	if err != nil {
		return nil, err
	}

	return &EndpointStats{
		Requests:     requests,
		Errors:       errors,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}

// GetPeriodStatsAggregated returns aggregated statistics for all endpoints in a time period using a single query
func (s *SQLiteStorage) GetPeriodStatsAggregated(startDate, endDate string) (map[string]*EndpointStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT endpoint_name, SUM(requests), SUM(errors), SUM(input_tokens), SUM(output_tokens)
		FROM daily_stats
		WHERE date >= ? AND date <= ?
		GROUP BY endpoint_name`

	rows, err := s.db.Query(query, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]*EndpointStats)
	for rows.Next() {
		var endpointName string
		var requests, errors int
		var inputTokens, outputTokens int64

		if err := rows.Scan(&endpointName, &requests, &errors, &inputTokens, &outputTokens); err != nil {
			return nil, err
		}

		result[endpointName] = &EndpointStats{
			Requests:     requests,
			Errors:       errors,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		}
	}

	return result, rows.Err()
}

// GetOrCreateDeviceID returns the device ID, creating one if it doesn't exist
func (s *SQLiteStorage) GetOrCreateDeviceID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Try to get existing device ID
	var deviceID string
	err := s.db.QueryRow(`SELECT value FROM app_config WHERE key = 'device_id'`).Scan(&deviceID)

	if err == nil && deviceID != "" {
		return deviceID, nil
	}

	// Generate new device ID
	deviceID = generateDeviceID()

	// Save to database
	_, err = s.db.Exec(`INSERT OR REPLACE INTO app_config (key, value) VALUES ('device_id', ?)`, deviceID)
	if err != nil {
		return "", err
	}

	return deviceID, nil
}

func generateDeviceID() string {
	// Use UUID v4 for guaranteed uniqueness
	return "device-" + uuid.New().String()
}

func GenerateDeviceID() string {
	return generateDeviceID()
}

// GetDBPath returns the database file path
func (s *SQLiteStorage) GetDBPath() string {
	return s.dbPath
}

// DeleteAllStats wipes every row from daily_stats and credential_usage. Used
// by the admin "flush stats" action. Endpoints + credentials are preserved.
// Returns the number of daily_stats rows deleted (the headline metric).
func (s *SQLiteStorage) DeleteAllStats() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`DELETE FROM daily_stats`)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`DELETE FROM credential_usage`); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ClearAllTokenCooldowns drops cooldown_until on every credential of every
// endpoint. Use after fixing an upstream — the next request will go straight
// through instead of waiting for the scheduled reset time.
func (s *SQLiteStorage) ClearAllTokenCooldowns() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`
		UPDATE endpoint_credentials
		SET
			cooldown_until = NULL,
			status = CASE
				WHEN status = 'cooldown' THEN 'active'
				ELSE status
			END,
			updated_at = CURRENT_TIMESTAMP
		WHERE cooldown_until IS NOT NULL
	`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func normalizeEndpointAuthMode(ep *Endpoint) {
	if ep == nil {
		return
	}
	normalized := config.Endpoint{
		Name:        ep.Name,
		APIUrl:      ep.APIUrl,
		APIKey:      ep.APIKey,
		AuthMode:    ep.AuthMode,
		Enabled:     ep.Enabled,
		Transformer: ep.Transformer,
		Model:       ep.Model,
		Remark:      ep.Remark,
	}
	if normalized.Transformer == "" {
		normalized.Transformer = "claude"
	}
	config.ApplyEndpointAuthModeRules(&normalized)
	ep.APIUrl = normalized.APIUrl
	ep.APIKey = normalized.APIKey
	ep.AuthMode = normalized.AuthMode
	ep.Transformer = normalized.Transformer
	ep.Model = normalized.Model
	ep.Remark = normalized.Remark
}
