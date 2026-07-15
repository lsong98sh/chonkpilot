package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens a connection to the SQLite database at .ide/ide.db in the given workDir.
// It creates the .ide directory and runs migrations if needed.
func Open(workDir string) (*sql.DB, error) {
	ideDir := filepath.Join(workDir, ".ide")
	if err := os.MkdirAll(ideDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .ide directory: %w", err)
	}

	dbPath := filepath.Join(ideDir, "ide.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set pragmas: busy_timeout FIRST so it applies to subsequent PRAGMAs
	pragmas := []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	}
	// Retry pragmas with backoff (can fail under concurrent sub-executor access)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		lastErr = nil
		for _, p := range pragmas {
			if _, err := db.Exec(p); err != nil {
				lastErr = fmt.Errorf("failed to set pragma %s: %w", p, err)
				break
			}
		}
		if lastErr == nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 300 * time.Millisecond)
	}
	if lastErr != nil {
		db.Close()
		return nil, lastErr
	}

	// Auto-migrate so executor can run standalone
	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

// Close closes the database connection.
func Close(db *sql.DB) error {
	if db != nil {
		return db.Close()
	}
	return nil
}

// WithDB opens a connection, calls the given function, and closes the connection.
func WithDB(workDir string, fn func(*sql.DB) error) error {
	db, err := Open(workDir)
	if err != nil {
		return err
	}
	defer Close(db)
	return fn(db)
}
