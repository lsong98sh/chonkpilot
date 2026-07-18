//go:build windows
// +build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot/internal/db"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/prompts"
	"github.com/chonkpilot/chonkpilot/pkg/ide/folder"
	"go.uber.org/zap"
)

// ─── Config operations ─────────────────────────────────────

// GetAllConfig returns all project configuration.
func (a *App) GetAllConfig() (map[string]interface{}, error) {
	var result map[string]interface{}
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		configs, err := db.GetAllConfig(sqlDB)
		if err != nil {
			return err
		}
		var projectTools []models.ToolConfig
		if val, ok := configs["project_tools"]; ok && val != "" {
			json.Unmarshal([]byte(val), &projectTools)
		}
		result = map[string]interface{}{
			"config":       configs,
			"workDir":      a.workDir,
			"projectTools": projectTools,
		}
		// Include user-level LLM list so agent editor can reference them by name
		if a.userCfg != nil {
			uc := a.userCfg.Get()
			result["llms"] = uc.LLMs
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// SetConfig sets a config key/value.
// For prompt keys (system_prompt, tool_usage_prompt, summary_prompt),
// writes to project_prompts table in addition to the config table.
func (a *App) SetConfig(key, value string) error {
	if err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		// Write to config table (generic)
		if err := db.SetConfig(sqlDB, key, value); err != nil {
			return err
		}
		// Also sync prompt keys to project_prompts table
		switch key {
		case "system_prompt", "tool_usage_prompt", "summary_prompt":
			if dberr := db.SetProjectPrompt(sqlDB, key, value); dberr != nil {
				a.logger.Warn("SetConfig: failed to sync to project_prompts", zap.Error(dberr))
			}
		}
		return nil
	}); err != nil {
		return err
	}
	a.push("config:refresh", map[string]interface{}{})
	return nil
}

// GetConfig returns a config value by key (returns empty string if not found).
func (a *App) GetConfig(key string) (map[string]string, error) {
	var val string
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		v, err := db.GetConfig(sqlDB, key)
		if err != nil {
			return err
		}
		val = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"value": val}, nil
}

// GetPrompt returns a prompt by key with embedded fallback.
// Reads from project_prompts table first, then falls back to config table (legacy).
func (a *App) GetPrompt(key string) (map[string]string, error) {
	// Check project_prompts table first, then config table for backwards compat
	var dbVal string
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		v, err := db.GetProjectPrompt(sqlDB, key)
		if err == nil && v != "" {
			dbVal = v
			return nil
		}
		// Fallback to legacy config table
		v, err = db.GetConfig(sqlDB, key)
		if err == nil {
			dbVal = v
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if dbVal != "" {
		return map[string]string{"value": dbVal}, nil
	}
	switch key {
	case "system_prompt":
		return map[string]string{"value": prompts.DefaultSystemPrompt}, nil
	case "tool_usage_prompt":
		return map[string]string{"value": prompts.DefaultToolUsage}, nil
	case "summary_prompt":
		return map[string]string{"value": prompts.DefaultSummaryPrompt}, nil
	}
	return map[string]string{"value": ""}, nil
}

// GetRecentDirs returns recently accessed directories.
func (a *App) GetRecentDirs() (map[string]interface{}, error) {
	if a.recentMgr == nil {
		return map[string]interface{}{"dirs": []string{}}, nil
	}
	dirs, err := a.recentMgr.GetRecentDirs(10)
	if err != nil {
		return map[string]interface{}{"dirs": []string{}}, nil
	}
	return map[string]interface{}{"dirs": dirs}, nil
}

// OpenDir opens a new window for the given directory.
func (a *App) OpenDir(dirPath string) (map[string]string, error) {
	if dirPath == "" {
		return nil, fmt.Errorf("path required")
	}
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exePath, "-dir", dirPath)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return map[string]string{
		"code":    "OK",
		"message": "New window opened",
		"path":    dirPath,
	}, nil
}

// OpenDirDialog opens a folder picker and opens a new window.
func (a *App) OpenDirDialog() (map[string]string, error) {
	selectedPath, err := folder.PickFolder("Select Project Directory")
	if err != nil {
		return nil, err
	}
	if selectedPath == "" {
		return map[string]string{"code": "CANCELLED", "message": "User cancelled"}, nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exePath, "-dir", selectedPath)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return map[string]string{
		"code":    "OK",
		"message": "New window opened",
		"path":    selectedPath,
	}, nil
}

// OpenFileDialog opens a file picker dialog.
func (a *App) OpenFileDialog(data map[string]interface{}) (map[string]interface{}, error) {
	path, err := folder.PickFile("Select File", "All files\x00*.*\x00\x00")
	if err != nil {
		return nil, err
	}
	if path == "" {
		return map[string]interface{}{"code": "CANCELLED"}, nil
	}
	return map[string]interface{}{"path": path}, nil
}

// PickFolder opens a folder picker.
func (a *App) PickFolder() (map[string]interface{}, error) {
	path, err := folder.PickFolder("Select Directory")
	if err != nil {
		return nil, err
	}
	if path == "" {
		return map[string]interface{}{"code": "CANCELLED"}, nil
	}
	return map[string]interface{}{"path": path}, nil
}

// PickExecutableFile opens a file picker for executable files.
func (a *App) PickExecutableFile() (map[string]interface{}, error) {
	path, err := folder.PickFile("Select Executable", "Executable files\x00*.exe\x00All files\x00*.*\x00\x00")
	if err != nil {
		return nil, err
	}
	if path == "" {
		return map[string]interface{}{"code": "CANCELLED"}, nil
	}
	return map[string]interface{}{"path": path}, nil
}

// GetUserConfig returns user-level config.
func (a *App) GetUserConfig() (map[string]interface{}, error) {
	if a.userCfg == nil {
		return nil, fmt.Errorf("user config not available")
	}
	cfg := a.userCfg.Get()
	return map[string]interface{}{"config": cfg}, nil
}

// SaveUserConfig saves user-level config.
func (a *App) SaveUserConfig(body map[string]interface{}) error {
	if a.userCfg == nil {
		return fmt.Errorf("user config not available")
	}
	cfg := a.userCfg.Get()
	if llms, ok := body["llms"]; ok {
		data, err := json.Marshal(llms)
		if err != nil {
			return fmt.Errorf("marshal llms: %w", err)
		}
		var v []models.LLMProvider
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("unmarshal llms: %w", err)
		}
		cfg.LLMs = v
	}
	if d, ok := body["defaultLLM"]; ok {
		switch v := d.(type) {
		case float64:
			cfg.DefaultLLM = int(v)
		}
	}
	if theme, ok := body["theme"]; ok {
		if s, ok := theme.(string); ok {
			cfg.Theme = s
		}
	}
	if v, ok := body["chromePath"]; ok {
		if s, ok := v.(string); ok {
			cfg.ChromePath = s
		}
	}
	if v, ok := body["maxToolIterations"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.MaxToolIterations = int(n)
		}
	}
	if v, ok := body["responseTimeout"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.ResponseTimeout = int(n)
		}
	}
	if v, ok := body["streamTimeout"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.StreamTimeout = int(n)
		}
	}
	if v, ok := body["activeSessionID"]; ok {
		if s, ok := v.(string); ok {
			cfg.ActiveSessionID = s
		}
	}
	if servers, ok := body["mcpServers"]; ok {
		data, err := json.Marshal(servers)
		if err == nil {
			var v []models.MCPServerConfig
			if err := json.Unmarshal(data, &v); err == nil {
				// Validate MCP server names: must start with letter, only [a-zA-Z0-9_]
				for _, srv := range v {
					if srv.Name == "" {
						continue
					}
					matched, _ := regexp.MatchString(`^[a-zA-Z][a-zA-Z0-9_]*$`, srv.Name)
					if !matched {
						return fmt.Errorf("invalid MCP server name '%s': must start with a letter and contain only letters, digits, and underscores", srv.Name)
					}
				}
				cfg.MCPServers = v
			}
		}
	}
	if v, ok := body["javaPath"]; ok {
		if s, ok := v.(string); ok {
			cfg.JavaPath = s
		}
	}
	if v, ok := body["pythonPath"]; ok {
		if s, ok := v.(string); ok {
			cfg.PythonPath = s
		}
	}
	if v, ok := body["nodePath"]; ok {
		if s, ok := v.(string); ok {
			cfg.NodePath = s
		}
	}
	if v, ok := body["logLevel"]; ok {
		if s, ok := v.(string); ok {
			cfg.LogLevel = s
		}
	}
	if v, ok := body["retryCount"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.RetryCount = int(n)
		}
	}
	if v, ok := body["retryDelay"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.RetryDelay = int(n)
		}
	}
	if v, ok := body["codeIndexTemperature"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.CodeIndexTemperature = n
		}
	}
	if v, ok := body["toolMaxDepth"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.ToolMaxDepth = int(n)
		}
	}
	if v, ok := body["taskPollIntervalMs"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.TaskPollIntervalMs = int(n)
		}
	}
	if v, ok := body["searchMaxResults"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.SearchMaxResults = int(n)
		}
	}
	if v, ok := body["fetchMaxBodySizeKB"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.FetchMaxBodySizeKB = int(n)
		}
	}
	if v, ok := body["browserWindowWidth"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.BrowserWindowWidth = int(n)
		}
	}
	if v, ok := body["browserWindowHeight"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.BrowserWindowHeight = int(n)
		}
	}
	if v, ok := body["browserLogCap"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.BrowserLogCap = int(n)
		}
	}
	if v, ok := body["llmTLSHandshakeTimeout"]; ok {
		switch n := v.(type) {
		case float64:
			cfg.LLMTLSHandshakeTimeout = int(n)
		}
	}
	if err := a.userCfg.Update(cfg); err != nil {
		return err
	}
	a.push("config:refresh", map[string]interface{}{})
	return nil
}

// GetProjectTools returns project-level tool configuration.
func (a *App) GetProjectTools() (map[string]interface{}, error) {
	var tools []models.ToolConfig
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		tools, err = db.GetProjectTools(sqlDB)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"tools": tools}, nil
}

// SaveProjectTools saves project-level tool configuration.
func (a *App) SaveProjectTools(data map[string]interface{}) error {
	toolsRaw, err := json.Marshal(data["tools"])
	if err != nil {
		return fmt.Errorf("marshal project_tools: %w", err)
	}
	var tools []models.ToolConfig
	if err := json.Unmarshal(toolsRaw, &tools); err != nil {
		return fmt.Errorf("unmarshal project_tools: %w", err)
	}
	if err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.SaveProjectTools(sqlDB, tools)
	}); err != nil {
		return err
	}
	a.push("config:refresh", map[string]interface{}{})
	return nil
}

// GetProjectAgents returns project-level agent configuration.
func (a *App) GetProjectAgents() (map[string]interface{}, error) {
	var agents []models.AgentConfig
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		agents, err = db.GetProjectAgents(sqlDB)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"agents": agents}, nil
}

// SaveProjectAgents saves project-level agent configuration.
func (a *App) SaveProjectAgents(data map[string]interface{}) error {
	agentsRaw, err := json.Marshal(data["agents"])
	if err != nil {
		return fmt.Errorf("marshal project_agents: %w", err)
	}
	var agents []models.AgentConfig
	if err := json.Unmarshal(agentsRaw, &agents); err != nil {
		return fmt.Errorf("unmarshal project_agents: %w", err)
	}
	if err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.SaveProjectAgents(sqlDB, agents)
	}); err != nil {
		return err
	}
	a.push("config:refresh", map[string]interface{}{})
	return nil
}

// GetProjectSecurity returns project-level security configuration.
func (a *App) GetProjectSecurity() (map[string]interface{}, error) {
	var entries []map[string]interface{}
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		var err error
		entries, err = db.GetProjectSecurity(sqlDB)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"entries": entries}, nil
}

// SaveProjectSecurity saves project-level security configuration.
func (a *App) SaveProjectSecurity(data map[string]interface{}) error {
	entriesRaw, err := json.Marshal(data["entries"])
	if err != nil {
		return fmt.Errorf("marshal project_security: %w", err)
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(entriesRaw, &entries); err != nil {
		return fmt.Errorf("unmarshal project_security: %w", err)
	}
	if err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		return db.SaveProjectSecurity(sqlDB, entries)
	}); err != nil {
		return err
	}
	a.push("config:refresh", map[string]interface{}{})
	return nil
}

// ─── User Configuration ─────────────────────────────────────

// DiscoverMCPServerTools calls tools/list on an MCP server URL to discover available tools.
// url convention: base URL of the MCP server, e.g. "http://localhost:5612/mcp" or "http://localhost:5612/myserver/mcp"
//   - Direct POST: tries POST to url directly (for non-SSE MCP servers)
//   - SSE transport: connects to {url}/sse → gets sessionId → POST {url}/message?sessionId=X → reads response from SSE stream
func (a *App) DiscoverMCPServerTools(name, rawURL string) (map[string]interface{}, error) {
	matched, _ := regexp.MatchString(`^[a-zA-Z][a-zA-Z0-9_]*$`, name)
	if !matched {
		return nil, fmt.Errorf("invalid MCP server name '%s': must start with a letter and contain only letters, digits, and underscores", name)
	}

	baseURL := strings.TrimRight(rawURL, "/")

	// First, try direct POST (simpler, some MCP servers support it)
	a.logger.Debug("[MCP] direct POST", zap.String("url", baseURL))
	if result, err := a.discoverViaDirectPost(baseURL); err == nil {
		result["transport"] = "direct"
		return result, nil
	}

	// Fallback: SSE transport
	result, err := a.discoverViaSSE(baseURL)
	if err == nil {
		result["transport"] = "sse"
	}
	return result, err
}

// discoverViaDirectPost tries a simple POST JSON-RPC to the URL.
func (a *App) discoverViaDirectPost(url string) (map[string]interface{}, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	reqData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := client.Post(url, "application/json", bytes.NewReader(reqData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(respData)) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	return parseToolsListResponse(respData)
}

// discoverViaSSE connects to {base}/sse, gets a session, posts {base}/message?sessionId=X, reads response from SSE.
func (a *App) discoverViaSSE(base string) (map[string]interface{}, error) {
	sseURL := base + "/sse"
	msgBase := base + "/message"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	a.logger.Debug("[MCP] SSE connecting", zap.String("sseURL", sseURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect SSE: %w", err)
	}
	defer resp.Body.Close()

	a.logger.Debug("[MCP] SSE connected, reading session...")

	// Read SSE stream to get sessionId
	br := bufio.NewReader(resp.Body)
	var sessionID string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read SSE: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			a.logger.Debug("[MCP] SSE event", zap.String("data", data))
			if strings.Contains(data, "sessionId") {
				if idx := strings.Index(data, "sessionId="); idx >= 0 {
					sessionID = data[idx+len("sessionId="):]
				}
				break
			}
		}
	}

	if sessionID == "" {
		return nil, fmt.Errorf("no sessionId from SSE endpoint")
	}
	msgURL := fmt.Sprintf("%s?sessionId=%s", msgBase, sessionID)
	a.logger.Debug("[MCP] message URL", zap.String("msgURL", msgURL))

	// Build JSON-RPC tools/list request
	rpcBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	rpcData, err := json.Marshal(rpcBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	// POST to message endpoint
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, msgURL, bytes.NewReader(rpcData))
	if err != nil {
		return nil, fmt.Errorf("create POST: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/json")

	a.logger.Debug("[MCP] POSTing tools/list...")
	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		return nil, fmt.Errorf("POST message: %w", err)
	}
	defer postResp.Body.Close()

	a.logger.Debug("[MCP] POST response", zap.Int("status", postResp.StatusCode))

	// Some MCP servers respond inline in the POST body
	postBody, postErr := io.ReadAll(postResp.Body)
	if postErr == nil && len(bytes.TrimSpace(postBody)) > 0 {
		a.logger.Debug("[MCP] POST body", zap.String("body", string(postBody)))
		if result, err := parseToolsListResponse(postBody); err == nil {
			return result, nil
		}
	}

	// The response may come through the SSE stream. Continue reading SSE.
	a.logger.Debug("[MCP] waiting for SSE response...")
	type sseResult struct {
		data json.RawMessage
		err  error
	}
	resultCh := make(chan sseResult, 1)

	go func() {
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				resultCh <- sseResult{err: fmt.Errorf("SSE read: %w", err)}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				var raw json.RawMessage
				if json.Unmarshal([]byte(data), &raw) == nil {
					resultCh <- sseResult{data: raw}
					return
				}
			}
		}
	}()

	select {
	case r := <-resultCh:
		if r.err != nil {
			return nil, r.err
		}
		return parseToolsListResponse(r.data)
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for MCP tools/list response")
	}
}

// parseToolsListResponse parses a JSON-RPC tools/list response.
func parseToolsListResponse(data []byte) (map[string]interface{}, error) {
	var mcpResp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *struct {
			Code    int         `json:"code"`
			Message string      `json:"message"`
			Data    interface{} `json:"data,omitempty"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &mcpResp); err != nil {
		return nil, fmt.Errorf("invalid JSON-RPC response: %w", err)
	}
	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP server error [%d]: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}
	if len(mcpResp.Result) == 0 {
		return nil, fmt.Errorf("empty response from MCP server")
	}

	var listResult struct {
		Tools []models.MCPServerToolConfig `json:"tools"`
	}
	if err := json.Unmarshal(mcpResp.Result, &listResult); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	return map[string]interface{}{
		"tools": listResult.Tools,
	}, nil
}

// RestoreAgent restores an agent from its embedded resource by DB row id.
// Returns the restored agent title on success.
func (a *App) RestoreAgent(id int64) (map[string]interface{}, error) {
	var title string
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		// Get current agent from DB
		agent, err := db.GetProjectAgentByID(sqlDB, id)
		if err != nil {
			return fmt.Errorf("failed to get agent %d: %w", id, err)
		}
		if agent == nil {
			return fmt.Errorf("agent %d not found", id)
		}

		// Find matching embedded agent by title
		names := prompts.Agents()
		for _, name := range names {
			agtTitle, useCase, prompt, err := prompts.ReadAgent(name)
			if err != nil {
				continue
			}
			if agtTitle != agent.Title {
				continue
			}

			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := sqlDB.Exec(
				`UPDATE project_agents SET title=?, use_case=?, prompt=?, source='system', updated_at=? WHERE id=?`,
				agtTitle, useCase, prompt, now, id,
			); err != nil {
				return fmt.Errorf("failed to restore agent %d: %w", id, err)
			}
			title = agtTitle
			return nil
		}

		return fmt.Errorf("no embedded resource found for agent %q", agent.Title)
	})
	if err != nil {
		return nil, err
	}
	a.push("config:refresh", map[string]interface{}{})
	return map[string]interface{}{"title": title}, nil
}

// LoadMissingAgentsFromResource reads all embedded agent files and inserts
// any that are not yet in the project_agents table. Returns the count of inserted agents.
func (a *App) LoadMissingAgentsFromResource() (map[string]interface{}, error) {
	var inserted int
	err := db.WithDB(a.workDir, func(sqlDB *sql.DB) error {
		names := prompts.Agents()
		if len(names) == 0 {
			return nil
		}

		now := time.Now().UTC().Format(time.RFC3339)

		for _, name := range names {
			title, useCase, prompt, err := prompts.ReadAgent(name)
			if err != nil {
				continue
			}

			// Check if an agent with this title already exists
			existing, _, err := db.GetProjectAgentByTitle(sqlDB, title)
			if err != nil {
				return fmt.Errorf("failed to check existing agent %q: %w", title, err)
			}
			if existing != nil {
				continue // already exists, skip
			}

			if _, err := sqlDB.Exec(
				`INSERT INTO project_agents (title, use_case, prompt, source, created_at, updated_at) VALUES (?, ?, ?, 'system', ?, ?)`,
				title, useCase, prompt, now, now,
			); err != nil {
				return fmt.Errorf("failed to insert embedded agent %q: %w", title, err)
			}
			inserted++
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	if inserted > 0 {
		a.push("config:refresh", map[string]interface{}{})
	}
	return map[string]interface{}{"inserted": inserted}, nil
}
