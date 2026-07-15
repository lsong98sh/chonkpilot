//go:build windows
// +build windows

package main

import (
	"database/sql"
	"fmt"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
)

// ─── Note operations ───────────────────────────────────────

// GetNotes returns all notes.
func (a *App) GetNotes() (map[string]interface{}, error) {
	var notes []*models.Note
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		notes, err = db.ListNotes(sqlDB)
		return err
	})
	if err != nil {
		return nil, err
	}
	if notes == nil {
		notes = []*models.Note{}
	}
	return map[string]interface{}{"notes": notes}, nil
}

// GetNote returns a single note by title.
func (a *App) GetNote(title string) (map[string]interface{}, error) {
	var note *models.Note
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		note, err = db.GetNote(sqlDB, title)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"note": note}, nil
}

// SaveNote creates or updates a note.
func (a *App) SaveNote(args map[string]interface{}) (map[string]interface{}, error) {
	title, _ := args["title"].(string)
	content, _ := args["content"].(string)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		tx, err := sqlDB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if err := db.UpdateNote(tx, title, content); err != nil {
			note := models.NewNote(title, content)
			if err := db.CreateNote(tx, note); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"title": title}, nil
}

// DeleteNote deletes a note by title.
func (a *App) DeleteNote(title string) (map[string]interface{}, error) {
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.DeleteNote(sqlDB, title)
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"title": title}, nil
}
