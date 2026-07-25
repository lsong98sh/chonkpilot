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



// TurnStat holds the message count and total bytes for a single turn.
type TurnStat struct {
	MessageCount int
	TotalBytes   int
}

// GetTurnsPaginated returns turns with their message stats, accumulating turns
// until targetMessages or targetBytes is reached (whichever comes first).
// beforeTurnID = "" means fetch the latest (newest) turns.
// Returns: turns in chronological order, hasMore flag, error.
func GetTurnsPaginated(db *sql.DB, sessionID string, beforeTurnID string,
	targetMessages int, targetBytes int) ([]*models.Turn, bool, error) {

	rows, err := db.Query(`
		SELECT t.turn_id, t.session_id, t.score, t.created_at, t.updated_at,
		       COALESCE(COUNT(m.message_id), 0) as msg_count,
		       COALESCE(SUM(LENGTH(COALESCE(m.content,''))), 0) as total_bytes
		FROM turns t
		LEFT JOIN messages m ON m.turn_id = t.turn_id
		WHERE t.session_id = ?
		  AND (? = '' OR t.created_at < (SELECT COALESCE(created_at,'') FROM turns WHERE turn_id = ?))
		GROUP BY t.turn_id
		ORDER BY t.created_at DESC
	`, sessionID, beforeTurnID, beforeTurnID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to query turns paginated: %w", err)
	}
	defer rows.Close()

	var selected []*models.Turn
	accMessages, accBytes := 0, 0
	hasMore := false

	for rows.Next() {
		t := &models.Turn{}
		var msgCount, totalBytes int
		if err := rows.Scan(&t.TurnID, &t.SessionID, &t.Score, &t.CreatedAt, &t.UpdatedAt, &msgCount, &totalBytes); err != nil {
			return nil, false, fmt.Errorf("failed to scan turn: %w", err)
		}
		if accMessages >= targetMessages || accBytes >= targetBytes {
			hasMore = true
			break
		}
		selected = append(selected, t)
		accMessages += msgCount
		accBytes += totalBytes
	}

	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("rows iteration error: %w", err)
	}

	// Reverse to chronological order (ASC)
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}

	return selected, hasMore, nil
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
