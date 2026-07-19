package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// GetConfig retrieves a configuration value by key.
func GetConfig(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil // Not found is not an error
	}
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}
	return value, nil
}

// SetConfig upserts a config value.
func SetConfig(db *sql.DB, key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO config (key, value, created_at, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = ?`,
		key, value, now, now, value, now,
	)
	if err != nil {
		return fmt.Errorf("failed to set config: %w", err)
	}
	return nil
}

// GetAllConfig returns all config entries as a map.
func GetAllConfig(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT key, value FROM config`)
	if err != nil {
		return nil, fmt.Errorf("failed to get all config: %w", err)
	}
	defer rows.Close()

	configs := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan config: %w", err)
		}
		configs[key] = value
	}
	return configs, rows.Err()
}

// SaveRule upserts a rule record.
func SaveRule(db *sql.DB, rule *models.Rule) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if rule.CreatedAt == "" {
		rule.CreatedAt = now
	}
	_, err := db.Exec(
		`INSERT INTO rules (name, category, content, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(name) DO UPDATE SET category = ?, content = ?, updated_at = ?`,
		rule.Name, rule.Category, rule.Content, rule.CreatedAt, now,
		rule.Category, rule.Content, now,
	)
	if err != nil {
		return fmt.Errorf("failed to save rule: %w", err)
	}
	return nil
}

// GetRule retrieves a rule by name.
func GetRule(db *sql.DB, name string) (*models.Rule, error) {
	r := &models.Rule{}
	err := db.QueryRow(
		`SELECT name, category, content, created_at, updated_at FROM rules WHERE name = ?`,
		name,
	).Scan(&r.Name, &r.Category, &r.Content, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("rule not found: %s", name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get rule: %w", err)
	}
	return r, nil
}

// GetRulesByCategory returns all rules in a category.
func GetRulesByCategory(db *sql.DB, category string) ([]*models.Rule, error) {
	rows, err := db.Query(
		`SELECT name, category, content, created_at, updated_at FROM rules WHERE category = ? ORDER BY name`,
		category,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get rules by category: %w", err)
	}
	return scanAll(rows, func(r *models.Rule) []any {
		return []any{&r.Name, &r.Category, &r.Content, &r.CreatedAt, &r.UpdatedAt}
	})
}

// DeleteRule deletes a rule by name.
func DeleteRule(db *sql.DB, name string) error {
	res, err := db.Exec(`DELETE FROM rules WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("failed to delete rule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("rule not found: %s", name)
	}
	return nil
}

// OpenRaw opens an existing SQLite database file without running migrations.
// Useful for read-only inspection.
func OpenRaw(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set pragma: %w", err)
	}
	return db, nil
}

// CloseRaw closes a raw database connection.
func CloseRaw(db *sql.DB) error {
	if db != nil {
		return db.Close()
	}
	return nil
}

// ListConfigKeys returns all keys from the config table.
func ListConfigKeys(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT key FROM config ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("failed to list config keys: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("failed to scan config key: %w", err)
		}
		keys = append(keys, key)
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, rows.Err()
}

// ListConfigByPrefix returns key-value pairs from config where key starts with prefix.
func ListConfigByPrefix(db *sql.DB, prefix string) (map[string]string, error) {
	rows, err := db.Query(`SELECT key, value FROM config WHERE key LIKE ? || '%' ORDER BY key`, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to query config by prefix: %w", err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan config row: %w", err)
		}
		result[key] = value
	}
	return result, rows.Err()
}

// GetConfigInt reads an integer config value with a default fallback.
func GetConfigInt(db *sql.DB, key string, defaultVal int) int {
	val, err := GetConfig(db, key)
	if err != nil || val == "" {
		return defaultVal
	}
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
		return defaultVal
	}
	return n
}

// DeleteConfig removes a config entry by key.
func DeleteConfig(db *sql.DB, key string) error {
	_, err := db.Exec(`DELETE FROM config WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("failed to delete config %s: %w", key, err)
	}
	return nil
}

// DeleteConfigLike removes config entries where key matches a LIKE pattern.
// Use '%' as wildcard, e.g. "compress_lock:%" deletes all compress_lock keys.
func DeleteConfigLike(db *sql.DB, pattern string) error {
	_, err := db.Exec(`DELETE FROM config WHERE key LIKE ?`, pattern)
	if err != nil {
		return fmt.Errorf("failed to delete config by pattern %s: %w", pattern, err)
	}
	return nil
}
