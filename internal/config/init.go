// Package config provides shared configuration initialization for both IDE and executor.
package config

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"sync"

	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/chrome"
)

// execOnce ensures EnsureUserConfig is cached within a single executor process.
// Each executor process handles one conversation turn, so caching is safe:
// UI changes are picked up by the next executor process (fresh start).
var (
	execCfg    *models.UserConfig
	execCfgErr error
	execOnce   sync.Once
)

const (
	defaultMaxToolIterations = 800
	defaultResponseTimeout   = 180 // seconds
	defaultStreamTimeout     = 180 // seconds
)

// UserConfigPath returns the path to ~/.chonkpilot/config.json.
func UserConfigPath() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(usr.HomeDir, ".chonkpilot", "config.json"), nil
}

// EnsureUserConfig reads the global config, fills in defaults for Chrome path
// and execution parameters, and writes back if anything changed.
//
// Within an executor process the result is cached (sync.Once) — redundant calls
// within the same process return the cached value without re-reading disk.
// IDE side calls this once at startup and does not share the cache.
func EnsureUserConfig() (*models.UserConfig, error) {
	execOnce.Do(func() {
		execCfg, execCfgErr = ensureUserConfigUncached()
	})
	return execCfg, execCfgErr
}

// ensureUserConfigUncached reads config from disk, applies defaults, and writes back if changed.
func ensureUserConfigUncached() (*models.UserConfig, error) {
	path, err := UserConfigPath()
	if err != nil {
		return nil, err
	}

	cfg := &models.UserConfig{}
	changed := false

	// Read existing config if available
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, cfg); err == nil {
			// Successfully loaded existing config
		}
	}

	// 1. Chrome path: verify cached, or auto-discover
	if cfg.ChromePath != "" {
		if !chrome.Verify(cfg.ChromePath) {
			cfg.ChromePath = ""
			changed = true
		}
	}
	if cfg.ChromePath == "" {
		result := chrome.Discover()
		if result.Ok {
			cfg.ChromePath = result.Path
			changed = true
		}
	}

	// 2. Execution parameter defaults
	if cfg.MaxToolIterations == 0 {
		cfg.MaxToolIterations = defaultMaxToolIterations
		changed = true
	}
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = defaultResponseTimeout
		changed = true
	}
	if cfg.StreamTimeout == 0 {
		cfg.StreamTimeout = defaultStreamTimeout
		changed = true
	}
	if cfg.CodeIndexTemperature == 0 {
		cfg.CodeIndexTemperature = 0.1
		changed = true
	}
	if cfg.ToolMaxDepth == 0 {
		cfg.ToolMaxDepth = 5
		changed = true
	}
	if cfg.TaskPollIntervalMs == 0 {
		cfg.TaskPollIntervalMs = 200
		changed = true
	}
	if cfg.SearchMaxResults == 0 {
		cfg.SearchMaxResults = 200
		changed = true
	}
	if cfg.FetchMaxBodySizeKB == 0 {
		cfg.FetchMaxBodySizeKB = 100
		changed = true
	}
	if cfg.BrowserWindowWidth == 0 {
		cfg.BrowserWindowWidth = 1280
		changed = true
	}
	if cfg.BrowserWindowHeight == 0 {
		cfg.BrowserWindowHeight = 800
		changed = true
	}
	if cfg.BrowserLogCap == 0 {
		cfg.BrowserLogCap = 500
		changed = true
	}
	if cfg.LLMTLSHandshakeTimeout == 0 {
		cfg.LLMTLSHandshakeTimeout = 30
		changed = true
	}

	// 3. Ensure LLM slice is non-nil for JSON serialization
	if cfg.LLMs == nil {
		cfg.LLMs = []models.LLMProvider{}
	}

	// Write back if anything changed
	if changed {
		if err := writeConfig(path, cfg); err != nil {
			return cfg, err
		}
	}

	return cfg, nil
}

// writeConfig writes the config to disk with standard permissions.
func writeConfig(path string, cfg *models.UserConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
