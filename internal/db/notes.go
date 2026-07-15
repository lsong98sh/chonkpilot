package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// execer is satisfied by both *sql.DB and *sql.Tx for use in transactions.
type execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// CreateNote inserts a new note. Returns error if title already exists.
func CreateNote(db execer, note *models.Note) error {
	_, err := db.Exec(
		`INSERT INTO notes (title, content, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		note.Title, note.Content, note.CreatedAt, note.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create note: %w", err)
	}
	return nil
}

// UpdateNote updates an existing note's content and updated_at.
func UpdateNote(db execer, title, content string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(
		`UPDATE notes SET content = ?, updated_at = ? WHERE title = ?`,
		content, now, title,
	)
	if err != nil {
		return fmt.Errorf("failed to update note: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("note not found: %s", title)
	}
	return nil
}

// GetNote retrieves a note by title.
func GetNote(db *sql.DB, title string) (*models.Note, error) {
	n := &models.Note{}
	err := db.QueryRow(
		`SELECT title, content, created_at, updated_at FROM notes WHERE title = ?`,
		title,
	).Scan(&n.Title, &n.Content, &n.CreatedAt, &n.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("note not found: %s", title)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get note: %w", err)
	}
	return n, nil
}

// ListNotes returns all notes ordered by updated_at desc.
func ListNotes(db *sql.DB) ([]*models.Note, error) {
	rows, err := db.Query(`SELECT title, content, created_at, updated_at FROM notes ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to list notes: %w", err)
	}
	defer rows.Close()

	var notes []*models.Note
	for rows.Next() {
		n := &models.Note{}
		if err := rows.Scan(&n.Title, &n.Content, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan note: %w", err)
		}
		notes = append(notes, n)
	}
	if notes == nil {
		notes = []*models.Note{}
	}
	return notes, rows.Err()
}

// DeleteNote removes a note by title.
func DeleteNote(db *sql.DB, title string) error {
	res, err := db.Exec(`DELETE FROM notes WHERE title = ?`, title)
	if err != nil {
		return fmt.Errorf("failed to delete note: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("note not found: %s", title)
	}
	return nil
}
