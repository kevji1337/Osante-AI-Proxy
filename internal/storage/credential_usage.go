package storage

import (
	"database/sql"
	"time"
)

func (s *SQLiteStorage) UpsertCredentialUsage(credentialID int64, endpointName string, requestsDelta, errorsDelta, inputTokensDelta, outputTokensDelta int, updatedAt time.Time) error {
	if s == nil || credentialID <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO credential_usage (
			credential_id, endpoint_name, requests, errors, input_tokens, output_tokens, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(credential_id) DO UPDATE SET
			endpoint_name=excluded.endpoint_name,
			requests=requests + excluded.requests,
			errors=errors + excluded.errors,
			input_tokens=input_tokens + excluded.input_tokens,
			output_tokens=output_tokens + excluded.output_tokens,
			updated_at=excluded.updated_at
	`, credentialID, endpointName, requestsDelta, errorsDelta, inputTokensDelta, outputTokensDelta, updatedAt.UTC())
	return err
}

func (s *SQLiteStorage) GetCredentialUsageByEndpoint(endpointName string) (map[int64]*CredentialUsage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT credential_id, requests, errors, input_tokens, output_tokens, updated_at
		FROM credential_usage
		WHERE endpoint_name=?
	`, endpointName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64]*CredentialUsage)
	for rows.Next() {
		var id int64
		var requests, errors, inputTokens, outputTokens int
		var updatedAtNull sql.NullTime
		if err := rows.Scan(&id, &requests, &errors, &inputTokens, &outputTokens, &updatedAtNull); err != nil {
			return nil, err
		}
		usage := &CredentialUsage{
			CredentialID: id,
			Requests:     requests,
			Errors:       errors,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			UpdatedAt:    fromNullTime(updatedAtNull),
		}
		result[id] = usage
	}
	return result, nil
}

// RecordCredentialSuccess folds the two writes a successful request used to make
// against the credential (usage counters + "this token works") into one
// transaction, so a proxied request takes one trip through the single-writer
// SQLite lock instead of two.
func (s *SQLiteStorage) RecordCredentialSuccess(credentialID int64, endpointName string, inputTokens, outputTokens int, now time.Time) error {
	if s == nil || credentialID <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		INSERT INTO credential_usage (
			credential_id, endpoint_name, requests, errors, input_tokens, output_tokens, updated_at
		) VALUES (?, ?, 1, 0, ?, ?, ?)
		ON CONFLICT(credential_id) DO UPDATE SET
			endpoint_name=excluded.endpoint_name,
			requests=requests + excluded.requests,
			input_tokens=input_tokens + excluded.input_tokens,
			output_tokens=output_tokens + excluded.output_tokens,
			updated_at=excluded.updated_at
	`, credentialID, endpointName, inputTokens, outputTokens, now.UTC()); err != nil {
		return err
	}

	if _, err := tx.Exec(`
		UPDATE endpoint_credentials
		SET
			status=?,
			failure_count=0,
			cooldown_until=NULL,
			last_error='',
			last_checked_at=?,
			last_used_at=?,
			updated_at=CURRENT_TIMESTAMP
		WHERE id=?
	`, credentialStatusActive, now.UTC(), now.UTC(), credentialID); err != nil {
		return err
	}

	return tx.Commit()
}
