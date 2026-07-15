package recent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Manager manages recent directories using file-based shortcuts with locking.
type Manager struct {
	dir string
}

// NewManager creates a new recent manager.
func NewManager(userHome string) *Manager {
	return &Manager{
		dir: filepath.Join(userHome, ".chonkpilot", "recent"),
	}
}

// Init ensures the recent directory exists.
func (m *Manager) Init() error {
	return os.MkdirAll(m.dir, 0755)
}

// shortcutPath returns the path for a workDir shortcut.
func (m *Manager) shortcutPath(workDir string) string {
	hash := sha256.Sum256([]byte(workDir))
	name := hex.EncodeToString(hash[:8]) + ".txt"
	return filepath.Join(m.dir, name)
}

// LockAndCreate creates/updates a shortcut for workDir and locks it.
// Returns the file handle which must be kept open to maintain the lock.
func (m *Manager) LockAndCreate(workDir string) (*os.File, error) {
	path := m.shortcutPath(workDir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}
	if _, err := f.WriteString(workDir); err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		unlockFile(f)
		f.Close()
		return nil, err
	}
	return f, nil
}

// ReleaseAndTouch unlocks the file and updates its modification time.
func (m *Manager) ReleaseAndTouch(f *os.File) error {
	defer f.Close()
	if err := unlockFile(f); err != nil {
		return err
	}
	now := time.Now()
	return os.Chtimes(f.Name(), now, now)
}

// GetRecentDirs returns recent directories, excluding locked (active) ones.
// Also removes shortcuts pointing to deleted directories.
func (m *Manager) GetRecentDirs(max int) ([]string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	type item struct {
		path string
		time time.Time
	}
	var items []item

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		path := filepath.Join(m.dir, entry.Name())

		// Check if locked (another instance is running)
		if isLocked(path) {
			continue
		}

		// Read content
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		workDir := strings.TrimSpace(string(data))
		if workDir == "" {
			continue
		}

		// Check if directory still exists
		if _, err := os.Stat(workDir); os.IsNotExist(err) {
			os.Remove(path)
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, item{path: workDir, time: info.ModTime()})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].time.After(items[j].time)
	})

	var result []string
	for i, item := range items {
		if i >= max {
			break
		}
		result = append(result, item.path)
	}
	return result, nil
}

// GetDefaultWorkDir returns the most recent unlocked directory.
func (m *Manager) GetDefaultWorkDir() (string, error) {
	dirs, err := m.GetRecentDirs(1)
	if err != nil {
		return "", err
	}
	if len(dirs) == 0 {
		return "", fmt.Errorf("no recent directories")
	}
	return dirs[0], nil
}
