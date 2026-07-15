package config

import (
	"database/sql"
	"sync"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"go.uber.org/zap"
)

// ConfigManager manages application configuration.
type ConfigManager struct {
	workDir string
	logger  *zap.Logger
	cache   map[string]string
	mu      sync.RWMutex
}

// NewConfigManager creates a new ConfigManager.
func NewConfigManager(workDir string, logger *zap.Logger) *ConfigManager {
	return &ConfigManager{
		workDir: workDir,
		logger:  logger,
		cache:   make(map[string]string),
	}
}

// Load loads all config from database into cache.
// It ensures the config table exists before querying.
func (cm *ConfigManager) Load() error {
	return db.WithDB(cm.workDir, func(sqlDB *sql.DB) error {
		// Ensure config table exists
		if err := db.RunMigrations(sqlDB); err != nil {
			cm.logger.Warn("Failed to run migrations during config load", zap.Error(err))
		}
		configs, err := db.GetAllConfig(sqlDB)
		if err != nil {
			return err
		}
		cm.mu.Lock()
		cm.cache = configs
		cm.mu.Unlock()
		return nil
	})
}

// Get returns a config value by key.
func (cm *ConfigManager) Get(key string) (string, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	val, ok := cm.cache[key]
	return val, ok
}

// Set updates a config value in DB and cache.
func (cm *ConfigManager) Set(key, value string) error {
	err := db.WithDB(cm.workDir, func(sqlDB *sql.DB) error {
		return db.SetConfig(sqlDB, key, value)
	})
	if err != nil {
		return err
	}
	cm.mu.Lock()
	cm.cache[key] = value
	cm.mu.Unlock()
	return nil
}
