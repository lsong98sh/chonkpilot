package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/models"
)

// ─── Project Agents ──────────────────────────────────────────

// GetProjectAgents returns all project agent configurations.
func GetProjectAgents(db *sql.DB) ([]models.AgentConfig, error) {
	rows, err := db.Query(`SELECT id, title, use_case, prompt, source, created_at, updated_at FROM project_agents ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query project_agents: %w", err)
	}
	defer rows.Close()

	var agents []models.AgentConfig
	for rows.Next() {
		var a models.AgentConfig
		if err := rows.Scan(&a.ID, &a.Title, &a.UseCase, &a.Prompt, &a.Source, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan project_agents: %w", err)
		}
		agents = append(agents, a)
	}
	if agents == nil {
		agents = []models.AgentConfig{}
	}
	return agents, rows.Err()
}

// SaveProjectAgents replaces all project agent configurations.
func SaveProjectAgents(db *sql.DB, agents []models.AgentConfig) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin tx for project_agents: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM project_agents`); err != nil {
		return fmt.Errorf("failed to clear project_agents: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.Prepare(`INSERT INTO project_agents (title, use_case, prompt, source, created_at, updated_at) VALUES (?, ?, ?, NULLIF(?,''), ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert project_agents: %w", err)
	}
	defer stmt.Close()

	for _, a := range agents {
		if _, err := stmt.Exec(a.Title, a.UseCase, a.Prompt, a.Source, now, now); err != nil {
			return fmt.Errorf("failed to insert project_agent: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateProjectAgent updates a single project agent identified by id.
func UpdateProjectAgent(db *sql.DB, agent models.AgentConfig, id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`UPDATE project_agents SET title=?, use_case=?, prompt=?, source=NULLIF(?,''), updated_at=? WHERE id=?`,
		agent.Title, agent.UseCase, agent.Prompt, agent.Source, now, id,
	)
	return err
}

// GetProjectAgentByID returns a single project agent by id.
func GetProjectAgentByID(db *sql.DB, id int64) (*models.AgentConfig, error) {
	var a models.AgentConfig
	err := db.QueryRow(`SELECT id, title, use_case, prompt, source, created_at, updated_at FROM project_agents WHERE id=?`, id).Scan(&id, &a.Title, &a.UseCase, &a.Prompt, &a.Source, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// GetProjectAgentByTitle returns a single project agent by title.
func GetProjectAgentByTitle(db *sql.DB, title string) (*models.AgentConfig, int64, error) {
	var a models.AgentConfig
	var id int64
	err := db.QueryRow(`SELECT id, title, use_case, prompt, source, created_at, updated_at FROM project_agents WHERE title=?`, title).Scan(&id, &a.Title, &a.UseCase, &a.Prompt, &a.Source, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	a.ID = id
	return &a, id, nil
}

// DeleteProjectAgentsBySource removes all project agents with the given source.
func DeleteProjectAgentsBySource(db *sql.DB, source string) error {
	_, err := db.Exec(`DELETE FROM project_agents WHERE source = ?`, source)
	return err
}

// ─── Project Tools ───────────────────────────────────────────

// GetProjectTools returns all project-level tool configurations.
func GetProjectTools(dbr *sql.DB) ([]models.ToolConfig, error) {
	rows, err := dbr.Query(`SELECT id, name, type, command, description, parameters, source, COALESCE(mcp_id, '') FROM project_tools ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query project_tools: %w", err)
	}
	defer rows.Close()

	var tools []models.ToolConfig
	for rows.Next() {
		var id int64
		var t models.ToolConfig
		var paramsStr, mcpID string
		if err := rows.Scan(&id, &t.Name, &t.Type, &t.Command, &t.Description, &paramsStr, &t.Source, &mcpID); err != nil {
			return nil, fmt.Errorf("failed to scan project_tools: %w", err)
		}
		if paramsStr != "" && paramsStr != "{}" {
			var params interface{}
			if err := json.Unmarshal([]byte(paramsStr), &params); err == nil {
				t.Parameters = params
			}
		}
		if mcpID != "" {
			if params, ok := t.Parameters.(map[string]interface{}); ok {
				params["mcp_id"] = mcpID
			} else if t.Parameters == nil {
				t.Parameters = map[string]interface{}{"mcp_id": mcpID}
			}
		}
		tools = append(tools, t)
	}
	if tools == nil {
		tools = []models.ToolConfig{}
	}
	return tools, rows.Err()
}

// SaveProjectTools replaces all project-level tool configurations.
func SaveProjectTools(dbr *sql.DB, tools []models.ToolConfig) error {
	tx, err := dbr.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin tx for project_tools: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM project_tools`); err != nil {
		return fmt.Errorf("failed to clear project_tools: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.Prepare(`INSERT INTO project_tools (name, type, command, description, parameters, source, mcp_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert project_tools: %w", err)
	}
	defer stmt.Close()

	for _, t := range tools {
		source := t.Source
		if source == "" {
			source = "user"
		}
		// Extract mcp_id from parameters if present
		mcpID := ""
		if params, ok := t.Parameters.(map[string]interface{}); ok {
			if mid, exists := params["mcp_id"]; exists {
				mcpID, _ = mid.(string)
				delete(params, "mcp_id")
				if len(params) == 0 {
					t.Parameters = nil
				}
			}
		}
		paramsStr := "{}"
		if t.Parameters != nil {
			if b, err := json.Marshal(t.Parameters); err == nil {
				paramsStr = string(b)
			}
		}
		if _, err := stmt.Exec(t.Name, t.Type, t.Command, t.Description, paramsStr, source, mcpID, now, now); err != nil {
			return fmt.Errorf("failed to insert project_tool: %w", err)
		}
	}

	return tx.Commit()
}

// ─── Project Prompts ─────────────────────────────────────────

// GetProjectPrompt returns a project prompt value by key.
func GetProjectPrompt(dbr *sql.DB, key string) (string, error) {
	var value string
	err := dbr.QueryRow(`SELECT value FROM project_prompts WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get project_prompt %s: %w", key, err)
	}
	return value, nil
}

// SetProjectPrompt upserts a project prompt value.
func SetProjectPrompt(dbr *sql.DB, key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := dbr.Exec(
		`INSERT INTO project_prompts (key, value, created_at, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = ?`,
		key, value, now, now, value, now,
	)
	if err != nil {
		return fmt.Errorf("failed to set project_prompt %s: %w", key, err)
	}
	return nil
}

// ─── Project Security ────────────────────────────────────────

// GetProjectSecurity returns all project security entries.
func GetProjectSecurity(dbr *sql.DB) ([]map[string]interface{}, error) {
	rows, err := dbr.Query(`SELECT id, dir, writable FROM project_security ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query project_security: %w", err)
	}
	defer rows.Close()

	var entries []map[string]interface{}
	for rows.Next() {
		var id int64
		var dir string
		var writable int
		if err := rows.Scan(&id, &dir, &writable); err != nil {
			return nil, fmt.Errorf("failed to scan project_security: %w", err)
		}
		entries = append(entries, map[string]interface{}{
			"dir":      dir,
			"writable": writable == 1,
		})
	}
	if entries == nil {
		entries = []map[string]interface{}{}
	}
	return entries, rows.Err()
}

// SaveProjectSecurity replaces all project security entries.
func SaveProjectSecurity(dbr *sql.DB, entries []map[string]interface{}) error {
	tx, err := dbr.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin tx for project_security: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM project_security`); err != nil {
		return fmt.Errorf("failed to clear project_security: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.Prepare(`INSERT INTO project_security (dir, writable, created_at, updated_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert project_security: %w", err)
	}
	defer stmt.Close()

	for _, e := range entries {
		dir, _ := e["dir"].(string)
		writable := 0
		if w, ok := e["writable"].(bool); ok && w {
			writable = 1
		}
		if _, err := stmt.Exec(dir, writable, now, now); err != nil {
			return fmt.Errorf("failed to insert project_security: %w", err)
		}
	}

	return tx.Commit()
}

// ─── Migration helpers ───────────────────────────────────────

// MigrateProjectConfigs migrates data from config table to dedicated tables.
func MigrateProjectConfigs(dbr *sql.DB) error {
	// Migrate project_agents
	if val, err := GetConfig(dbr, "project_agents"); err == nil && val != "" {
		var agents []models.AgentConfig
		if json.Unmarshal([]byte(val), &agents) == nil && len(agents) > 0 {
			if err := SaveProjectAgents(dbr, agents); err != nil {
				return fmt.Errorf("migrate project_agents: %w", err)
			}
		}
		DeleteConfigSilent(dbr, "project_agents")
	}

	// Migrate project_tools
	if val, err := GetConfig(dbr, "project_tools"); err == nil && val != "" {
		var tools []models.ToolConfig
		if json.Unmarshal([]byte(val), &tools) == nil && len(tools) > 0 {
			if err := SaveProjectTools(dbr, tools); err != nil {
				return fmt.Errorf("migrate project_tools: %w", err)
			}
		}
		DeleteConfigSilent(dbr, "project_tools")
	}

	// Migrate project_security
	if val, err := GetConfig(dbr, "project_security"); err == nil && val != "" {
		var entries []map[string]interface{}
		if json.Unmarshal([]byte(val), &entries) == nil && len(entries) > 0 {
			if err := SaveProjectSecurity(dbr, entries); err != nil {
				return fmt.Errorf("migrate project_security: %w", err)
			}
		}
		DeleteConfigSilent(dbr, "project_security")
	}

	// Migrate prompts
	for _, key := range []string{"system_prompt", "tool_usage_prompt", "summary_prompt"} {
		if val, err := GetConfig(dbr, key); err == nil && val != "" {
			if err := SetProjectPrompt(dbr, key, val); err != nil {
				return fmt.Errorf("migrate %s: %w", key, err)
			}
			// Keep old config key for backwards compat — executor reads from both
		}
	}

	return nil
}

// DeleteConfigSilent deletes a config key, ignoring errors.
func DeleteConfigSilent(dbr *sql.DB, key string) {
	_, _ = dbr.Exec(`DELETE FROM config WHERE key = ?`, key)
}
