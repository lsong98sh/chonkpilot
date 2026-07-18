package db

import (
	"database/sql"
	"fmt"
)

// RunMigrations creates all tables if they don't exist.
func RunMigrations(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			parent_id TEXT DEFAULT NULL,
			work_dir TEXT NOT NULL,
			title TEXT DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_id)`,

		`CREATE TABLE IF NOT EXISTS turns (
			turn_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(session_id),
			score INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_turns_session ON turns(session_id)`,
		`CREATE TABLE IF NOT EXISTS messages (
			message_id TEXT PRIMARY KEY,
			turn_id TEXT NOT NULL REFERENCES turns(turn_id),
			role TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'text',
			content TEXT DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_turn ON messages(turn_id)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			task_id TEXT PRIMARY KEY,
			parent_task_id TEXT DEFAULT NULL REFERENCES tasks(task_id),
			turn_id TEXT NOT NULL REFERENCES turns(turn_id),
			session_id TEXT NOT NULL REFERENCES sessions(session_id),
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			progress INTEGER DEFAULT 0,
			result TEXT DEFAULT NULL,
			executor_pid INTEGER DEFAULT NULL,
			pipe_path TEXT DEFAULT NULL,
			prompt_file TEXT DEFAULT NULL,
			depth INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_parent ON tasks(parent_task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE TABLE IF NOT EXISTS rules (
			name TEXT PRIMARY KEY,
			category TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rules_category ON rules(category)`,
		`CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS notes (
			title TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS turn_summaries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			summary TEXT NOT NULL,
			last_turn_id TEXT NOT NULL,
			version INTEGER DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_summaries_session ON turn_summaries(session_id)`,
	}
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migration failed: %w\nQuery: %s", err, q)
		}
	}

	// Add dedicated tables for project-level configurations
	newTables := []string{
		`CREATE TABLE IF NOT EXISTS project_agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			use_case TEXT DEFAULT '',
			prompt TEXT NOT NULL,
			source TEXT DEFAULT 'user',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS project_tools (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			command TEXT DEFAULT '',
			description TEXT DEFAULT '',
			parameters TEXT DEFAULT '{}',
			source TEXT DEFAULT 'user',
			mcp_id TEXT DEFAULT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS project_prompts (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS project_security (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			dir TEXT NOT NULL,
			writable INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, q := range newTables {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("new table migration failed: %w\nQuery: %s", err, q)
		}
	}

	// Migrate data from config table to dedicated tables (idempotent, best-effort)
	_ = MigrateProjectConfigs(db)

	// Run optional migrations for existing databases (safe to fail if already applied)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN parent_id TEXT DEFAULT NULL`)
	_, _ = db.Exec(`ALTER TABLE messages ADD COLUMN type TEXT DEFAULT 'text'`)
	_, _ = db.Exec(`ALTER TABLE project_agents ADD COLUMN llm_ref TEXT DEFAULT ''`)

	// Add brief column to messages table for tool_call thinking snippets
	_, _ = db.Exec(`ALTER TABLE messages ADD COLUMN brief TEXT DEFAULT ''`)

	// Migrate turns table: drop q/a columns (old schema)
	var hasQCol int
	_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('turns') WHERE name='q'`).Scan(&hasQCol)
	if hasQCol > 0 {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin turn migration transaction: %w", err)
		}
		defer tx.Rollback()

		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS turns_v2 (
			turn_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(session_id),
			score INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`); err != nil {
			return fmt.Errorf("turn migration create v2 failed: %w", err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO turns_v2 (turn_id, session_id, score, created_at, updated_at) SELECT turn_id, session_id, score, created_at, updated_at FROM turns`); err != nil {
			return fmt.Errorf("turn migration copy failed: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE IF EXISTS turns_old`); err != nil {
			return fmt.Errorf("turn migration drop old helper failed: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE turns RENAME TO turns_old`); err != nil {
			return fmt.Errorf("turn migration rename to old failed: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE turns_v2 RENAME TO turns`); err != nil {
			return fmt.Errorf("turn migration rename v2 failed: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE IF EXISTS turns_old`); err != nil {
			return fmt.Errorf("turn migration drop old failed: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("turn migration commit failed: %w", err)
		}
	}

	return nil
}
