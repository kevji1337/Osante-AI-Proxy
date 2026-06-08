package storage

// Schema definition and incremental migrations. Pulled out of sqlite.go to
// keep the bulky DDL string out of the way of the query/operation code.

func (s *SQLiteStorage) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS endpoints (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		api_url TEXT NOT NULL,
		api_key TEXT NOT NULL,
		auth_mode TEXT NOT NULL DEFAULT 'api_key',
		enabled BOOLEAN DEFAULT TRUE,
		transformer TEXT DEFAULT 'claude',
		model TEXT,
		remark TEXT,
		sort_order INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS endpoint_credentials (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		endpoint_name TEXT NOT NULL,
		provider_type TEXT NOT NULL DEFAULT 'codex',
		account_id TEXT,
		email TEXT,
		access_token TEXT NOT NULL,
		refresh_token TEXT,
		id_token TEXT,
		last_refresh DATETIME,
		expires_at DATETIME,
		status TEXT NOT NULL DEFAULT 'active',
		enabled BOOLEAN DEFAULT TRUE,
		failure_count INTEGER DEFAULT 0,
		cooldown_until DATETIME,
		last_checked_at DATETIME,
		last_used_at DATETIME,
		last_error TEXT,
		remark TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS credential_rate_limits (
		credential_id INTEGER PRIMARY KEY,
		snapshot_json TEXT,
		last_status TEXT,
		last_error TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS credential_usage (
		credential_id INTEGER PRIMARY KEY,
		endpoint_name TEXT NOT NULL,
		requests INTEGER DEFAULT 0,
		errors INTEGER DEFAULT 0,
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS daily_stats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		endpoint_name TEXT NOT NULL,
		date TEXT NOT NULL,
		requests INTEGER DEFAULT 0,
		errors INTEGER DEFAULT 0,
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		device_id TEXT DEFAULT 'default',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(endpoint_name, date, device_id)
	);

	CREATE TABLE IF NOT EXISTS app_config (
		key TEXT PRIMARY KEY,
		value TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_daily_stats_date ON daily_stats(date);
	CREATE INDEX IF NOT EXISTS idx_daily_stats_endpoint ON daily_stats(endpoint_name);
	CREATE INDEX IF NOT EXISTS idx_daily_stats_device ON daily_stats(device_id);
	CREATE INDEX IF NOT EXISTS idx_endpoint_credentials_endpoint ON endpoint_credentials(endpoint_name);
	CREATE INDEX IF NOT EXISTS idx_endpoint_credentials_status ON endpoint_credentials(status);
	CREATE INDEX IF NOT EXISTS idx_endpoint_credentials_expires_at ON endpoint_credentials(expires_at);
	CREATE INDEX IF NOT EXISTS idx_credential_rate_limits_updated ON credential_rate_limits(updated_at);
	CREATE INDEX IF NOT EXISTS idx_credential_usage_endpoint ON credential_usage(endpoint_name);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	if err := s.migrateSortOrder(); err != nil {
		return err
	}
	if err := s.migrateAuthMode(); err != nil {
		return err
	}

	return nil
}

// migrateSortOrder adds the sort_order column to existing databases that
// pre-date its introduction. Idempotent.
func (s *SQLiteStorage) migrateSortOrder() error {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('endpoints') WHERE name='sort_order'`).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		if _, err := s.db.Exec(`ALTER TABLE endpoints ADD COLUMN sort_order INTEGER DEFAULT 0`); err != nil {
			return err
		}

		// Seed sort_order from id so existing rows preserve their previous
		// ordering instead of all stacking up at zero.
		if _, err := s.db.Exec(`UPDATE endpoints SET sort_order = id WHERE sort_order = 0`); err != nil {
			return err
		}
	}

	return nil
}

// migrateAuthMode adds the auth_mode column to existing databases that
// pre-date its introduction and backfills empty values to 'api_key'. The
// later token-pool migration in main.go promotes those rows to 'token_pool'
// on first launch.
func (s *SQLiteStorage) migrateAuthMode() error {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('endpoints') WHERE name='auth_mode'`).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		if _, err := s.db.Exec(`ALTER TABLE endpoints ADD COLUMN auth_mode TEXT NOT NULL DEFAULT 'api_key'`); err != nil {
			return err
		}
	}

	_, err = s.db.Exec(`UPDATE endpoints SET auth_mode='api_key' WHERE auth_mode IS NULL OR auth_mode=''`)
	return err
}
