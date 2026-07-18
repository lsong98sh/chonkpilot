package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/chonkpilot/chonkpilot/internal/config"
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
// Uses EnsureUserConfig (internal/config) for initial load to avoid redundant disk I/O
// and ensure default values (Chrome path, timeouts) are applied.
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
	// Load via internal/config.EnsureUserConfig — reads once, applies defaults (Chrome, timeouts),
	// and writes back if needed. Falls back to direct Load if the init path fails.
	if initCfg, initErr := config.EnsureUserConfig(); initErr == nil && initCfg != nil {
		uc.cfg = initCfg
	} else {
		if loadErr := uc.Load(); loadErr != nil {
			uc.log.Warn("Failed to load user config, using defaults", zap.Error(loadErr))
		}
	}
	return uc, nil
}

// Load reads the user config from disk and applies default values for missing fields.
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
	// Apply safe defaults for missing fields (consistent with internal/config/init.go)
	if cfg.MaxToolIterations == 0 {
		cfg.MaxToolIterations = 800
	}
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 180
	}
	if cfg.StreamTimeout == 0 {
		cfg.StreamTimeout = 180
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

// saveUnsafe writes config without locking (caller must hold lock).
func (uc *UserConfigManager) saveUnsafe() error {
	data, err := json.MarshalIndent(uc.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(uc.path, data, 0644)
}
