package db

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// AddMessage inserts a new message record.
func AddMessage(db *sql.DB, msg *models.Message) error {
	_, err := db.Exec(
		`INSERT INTO messages (message_id, turn_id, role, type, content, brief, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		msg.MessageID, msg.TurnID, msg.Role, msg.Type, msg.Content, msg.Brief, msg.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to add message: %w", err)
	}
	return nil
}

// UpdateMessageBrief updates the brief field for a message.
func UpdateMessageBrief(sqlDB *sql.DB, messageID, brief string) error {
	_, err := sqlDB.Exec(`UPDATE messages SET brief = ? WHERE message_id = ?`, brief, messageID)
	if err != nil {
		return fmt.Errorf("failed to update message brief: %w", err)
	}
	return nil
}

// GetMessagesByTurn returns all messages for a turn ordered by created_at.
func GetMessagesByTurn(db *sql.DB, turnID string) ([]*models.Message, error) {
	rows, err := db.Query(
		`SELECT message_id, turn_id, role, COALESCE(type,'text'), COALESCE(content,''), COALESCE(brief,''), created_at FROM messages WHERE turn_id = ? ORDER BY created_at ASC`,
		turnID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	return scanAll(rows, func(m *models.Message) []any {
		return []any{&m.MessageID, &m.TurnID, &m.Role, &m.Type, &m.Content, &m.Brief, &m.CreatedAt}
	})
}

// GetMessagesBySession returns all messages across all turns in a session.
func GetMessagesBySession(db *sql.DB, sessionID string) ([]*models.Message, error) {
	rows, err := db.Query(
		`SELECT m.message_id, m.turn_id, m.role, COALESCE(m.type,'text'), COALESCE(m.content,''), COALESCE(m.brief,''), m.created_at
		 FROM messages m
		 JOIN turns t ON t.turn_id = m.turn_id
		 WHERE t.session_id = ?
		 ORDER BY t.created_at ASC, m.created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}
	return scanAll(rows, func(m *models.Message) []any {
		return []any{&m.MessageID, &m.TurnID, &m.Role, &m.Type, &m.Content, &m.Brief, &m.CreatedAt}
	})
}

// LatestSessionID returns the ID of the session that has the most recent message.
// Returns empty string if no messages exist.
func GetLatestSessionID(sqlDB *sql.DB) (string, error) {
	var sessionID string
	err := sqlDB.QueryRow(
		`SELECT t.session_id FROM messages m
		 JOIN turns t ON t.turn_id = m.turn_id
		 ORDER BY m.created_at DESC LIMIT 1`,
	).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get latest session: %w", err)
	}
	return sessionID, nil
}

// GetMessage returns a single message by ID.
func GetMessage(sqlDB *sql.DB, messageID string) (*models.Message, error) {
	m := &models.Message{}
	err := sqlDB.QueryRow(
		`SELECT message_id, turn_id, role, COALESCE(type,'text'), COALESCE(content,''), COALESCE(brief,''), created_at FROM messages WHERE message_id = ?`,
		messageID,
	).Scan(&m.MessageID, &m.TurnID, &m.Role, &m.Type, &m.Content, &m.Brief, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	return m, nil
}

// GetToolResultByCallID returns the tool result content for a given tool_call_id within a session.
// Uses SQLite json_extract to filter at the query level for efficiency.
func GetToolResultByCallID(sqlDB *sql.DB, sessionID, toolCallID string) (string, error) {
	rows, err := sqlDB.Query(
		`SELECT m.content FROM messages m
		 JOIN turns t ON t.turn_id = m.turn_id
		 WHERE t.session_id = ? AND m.role = 'tool' AND m.type = 'tool_result'
		 AND json_extract(m.content, '$.tool_call_id') = ?`,
		sessionID, toolCallID,
	)
	if err != nil {
		return "", fmt.Errorf("failed to query tool results: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return "", fmt.Errorf("failed to scan tool result: %w", err)
		}
		var tp models.ToolResultPayload
		if err := json.Unmarshal([]byte(content), &tp); err == nil && tp.ToolCallID == toolCallID {
			return tp.Result, nil
		}
	}
	return "", fmt.Errorf("tool result not found: %s", toolCallID)
}

// nullString returns a *string for nullable SQL columns.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
