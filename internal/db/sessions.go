package db

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// CreateSession inserts a new session record.
func CreateSession(db *sql.DB, s *models.Session) error {
	_, err := db.Exec(
		`INSERT INTO sessions (session_id, parent_id, work_dir, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		s.SessionID, nullString(s.ParentID), s.WorkDir, s.Title, s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by ID.
func GetSession(db *sql.DB, sessionID string) (*models.Session, error) {
	s := &models.Session{}
	err := db.QueryRow(
		`SELECT session_id, COALESCE(parent_id,''), work_dir, title, created_at, updated_at FROM sessions WHERE session_id = ?`,
		sessionID,
	).Scan(&s.SessionID, &s.ParentID, &s.WorkDir, &s.Title, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return s, nil
}

// ListSessions returns all sessions ordered by created_at descending.
func ListSessions(db *sql.DB) ([]*models.Session, error) {
	rows, err := db.Query(
		`SELECT session_id, COALESCE(parent_id,''), work_dir, title, created_at, updated_at FROM sessions ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*models.Session
	for rows.Next() {
		s := &models.Session{}
		if err := rows.Scan(&s.SessionID, &s.ParentID, &s.WorkDir, &s.Title, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, s)
	}
	if sessions == nil {
		sessions = []*models.Session{}
	}
	return sessions, rows.Err()
}

// ListTopLevelSessions returns only top-level sessions (parent_id = ''),
// ordered by the latest activity (max updated_at among the session and its sub-sessions).
// This ensures the most recently active conversation appears first.
func ListTopLevelSessions(db *sql.DB) ([]*models.Session, error) {
	rows, err := db.Query(
		`SELECT session_id, COALESCE(parent_id,''), work_dir, title, created_at, updated_at FROM sessions`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list top-level sessions: %w", err)
	}
	defer rows.Close()

	var all []*models.Session
	for rows.Next() {
		s := &models.Session{}
		if err := rows.Scan(&s.SessionID, &s.ParentID, &s.WorkDir, &s.Title, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		all = append(all, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Build activity map: for each top-level session, find the latest updated_at
	// among itself and all its sub-sessions.
	activity := make(map[string]string, len(all))
	for _, s := range all {
		pid := s.ParentID
		if pid == "" {
			pid = s.SessionID
		}
		if curr, ok := activity[pid]; !ok || s.UpdatedAt > curr {
			activity[pid] = s.UpdatedAt
		}
	}

	// Collect and sort top-level sessions by latest activity descending
	var top []*models.Session
	for _, s := range all {
		if s.ParentID == "" {
			top = append(top, s)
		}
	}
	sort.Slice(top, func(i, j int) bool {
		actI := activity[top[i].SessionID]
		actJ := activity[top[j].SessionID]
		return actI > actJ
	})

	if top == nil {
		top = []*models.Session{}
	}
	return top, nil
}

// UpdateSessionTitle updates the title of a session.
func UpdateSessionTitle(db *sql.DB, sessionID, title string) error {
	res, err := db.Exec(`UPDATE sessions SET title = ?, updated_at = ? WHERE session_id = ?`,
		title, time.Now().UTC().Format(time.RFC3339), sessionID)
	if err != nil {
		return fmt.Errorf("failed to update session title: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return nil
}

// DeleteSession deletes a session and cascades to all descendant sessions,
// their related turns, messages, and tasks.
func DeleteSession(db *sql.DB, sessionID string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Collect all descendant session IDs (including self) using recursive CTE
	allIDs, err := collectDescendantIDs(tx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to collect descendant sessions: %w", err)
	}

	for _, id := range allIDs {
		// Delete messages for all turns in this session
		if _, err := tx.Exec(`DELETE FROM messages WHERE turn_id IN (SELECT turn_id FROM turns WHERE session_id = ?)`, id); err != nil {
			return fmt.Errorf("failed to delete messages for %s: %w", id, err)
		}
		// Delete tasks for this session
		if _, err := tx.Exec(`DELETE FROM tasks WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("failed to delete tasks for %s: %w", id, err)
		}
		// Delete turns
		if _, err := tx.Exec(`DELETE FROM turns WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("failed to delete turns for %s: %w", id, err)
		}
		// Delete session
		if _, err := tx.Exec(`DELETE FROM sessions WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("failed to delete session %s: %w", id, err)
		}
	}

	return tx.Commit()
}

// collectDescendantIDs recursively collects all descendant session IDs (including self)
// by traversing parent_id relationships.
func collectDescendantIDs(tx *sql.Tx, rootID string) ([]string, error) {
	var ids []string
	queue := []string{rootID}
	seen := map[string]bool{rootID: true}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		ids = append(ids, current)

		rows, err := tx.Query(`SELECT session_id FROM sessions WHERE parent_id = ?`, current)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var childID string
			if err := rows.Scan(&childID); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[childID] {
				seen[childID] = true
				queue = append(queue, childID)
			}
		}
		rows.Close()
	}

	return ids, nil
}
