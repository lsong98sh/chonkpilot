package fileversions

import (
	"database/sql"
	"sync"
)

// Versioner manages file version snapshots in versions.db.
type Versioner struct {
	db      *sql.DB
	workDir string
	mu      sync.Mutex
}

// NewVersioner opens or creates versions.db and returns a Versioner.
func NewVersioner(workDir string) (*Versioner, error) {
	db, err := OpenDB(workDir)
	if err != nil {
		return nil, err
	}
	return &Versioner{db: db, workDir: workDir}, nil
}

// Snapshot saves a version of a file if not already saved in this turn for this file_uid.
// Returns true if a new version was saved.
func (v *Versioner) Snapshot(turnID, filePath string) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Snapshot(v.db, turnID, filePath, v.workDir)
}

// GetVersions returns all version metadata for a file.
func (v *Versioner) GetVersions(filePath string) ([]VersionRecord, error) {
	return GetVersions(v.db, filePath, v.workDir)
}

// GetVersionContent retrieves full version data by ID.
func (v *Versioner) GetVersionContent(versionID int64) (*VersionContent, error) {
	return GetVersionContent(v.db, versionID)
}

// RestoreVersion replaces the current file content with a version's content.
func (v *Versioner) RestoreVersion(versionID int64) error {
	return RestoreVersion(v.db, versionID, v.workDir)
}

// RestoreFileAndDelete restores a single file and deletes its version records for this turn.
func (v *Versioner) RestoreFileAndDelete(turnID, filePath string) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return RestoreFileAndDelete(v.db, turnID, filePath, v.workDir)
}

// RestoreTurnAndDelete restores all files in a turn and deletes all records.
func (v *Versioner) RestoreTurnAndDelete(turnID string) (int, []error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return RestoreTurnAndDelete(v.db, turnID, v.workDir)
}

// GetTurnVersionsByPathPrefix returns all version records for a turn where file_path starts with pathPrefix.
func (v *Versioner) GetTurnVersionsByPathPrefix(turnID, pathPrefix string) ([]VersionRecord, error) {
	return GetTurnVersionsByPathPrefix(v.db, turnID, pathPrefix)
}

// Close closes the underlying database.
func (v *Versioner) Close() error {
	return v.db.Close()
}

// Put saves a versioned snapshot of a file (explicit history_put).
// Returns the auto-incremented version number.
func (v *Versioner) Put(turnID, filePath string) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return PutVersion(v.db, turnID, filePath, v.workDir)
}

// Take retrieves version content by turn_id and version number.
func (v *Versioner) Take(turnID string, version int) (*VersionContent, error) {
	return GetVersionByTurnAndVersion(v.db, turnID, version)
}

// List returns all versions, optionally filtered by turn_id.
// If turnID is empty, returns all turns.
func (v *Versioner) List(turnID string) (map[string][]VersionRecord, error) {
	if turnID != "" {
		versions, err := GetTurnVersions(v.db, turnID)
		if err != nil {
			return nil, err
		}
		return map[string][]VersionRecord{turnID: versions}, nil
	}

	turns, err := GetAllTurns(v.db)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]VersionRecord, len(turns))
	for _, t := range turns {
		versions, err := GetTurnVersions(v.db, t)
		if err != nil {
			continue
		}
		result[t] = versions
	}
	return result, nil
}
