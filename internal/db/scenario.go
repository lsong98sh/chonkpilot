package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
	_ "modernc.org/sqlite"
)

// ScenarioDBDir returns the ~/.chonkpilot directory, creating it if needed.
func ScenarioDBDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	dir := filepath.Join(home, ".chonkpilot")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("mkdir .chonkpilot: %w", err)
	}
	return dir, nil
}

// OpenScenarioDB opens or creates the scenario.db at ~/.chonkpilot/scenario.db.
func OpenScenarioDB() (*sql.DB, error) {
	dir, err := ScenarioDBDir()
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, "scenario.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open scenario.db: %w", err)
	}
	pragmas := []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}
	if err := RunScenarioMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("scenario migrations: %w", err)
	}
	return db, nil
}

// RunScenarioMigrations creates scenario tables if they don't exist.
func RunScenarioMigrations(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS scenarios (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			system_prompt TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS scenario_agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scenario_id INTEGER NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
			name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			prompt TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
	`)
	return err
}

// GetAllScenarios returns all scenarios from scenario.db.
func GetAllScenarios(sdb *sql.DB) ([]models.ScenarioConfig, error) {
	rows, err := sdb.Query(`SELECT id, name, description, system_prompt, created_at, updated_at FROM scenarios ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scenarios []models.ScenarioConfig
	for rows.Next() {
		var s models.ScenarioConfig
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.SystemPrompt, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, rows.Err()
}

// SaveScenario inserts or updates a scenario.
func SaveScenario(sdb *sql.DB, s *models.ScenarioConfig) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if s.ID > 0 {
		_, err := sdb.Exec(
			`UPDATE scenarios SET name=?, description=?, system_prompt=?, updated_at=? WHERE id=?`,
			s.Name, s.Description, s.SystemPrompt, now, s.ID,
		)
		return err
	}
	result, err := sdb.Exec(
		`INSERT INTO scenarios (name, description, system_prompt, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		s.Name, s.Description, s.SystemPrompt, now, now,
	)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	s.ID = id
	return nil
}

// DeleteScenario deletes a scenario by ID.
func DeleteScenario(sdb *sql.DB, id int64) error {
	_, err := sdb.Exec(`DELETE FROM scenarios WHERE id=?`, id)
	return err
}
