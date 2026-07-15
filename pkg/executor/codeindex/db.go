package codeindex

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// FileIndex holds the index data for a single file.
type FileIndex struct {
	Path         string        `json:"path"`
	Language     string        `json:"language"`
	Summary      string        `json:"summary"`
	Imports      []string      `json:"imports"`
	Exports      []string      `json:"exports"`
	ExternalDeps []string      `json:"external_deps"`
	Symbols      []SymbolIndex `json:"symbols"`
}

// SymbolIndex holds index data for a single symbol.
type SymbolIndex struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Exported   bool   `json:"exported"`
	Signature  string `json:"signature"`
	DocSummary string `json:"doc_summary"`
}

// IndexStatus describes the index state of a file.
type IndexStatus string

const (
	StatusPending   IndexStatus = "pending"
	StatusIndexing  IndexStatus = "indexing"
	StatusDone      IndexStatus = "done"
	StatusFailed    IndexStatus = "failed"
)

// QueueItem represents a pending index task.
type QueueItem struct {
	FilePath   string
	Checksum   string
	Status     IndexStatus
	RetryCount int
	UpdatedAt  string
}

// StaleFileInfo combines a file's index with its staleness status.
type StaleFileInfo struct {
	FileIndex
	IsQueued   bool   // pending in queue
	IsStale    bool   // checksum differs from current content
	QueueState string // "pending", "indexing", "failed"
}

// OpenCodebaseDB opens (or creates) the codebase.db in the given directory.
func OpenCodebaseDB(dbDir string) (*sql.DB, error) {
	dir := filepath.Join(dbDir, ".ide")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create .ide dir: %w", err)
	}
	dbPath := filepath.Join(dir, "codebase.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open codebase.db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate codebase.db: %w", err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'done',
			retry_count INTEGER NOT NULL DEFAULT 0,
			checksum TEXT NOT NULL DEFAULT '',
			language TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			imports TEXT NOT NULL DEFAULT '[]',
			exports TEXT NOT NULL DEFAULT '[]',
			external_deps TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS symbols (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			exported INTEGER NOT NULL DEFAULT 0,
			signature TEXT NOT NULL DEFAULT '',
			doc_summary TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (file_path) REFERENCES files(path) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name)`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_path)`,
		`CREATE INDEX IF NOT EXISTS idx_files_status ON files(status)`,
	}
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}

	// Migration v2: merge pending_indexes into files table
	// Add columns if missing (fresh install has them, upgrade needs them)
	// Note: use Exec+ignore-error because column may already exist in new DBs
	for _, alterSQL := range []string{
		`ALTER TABLE files ADD COLUMN status TEXT NOT NULL DEFAULT 'done'`,
		`ALTER TABLE files ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(alterSQL); err != nil {
			// column may already exist in newer DBs — ignore ALTER error
		}
	}

	// Check if pending_indexes table still exists (pre-v2 schema)
	var hasPending int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='pending_indexes'`).Scan(&hasPending)
	if hasPending > 0 {
		// Copy pending_indexes data into files
		db.Exec(`UPDATE files SET status = 'pending' WHERE path IN (SELECT file_path FROM pending_indexes WHERE status = 'pending')`)
		db.Exec(`UPDATE files SET status = 'indexing' WHERE path IN (SELECT file_path FROM pending_indexes WHERE status = 'indexing')`)
		db.Exec(`UPDATE files SET status = 'failed' WHERE path IN (SELECT file_path FROM pending_indexes WHERE status = 'failed')`)
		// Files in pending_indexes not yet in files table
		db.Exec(`INSERT OR IGNORE INTO files (path, status) SELECT file_path, status FROM pending_indexes`)
		db.Exec(`DROP TABLE pending_indexes`)
	}

	return nil
}

// ──────── Files CRUD ────────

// GetFileIndex retrieves the index for a file, or nil if not indexed.
func GetFileIndex(db *sql.DB, path string) (*FileIndex, error) {
	row := db.QueryRow(`SELECT path, language, summary, imports, exports, external_deps FROM files WHERE path = ?`, path)
	var fi FileIndex
	var importsJSON, exportsJSON, depsJSON string
	if err := row.Scan(&fi.Path, &fi.Language, &fi.Summary, &importsJSON, &exportsJSON, &depsJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	json.Unmarshal([]byte(importsJSON), &fi.Imports)
	json.Unmarshal([]byte(exportsJSON), &fi.Exports)
	json.Unmarshal([]byte(depsJSON), &fi.ExternalDeps)

	rows, err := db.Query(`SELECT kind, name, exported, signature, doc_summary FROM symbols WHERE file_path = ? ORDER BY id`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s SymbolIndex
		if err := rows.Scan(&s.Kind, &s.Name, &s.Exported, &s.Signature, &s.DocSummary); err != nil {
			return nil, err
		}
		fi.Symbols = append(fi.Symbols, s)
	}
	return &fi, nil
}

// GetFileChecksum returns the stored checksum for a file, or "" if not indexed.
func GetFileChecksum(db *sql.DB, path string) (string, error) {
	var cs string
	err := db.QueryRow(`SELECT checksum FROM files WHERE path = ?`, path).Scan(&cs)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return cs, err
}

// SaveFileIndex saves/updates the index for a file (transactional).
func SaveFileIndex(db *sql.DB, fi *FileIndex, checksum string) error {
	// Normalize to forward slashes for cross-platform consistency
	fi.Path = filepath.ToSlash(fi.Path)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	importsJSON, _ := json.Marshal(fi.Imports)
	exportsJSON, _ := json.Marshal(fi.Exports)
	depsJSON, _ := json.Marshal(fi.ExternalDeps)

	now := time.Now().Format(time.RFC3339)
	_, err = tx.Exec(`INSERT OR REPLACE INTO files (path, language, summary, imports, exports, external_deps, checksum, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fi.Path, fi.Language, fi.Summary, string(importsJSON), string(exportsJSON), string(depsJSON), checksum, now)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM symbols WHERE file_path = ?`, fi.Path); err != nil {
		return err
	}
	for _, s := range fi.Symbols {
		exported := 0
		if s.Exported {
			exported = 1
		}
		if _, err := tx.Exec(`INSERT INTO symbols (file_path, kind, name, exported, signature, doc_summary) VALUES (?, ?, ?, ?, ?, ?)`,
			fi.Path, s.Kind, s.Name, exported, s.Signature, s.DocSummary); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// DeleteFileIndex removes index entries for a file.
func DeleteFileIndex(db *sql.DB, path string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM symbols WHERE file_path = ?`, path)
	tx.Exec(`DELETE FROM files WHERE path = ?`, path)
	return tx.Commit()
}

// ──────── Queue CRUD ────────

// EnqueueFile adds a file to the index queue.
// If the file already exists in files table with matching checksum and status='done', it's skipped.
// If checksum differs or status is 'failed', it's reset to 'pending'.
// workDir is used to resolve relative paths for checksum computation (pass "" if abs).
func EnqueueFile(db *sql.DB, path, workDir string) error {
	// Normalize to forward slashes for cross-platform consistency
	path = filepath.ToSlash(path)

	// Resolve path for checksum computation
	absPath := path
	if !filepath.IsAbs(path) && workDir != "" {
		absPath = filepath.Join(workDir, path)
	}

	// Compute checksum (best-effort: if file doesn't exist, cs stays "")
	var cs string
	if data, err := os.ReadFile(absPath); err == nil {
		h := sha256.Sum256(data)
		cs = fmt.Sprintf("%x", h)
	}

	// Check existing record
	var existingChecksum string
	err := db.QueryRow(`SELECT checksum FROM files WHERE path = ?`, path).Scan(&existingChecksum)
	if err == nil {
		// File exists in table — skip if content unchanged
		if cs != "" && cs == existingChecksum {
			return nil // content unchanged, no re-index needed
		}
		// Checksum changed → re-index
		_, err = db.Exec(`UPDATE files SET checksum=?, status='pending', retry_count=0, updated_at=datetime('now','localtime') WHERE path=?`, cs, path)
		return err
	}

	// New file
	_, err = db.Exec(`INSERT INTO files (path, checksum, status) VALUES (?, ?, 'pending')`, path, cs)
	return err
}

// MaxRetriesPerFile is the maximum number of LLM indexing attempts per file before giving up.
const MaxRetriesPerFile = 3

// DequeueBatch picks up to limit pending/failed items and marks them as indexing (transactional).
// Failed items with retry_count >= MaxRetriesPerFile are NOT fetched.
func DequeueBatch(db *sql.DB, limit int) ([]QueueItem, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT path, checksum, status, retry_count, updated_at
		FROM files WHERE status='pending' OR (status='failed' AND retry_count < ?)
		ORDER BY CASE WHEN status='pending' THEN 0 ELSE 1 END, path LIMIT ?`, MaxRetriesPerFile, limit)
	if err != nil {
		return nil, err
	}

	var items []QueueItem
	for rows.Next() {
		var qi QueueItem
		if err := rows.Scan(&qi.FilePath, &qi.Checksum, &qi.Status, &qi.RetryCount, &qi.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, qi)
	}
	rows.Close()

	if len(items) == 0 {
		return nil, nil
	}

	// Mark all as indexing
	for _, qi := range items {
		if _, err := tx.Exec(`UPDATE files SET status='indexing', updated_at=datetime('now','localtime') WHERE path=?`, qi.FilePath); err != nil {
			return nil, err
		}
	}

	return items, tx.Commit()
}

// ClearQueue resets all pending/indexing entries back to 'done'.
func ClearQueue(db *sql.DB) error {
	_, err := db.Exec(`UPDATE files SET status='done' WHERE status IN ('pending','indexing')`)
	return err
}

// ClearAll removes all index data and queue entries.
func ClearAll(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM symbols`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM files`)
	return err
}

// MarkQueueDone marks a queue item as done.
func MarkQueueDone(db *sql.DB, filePath string) error {
	_, err := db.Exec(`UPDATE files SET status='done', updated_at=datetime('now','localtime') WHERE path=?`, filePath)
	return err
}

// MarkQueueFailed marks a queue item as failed.
func MarkQueueFailed(db *sql.DB, filePath string) error {
	_, err := db.Exec(`UPDATE files SET status='failed', retry_count=retry_count+1, updated_at=datetime('now','localtime') WHERE path=?`, filePath)
	return err
}

// QueueCounts returns the number of items per status.
func QueueCounts(db *sql.DB) (pending, indexing, failed, failedExhausted int) {
	db.QueryRow(`SELECT COUNT(*) FROM files WHERE status='pending'`).Scan(&pending)
	db.QueryRow(`SELECT COUNT(*) FROM files WHERE status='indexing'`).Scan(&indexing)
	db.QueryRow(`SELECT COUNT(*) FROM files WHERE status='failed' AND retry_count < ?`, MaxRetriesPerFile).Scan(&failed)
	db.QueryRow(`SELECT COUNT(*) FROM files WHERE status='failed' AND retry_count >= ?`, MaxRetriesPerFile).Scan(&failedExhausted)
	return
}

// ResetFailedItems resets all failed items back to pending for retry.
func ResetFailedItems(db *sql.DB) error {
	_, err := db.Exec(`UPDATE files SET status='pending', retry_count=0, updated_at=datetime('now','localtime') WHERE status='failed'`)
	return err
}

// ──────── Staleness ────────

// GetStaleFileInfo decorates a FileIndex with queue status and staleness info.
// currentChecksum is the file's current on-disk content hash.
func GetStaleFileInfo(db *sql.DB, path string, currentChecksum string) (*StaleFileInfo, error) {
	fi, err := GetFileIndex(db, path)
	if err != nil {
		return nil, err
	}

	info := &StaleFileInfo{}
	if fi != nil {
		info.FileIndex = *fi
	}

	// Check queue status
	var qStatus string
	err = db.QueryRow(`SELECT status FROM files WHERE path = ?`, path).Scan(&qStatus)
	if err == nil && qStatus != "" && qStatus != "done" {
		info.IsQueued = true
		info.QueueState = qStatus
		info.IsStale = true
	} else if fi != nil {
		// Compare checksums
		storedCS, _ := GetFileChecksum(db, path)
		info.IsStale = storedCS != currentChecksum
	}

	return info, nil
}

// ──────── Search / Overview ────────

// SearchFiles searches file summaries by keywords (simple LIKE-based).
func SearchFiles(db *sql.DB, keywords string) ([]FileIndex, error) {
	like := "%" + keywords + "%"
	rows, err := db.Query(`SELECT path, language, summary FROM files WHERE summary LIKE ? OR path LIKE ? LIMIT 30`, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FileIndex
	for rows.Next() {
		var fi FileIndex
		if err := rows.Scan(&fi.Path, &fi.Language, &fi.Summary); err != nil {
			return nil, err
		}
		results = append(results, fi)
	}
	return results, nil
}

// FindSymbol searches for a symbol by name across all files.
func FindSymbol(db *sql.DB, name string) ([]SymbolIndex, error) {
	rows, err := db.Query(`SELECT s.kind, s.name, s.exported, s.signature, s.doc_summary, s.file_path
		FROM symbols s WHERE s.name LIKE ? ORDER BY s.file_path`, "%"+name+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SymbolIndex
	for rows.Next() {
		var s SymbolIndex
		var path string
		if err := rows.Scan(&s.Kind, &s.Name, &s.Exported, &s.Signature, &s.DocSummary, &path); err != nil {
			return nil, err
		}
		s.Signature = fmt.Sprintf("%s (%s)", s.Signature, path)
		results = append(results, s)
	}
	return results, nil
}

// GetOverview returns a high-level overview of the index.
func GetOverview(db *sql.DB) (map[string]interface{}, error) {
	var fileCount, symbolCount int
	db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&fileCount)
	db.QueryRow(`SELECT COUNT(*) FROM symbols`).Scan(&symbolCount)

	pending, indexing, failed, failedExhausted := QueueCounts(db)

	var langsJSON string
	db.QueryRow(`SELECT COALESCE(json_group_array(DISTINCT language), '[]') FROM files WHERE language != ''`).Scan(&langsJSON)

	var lastUpdated string
	db.QueryRow(`SELECT COALESCE(MAX(updated_at), '') FROM files`).Scan(&lastUpdated)

	return map[string]interface{}{
		"files":            fileCount,
		"symbols":          symbolCount,
		"languages":        langsJSON,
		"queue_pending":    pending,
		"queue_indexing":   indexing,
		"queue_failed":     failed,
		"queue_failed_exhausted": failedExhausted,
		"last_updated":     lastUpdated,
	}, nil
}

// ListQueuedPaths returns all file paths currently in the queue (pending or indexing).
func ListQueuedPaths(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT path FROM files WHERE status IN ('pending','indexing')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		paths = append(paths, p)
	}
	return paths, nil
}
