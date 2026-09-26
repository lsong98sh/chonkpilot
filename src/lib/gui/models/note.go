package models

import "time"

// Note represents a user note stored in the IDE database.
type Note struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// NewNote creates a new Note with current timestamps.
func NewNote(title, content string) *Note {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Note{
		Title:     title,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
