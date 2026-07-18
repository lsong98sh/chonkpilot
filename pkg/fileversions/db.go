package fileversions

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// FileUID uniquely identifies a file across renames using inode (Unix) / FileIndex (Windows).
type FileUID string

// VersionRecord is a single file version snapshot.
type VersionRecord struct {
	ID        int64  `json:"id"`
	TurnID    string `json:"turn_id"`
	FileUID   string `json:"file_uid"`
	FilePath  string `json:"file_path"`
	CreatedAt string `json:"created_at"`
}

// VersionContent holds version metadata plus the file content.
type VersionContent struct {
	VersionRecord
	Content []byte `json:"content"`
}

// ──────── DB ────────

// OpenDB opens (or creates) the versions.db in the project's .ide directory.
func OpenDB(workDir string) (*sql.DB, error) {
	dir := filepath.Join(workDir, ".ide")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create .ide dir: %w", err)
	}
	dbPath := filepath.Join(dir, "history.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open versions.db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate versions.db: %w", err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS file_versions (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		turn_id     TEXT NOT NULL,
		file_uid    TEXT NOT NULL,
		file_path   TEXT NOT NULL DEFAULT '',
		content     BLOB NOT NULL,
		created_at  TEXT NOT NULL
	)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_fv_uid ON file_versions(file_uid)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_fv_path ON file_versions(file_path)`)
	return err
}

// ──────── Snapshot ────────

// Snapshot saves a version of a file IF not already saved in this turn for this file_uid.
//	- filePath is relative to workDir
//	- turnID is the current turn UUID
//	- WorkDir is the project root
// Returns true if a new version was saved.
func Snapshot(db *sql.DB, turnID, filePath, workDir string) (bool, error) {
	// Normalize to forward slashes for cross-platform DB consistency
	filePath = filepath.ToSlash(filePath)

	// Resolve absolute path for reading and UID computation
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(workDir, filePath)
	}

	uid := ComputeFileUID(absPath)

	// Check if this turn already has a snapshot for this uid
	var exists int
	err := db.QueryRow(`SELECT COUNT(*) FROM file_versions WHERE turn_id = ? AND file_uid = ?`, turnID, string(uid)).Scan(&exists)
	if err != nil {
		return false, err
	}
	if exists > 0 {
		// Already snapshotted in this turn for this uid — skip
		return false, nil
	}

	// Read current file content
	data, err := os.ReadFile(absPath)
	if err != nil {
		// File doesn't exist yet (first time create) — no snapshot needed
		return false, nil
	}

	now := time.Now().Format(time.RFC3339)
	_, err = db.Exec(`INSERT INTO file_versions (turn_id, file_uid, file_path, content, created_at) VALUES (?, ?, ?, ?, ?)`,
		turnID, string(uid), filePath, data, now)
	if err != nil {
		return false, err
	}
	return true, nil
}

// ──────── Query ────────

// GetVersions returns all version metadata for a file, ordered by id.
// Uses file_uid first, then file_path as fallback.
// Updates file_path and file_uid on the fly for consistency.
func GetVersions(db *sql.DB, filePath, workDir string) ([]VersionRecord, error) {
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(workDir, filePath)
	}
	uid := string(ComputeFileUID(absPath))

	// 1. Try by UID
	rows, err := db.Query(`SELECT id, turn_id, file_uid, file_path, created_at FROM file_versions WHERE file_uid = ? ORDER BY id`, uid)
	if err != nil {
		return nil, err
	}
	results, scanErr := scanVersions(rows)
	if scanErr != nil {
		return nil, scanErr
	}
	if len(results) > 0 {
		// Update file_path to current path if changed
		_, _ = db.Exec(`UPDATE file_versions SET file_path = ? WHERE file_uid = ? AND file_path != ?`, filePath, uid, filePath)
		return results, nil
	}

	// 2. Fallback by file_path
	rows, err = db.Query(`SELECT id, turn_id, file_uid, file_path, created_at FROM file_versions WHERE file_path = ? ORDER BY id`, filePath)
	if err != nil {
		return nil, err
	}
	results, scanErr = scanVersions(rows)
	if scanErr != nil {
		return nil, scanErr
	}
	if len(results) > 0 {
		// Update UID for future lookups
		_, _ = db.Exec(`UPDATE file_versions SET file_uid = ? WHERE file_path = ? AND file_uid != ?`, uid, filePath, uid)
	}
	return results, nil
}

func scanVersions(rows *sql.Rows) ([]VersionRecord, error) {
	defer rows.Close()
	var results []VersionRecord
	for rows.Next() {
		var vr VersionRecord
		if err := rows.Scan(&vr.ID, &vr.TurnID, &vr.FileUID, &vr.FilePath, &vr.CreatedAt); err != nil {
			continue
		}
		results = append(results, vr)
	}
	return results, rows.Err()
}

// GetVersionContent retrieves full version data by ID.
func GetVersionContent(db *sql.DB, versionID int64) (*VersionContent, error) {
	row := db.QueryRow(`SELECT id, turn_id, file_uid, file_path, content, created_at FROM file_versions WHERE id = ?`, versionID)
	var vc VersionContent
	if err := row.Scan(&vc.ID, &vc.TurnID, &vc.FileUID, &vc.FilePath, &vc.Content, &vc.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &vc, nil
}

// RestoreVersion replaces the current file content with a version's content.
func RestoreVersion(db *sql.DB, versionID int64, workDir string) error {
	vc, err := GetVersionContent(db, versionID)
	if err != nil {
		return err
	}
	if vc == nil {
		return fmt.Errorf("version %d not found", versionID)
	}

	absPath := vc.FilePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(workDir, absPath)
	}
	return os.WriteFile(absPath, vc.Content, 0644)
}

// ──────── Turn-level operations ────────

// GetTurnVersions returns all version records for a given turn.
func GetTurnVersions(db *sql.DB, turnID string) ([]VersionRecord, error) {
	rows, err := db.Query(`SELECT id, turn_id, file_uid, file_path, created_at FROM file_versions WHERE turn_id = ? ORDER BY id`, turnID)
	if err != nil {
		return nil, err
	}
	return scanVersions(rows)
}

// DeleteByTurnID removes all version records for a given turn.
func DeleteByTurnID(db *sql.DB, turnID string) error {
	_, err := db.Exec(`DELETE FROM file_versions WHERE turn_id = ?`, turnID)
	return err
}

// GetTurnVersionsByPathPrefix returns all version records for a turn where file_path starts with the given prefix.
func GetTurnVersionsByPathPrefix(db *sql.DB, turnID, pathPrefix string) ([]VersionRecord, error) {
	rows, err := db.Query(`SELECT id, turn_id, file_uid, file_path, created_at FROM file_versions WHERE turn_id = ? AND file_path LIKE ? ORDER BY id`, turnID, pathPrefix+"%")
	if err != nil {
		return nil, err
	}
	return scanVersions(rows)
}

// DeleteByFile removes version records for a specific file in a turn.
// Uses file_uid (computed from path + workDir) to match records.
func DeleteByFile(db *sql.DB, turnID, filePath, workDir string) error {
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(workDir, filePath)
	}
	uid := string(ComputeFileUID(absPath))

	result, err := db.Exec(`DELETE FROM file_versions WHERE turn_id = ? AND file_uid = ?`, turnID, uid)
	if err != nil {
		return err
	}
	// Also delete by file_path as fallback
	_, _ = db.Exec(`DELETE FROM file_versions WHERE turn_id = ? AND file_path = ?`, turnID, filePath)

	n, _ := result.RowsAffected()
	if n == 0 {
		// Try file_path fallback
		_, _ = db.Exec(`DELETE FROM file_versions WHERE turn_id = ? AND file_path = ?`, turnID, filePath)
	}
	return nil
}

// RestoreFileAndDelete restores a single file to its snapshot content,
// then deletes the version records for this file in the turn.
// Returns the number of files restored (0 = no snapshot found, no error).
func RestoreFileAndDelete(db *sql.DB, turnID, filePath, workDir string) (int, error) {
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(workDir, filePath)
	}
	uid := string(ComputeFileUID(absPath))

	// Find the first snapshot for this file in this turn
	row := db.QueryRow(`SELECT id, content FROM file_versions WHERE turn_id = ? AND file_uid = ? ORDER BY id LIMIT 1`, turnID, uid)
	var versionID int64
	var content []byte
	if err := row.Scan(&versionID, &content); err != nil {
		if err == sql.ErrNoRows {
			// Fallback: try by file_path
			row = db.QueryRow(`SELECT id, content FROM file_versions WHERE turn_id = ? AND file_path = ? ORDER BY id LIMIT 1`, turnID, filePath)
			if err := row.Scan(&versionID, &content); err != nil {
				if err == sql.ErrNoRows {
					return 0, nil // no snapshot → nothing to restore
				}
				return 0, err
			}
		} else {
			return 0, err
		}
	}

	// Write snapshot content back to disk (create parent dirs if needed)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return 0, fmt.Errorf("create parent dir for restore: %w", err)
	}
	if err := os.WriteFile(absPath, content, 0644); err != nil {
		return 0, fmt.Errorf("write restored file: %w", err)
	}

	// Delete version records for this file in this turn
	_, _ = db.Exec(`DELETE FROM file_versions WHERE turn_id = ? AND file_uid = ?`, turnID, uid)
	_, _ = db.Exec(`DELETE FROM file_versions WHERE turn_id = ? AND file_path = ?`, turnID, filePath)

	return 1, nil
}

// RestoreTurnAndDelete restores all files in a turn, then deletes all records.
// Files without a snapshot are silently skipped.
func RestoreTurnAndDelete(db *sql.DB, turnID, workDir string) (int, []error) {
	versions, err := GetTurnVersions(db, turnID)
	if err != nil {
		return 0, []error{fmt.Errorf("query turn versions: %w", err)}
	}

	if len(versions) == 0 {
		return 0, nil
	}

	var restored int
	var errs []error
	seen := make(map[string]bool) // track unique file_uids

	for _, v := range versions {
		if seen[v.FileUID] {
			continue
		}
		seen[v.FileUID] = true

		absPath := v.FilePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(workDir, absPath)
		}

		vc, err := GetVersionContent(db, v.ID)
		if err != nil || vc == nil {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			errs = append(errs, fmt.Errorf("create parent dir for restore %s: %w", v.FilePath, err))
			continue
		}
		if err := os.WriteFile(absPath, vc.Content, 0644); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", v.FilePath, err))
			continue
		}
		restored++
	}

	// Delete all records for this turn
	if delErr := DeleteByTurnID(db, turnID); delErr != nil {
		errs = append(errs, fmt.Errorf("delete turn versions: %w", delErr))
	}

	return restored, errs
}
