package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// CreateTurn inserts a new turn record.
func CreateTurn(db *sql.DB, t *models.Turn) error {
	_, err := db.Exec(
		`INSERT INTO turns (turn_id, session_id, score, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		t.TurnID, t.SessionID, t.Score, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create turn: %w", err)
	}
	return nil
}

// GetTurn retrieves a single turn by turn_id.
func GetTurn(db *sql.DB, turnID string) (*models.Turn, error) {
	row := db.QueryRow(
		`SELECT turn_id, session_id, score, created_at, updated_at FROM turns WHERE turn_id = ?`,
		turnID,
	)
	t := &models.Turn{}
	if err := row.Scan(&t.TurnID, &t.SessionID, &t.Score, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, fmt.Errorf("failed to get turn: %w", err)
	}
	return t, nil
}

// UpdateTurnResult updates the score of a turn.
func UpdateTurnResult(db *sql.DB, turnID string, score int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE turns SET score = ?, updated_at = ? WHERE turn_id = ?`,
		score, now, turnID,
	)
	if err != nil {
		return fmt.Errorf("failed to update turn result: %w", err)
	}
	return nil
}

// GetTurnsBySession returns all turns for a session ordered by created_at.
func GetTurnsBySession(db *sql.DB, sessionID string) ([]*models.Turn, error) {
	rows, err := db.Query(
		`SELECT turn_id, session_id, score, created_at, updated_at FROM turns WHERE session_id = ? ORDER BY created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get turns: %w", err)
	}
	return scanAll(rows, func(t *models.Turn) []any {
		return []any{&t.TurnID, &t.SessionID, &t.Score, &t.CreatedAt, &t.UpdatedAt}
	})
}
