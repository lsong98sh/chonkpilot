package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/chonkpilot/chonkpilot/internal/models"
	"go.uber.org/zap"
)

const userConfigDirName = ".chonkpilot"
const userConfigFileName = "config.json"

// UserConfigManager manages user-level configuration stored at %USERPROFILE%\.chonkpilot\config.json.
// This config persists across different workspace directories.
type UserConfigManager struct {
	cfg  *models.UserConfig
	path string
	mu   sync.RWMutex
	log  *zap.Logger
}

// NewUserConfigManager creates a UserConfigManager that stores config under the user's home directory.
func NewUserConfigManager(logger *zap.Logger) (*UserConfigManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, userConfigDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, userConfigFileName)
	uc := &UserConfigManager{
		cfg: &models.UserConfig{
			LLMs:  []models.LLMProvider{},
			Theme: "light",
		},
		path: path,
		log:  logger,
	}
	if err := uc.Load(); err != nil {
		uc.log.Warn("Failed to load user config, using defaults", zap.Error(err))
	}
	return uc, nil
}

// Load reads the user config from disk.
func (uc *UserConfigManager) Load() error {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	data, err := os.ReadFile(uc.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // First run, use defaults
		}
		return err
	}
	var cfg models.UserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	// Ensure slices are not nil
	if cfg.LLMs == nil {
		cfg.LLMs = []models.LLMProvider{}
	}
	uc.cfg = &cfg
	return nil
}

// Save writes the user config to disk.
func (uc *UserConfigManager) Save() error {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	data, err := json.MarshalIndent(uc.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(uc.path, data, 0644)
}

// Get returns a copy of the entire user config.
func (uc *UserConfigManager) Get() *models.UserConfig {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	cp := *uc.cfg
	cp.LLMs = append([]models.LLMProvider{}, uc.cfg.LLMs...)
	return &cp
}

// Update replaces the entire user config and persists.
func (uc *UserConfigManager) Update(cfg *models.UserConfig) error {
	uc.mu.Lock()
	if cfg.LLMs == nil {
		cfg.LLMs = []models.LLMProvider{}
	}
	uc.cfg = cfg
	uc.mu.Unlock()
	return uc.Save()
}

// SetTheme updates the theme and persists.
func (uc *UserConfigManager) SetTheme(theme string) error {
	uc.mu.Lock()
	uc.cfg.Theme = theme
	uc.mu.Unlock()
	return uc.Save()
}

// SetLastWorkDir updates the last used work directory and persists.
func (uc *UserConfigManager) SetLastWorkDir(dir string) error {
	uc.mu.Lock()
	uc.cfg.LastWorkDir = dir
	uc.mu.Unlock()
	return uc.Save()
}

// GetLastWorkDir returns the last used work directory.
func (uc *UserConfigManager) GetLastWorkDir() string {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	return uc.cfg.LastWorkDir
}

// saveUnsafe writes config without locking (caller must hold lock).
func (uc *UserConfigManager) saveUnsafe() error {
	data, err := json.MarshalIndent(uc.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(uc.path, data, 0644)
}
