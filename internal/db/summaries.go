package db

import (
	"database/sql"
	"time"
)

// GetLatestSummary returns the most recent summary for a session.
// Returns ("", nil) if no summary exists.
func GetLatestSummary(db *sql.DB, sessionID string) (string, error) {
	var summary string
	err := db.QueryRow(
		`SELECT summary FROM turn_summaries
		 WHERE session_id = ?
		 ORDER BY version DESC
		 LIMIT 1`,
		sessionID,
	).Scan(&summary)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return summary, nil
}

// SaveSummary creates or updates a summary for a session.
func SaveSummary(db *sql.DB, sessionID, summary, lastTurnID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Get the current max version for this session
	var currentVersion int
	err := db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM turn_summaries WHERE session_id = ?`,
		sessionID,
	).Scan(&currentVersion)
	if err != nil {
		return err
	}

	_, err = db.Exec(
		`INSERT INTO turn_summaries (session_id, summary, last_turn_id, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, summary, lastTurnID, currentVersion+1, now, now,
	)
	return err
}
