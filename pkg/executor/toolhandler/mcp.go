package toolhandler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	icfg "github.com/chonkpilot/chonkpilot/internal/config"
	"github.com/chonkpilot/chonkpilot/internal/models"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/file"
	"github.com/chonkpilot/chonkpilot/pkg/executor/toolhandler/types"
)

// mcpRequestTimeout is the HTTP client timeout for all MCP server calls.
// Default 60s. Override via SetMCPTimeout.
var mcpRequestTimeout = 60 * time.Second

// SetMCPTimeout sets the global MCP request timeout.
func SetMCPTimeout(seconds int) {
	if seconds > 0 {
		mcpRequestTimeout = time.Duration(seconds) * time.Second
	}
}

// mcpRequest represents a JSON-RPC 2.0 request for MCP tool calls.
type mcpRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// mcpResponse represents a JSON-RPC 2.0 response from an MCP server.
type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

// mcpError represents a JSON-RPC error object.
type mcpError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// mcpToolCallPayload is the params for an MCP tools/call request.
type mcpToolCallPayload struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// handleMCPTool dispatches a tool call to an MCP server via JSON-RPC.
// Tries direct POST first; if that fails with non-JSON response, falls back to SSE transport.
func (h *Handler) handleMCPTool(tool models.ToolConfig, args map[string]interface{}) *types.ToolResult {
	params, ok := tool.Parameters.(map[string]interface{})
	if !ok {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   "mcp tool has invalid parameters (expected object with server_url and mcp_tool_name)",
		}
	}

	serverURL, _ := params["server_url"].(string)
	mcpToolName, _ := params["mcp_tool_name"].(string)
	if serverURL == "" || mcpToolName == "" {
		return &types.ToolResult{
			Success: false,
			Tool:    tool.Name,
			Error:   "mcp tool missing server_url or mcp_tool_name in parameters",
		}
	}

	transport, _ := params["transport"].(string)

	// If transport is known to be SSE, skip direct POST attempt
	if transport == "sse" {
		return trySSEMCPCall(serverURL, mcpToolName, args, tool.Name)
	}

	// Try direct POST first (works for non-SSE MCP servers)
	if result := tryDirectMCPCall(serverURL, mcpToolName, args); result != nil {
		return result
	}

	// Fallback: SSE transport
	return trySSEMCPCall(serverURL, mcpToolName, args, tool.Name)
}

// tryDirectMCPCall POSTs tools/call directly to the server URL.
// Returns nil if the response is not valid JSON-RPC (likely SSE-based server).
func tryDirectMCPCall(serverURL, mcpToolName string, args map[string]interface{}) *types.ToolResult {
	reqBody := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mcpToolCallPayload{
			Name:      mcpToolName,
			Arguments: args,
		},
	}
	reqData, err := json.Marshal(reqBody)
	if err != nil {
		return &types.ToolResult{Success: false, Tool: mcpToolName, Error: fmt.Sprintf("marshal: %s", err.Error())}
	}

	client := &http.Client{Timeout: mcpRequestTimeout}
	resp, err := client.Post(serverURL, "application/json", bytes.NewReader(reqData))
	if err != nil {
		return &types.ToolResult{Success: false, Tool: mcpToolName, Error: fmt.Sprintf("unreachable (%s): %s", serverURL, err.Error())}
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return &types.ToolResult{Success: false, Tool: mcpToolName, Error: fmt.Sprintf("read: %s", err.Error())}
	}

	// Try to parse as JSON-RPC
	var mcpResp mcpResponse
	if err := json.Unmarshal(respData, &mcpResp); err != nil {
		// Not valid JSON-RPC — likely SSE-based server, fall through to SSE transport
		return nil
	}

	if mcpResp.Error != nil {
		return &types.ToolResult{
			Success: false,
			Tool:    mcpToolName,
			Error:   fmt.Sprintf("MCP error [%d]: %s", mcpResp.Error.Code, mcpResp.Error.Message),
		}
	}

	return formatMCPResult(mcpResp.Result, serverURL, mcpToolName)
}

// trySSEMCPCall connects to SSE, gets a session, posts tools/call, reads response from SSE.
func trySSEMCPCall(serverURL, mcpToolName string, args map[string]interface{}, toolName string) *types.ToolResult {
	base := strings.TrimRight(serverURL, "/")
	sseURL := base + "/sse"
	msgBase := base + "/message"

	ctx, cancel := context.WithTimeout(context.Background(), mcpRequestTimeout)
	defer cancel()

	// 1. Connect to SSE to get sessionId
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return &types.ToolResult{Success: false, Tool: toolName, Error: fmt.Sprintf("create SSE req: %s", err.Error())}
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &types.ToolResult{Success: false, Tool: toolName, Error: fmt.Sprintf("SSE connect: %s", err.Error())}
	}
	defer resp.Body.Close()

	// Read sessionId from SSE stream
	br := bufio.NewReader(resp.Body)
	var sessionID string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return &types.ToolResult{Success: false, Tool: toolName, Error: fmt.Sprintf("SSE read: %s", err.Error())}
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if strings.Contains(data, "sessionId") {
				if idx := strings.Index(data, "sessionId="); idx >= 0 {
					sessionID = data[idx+len("sessionId="):]
				}
				break
			}
		}
	}
	if sessionID == "" {
		return &types.ToolResult{Success: false, Tool: toolName, Error: "no sessionId from SSE"}
	}

	// 2. Build tools/call request
	rpcBody := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mcpToolCallPayload{
			Name:      mcpToolName,
			Arguments: args,
		},
	}
	rpcData, err := json.Marshal(rpcBody)
	if err != nil {
		return &types.ToolResult{Success: false, Tool: toolName, Error: fmt.Sprintf("marshal: %s", err.Error())}
	}

	msgURL := fmt.Sprintf("%s?sessionId=%s", msgBase, sessionID)

	// 3. POST to message endpoint
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, msgURL, bytes.NewReader(rpcData))
	if err != nil {
		return &types.ToolResult{Success: false, Tool: toolName, Error: fmt.Sprintf("create POST: %s", err.Error())}
	}
	postReq.Header.Set("Content-Type", "application/json")

	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		return &types.ToolResult{Success: false, Tool: toolName, Error: fmt.Sprintf("POST: %s", err.Error())}
	}
	defer postResp.Body.Close()

	// Check POST body (some servers respond inline)
	postBody, postErr := io.ReadAll(postResp.Body)
	if postErr == nil && len(bytes.TrimSpace(postBody)) > 0 {
		var mcpResp mcpResponse
		if json.Unmarshal(postBody, &mcpResp) == nil {
			if mcpResp.Error != nil {
				return &types.ToolResult{
					Success: false,
					Tool:    toolName,
					Error:   fmt.Sprintf("MCP error [%d]: %s", mcpResp.Error.Code, mcpResp.Error.Message),
				}
			}
			return formatMCPResult(mcpResp.Result, serverURL, mcpToolName)
		}
	}

	// 4. Read response from SSE stream
	type sseResult struct {
		data json.RawMessage
		err  error
	}
	resultCh := make(chan sseResult, 1)

	go func() {
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				resultCh <- sseResult{err: err}
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
			return &types.ToolResult{Success: false, Tool: toolName, Error: fmt.Sprintf("SSE read: %s", r.err.Error())}
		}
		var mcpResp mcpResponse
		if err := json.Unmarshal(r.data, &mcpResp); err != nil {
			return &types.ToolResult{Success: false, Tool: toolName, Error: fmt.Sprintf("invalid SSE JSON-RPC: %s", err.Error())}
		}
		if mcpResp.Error != nil {
			return &types.ToolResult{
				Success: false,
				Tool:    toolName,
				Error:   fmt.Sprintf("MCP error [%d]: %s", mcpResp.Error.Code, mcpResp.Error.Message),
			}
		}
		return formatMCPResult(mcpResp.Result, serverURL, mcpToolName)
	case <-ctx.Done():
		return &types.ToolResult{Success: false, Tool: toolName, Error: "timeout waiting for SSE response"}
	}
}

// formatMCPResult extracts and formats the content from a JSON-RPC result.
func formatMCPResult(rawResult json.RawMessage, serverURL, mcpToolName string) *types.ToolResult {
	output := string(rawResult)
	var resultObj map[string]interface{}
	if json.Unmarshal(rawResult, &resultObj) == nil {
		if content, ok := resultObj["content"]; ok {
			switch c := content.(type) {
			case string:
				output = c
			default:
				if b, err := json.MarshalIndent(c, "", "  "); err == nil {
					output = string(b)
				}
			}
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Tool:    mcpToolName,
		RawResult: map[string]interface{}{
			"server_url":    serverURL,
			"mcp_tool_name": mcpToolName,
		},
	}
}

// handleAutoMCPTool dispatches a tool call to an MCP server discovered from global config.
// Two formats are supported:
//
//	Format 1 (specific, preferred): "mcp_<serverName>_<toolName>" — calls the specific tool with proper args.
//	Format 2 (generic, fallback):   "mcp_<serverName>" — calls any tool, requires 'tool_name' arg.
func (h *Handler) handleAutoMCPTool(toolName string, args map[string]interface{}) *types.ToolResult {
	// Strip "mcp_" prefix
	remainder := strings.TrimPrefix(toolName, "mcp_")
	if remainder == "" {
		return &types.ToolResult{
			Success: false,
			Tool:    toolName,
			Error:   fmt.Sprintf("invalid MCP tool name: %s", toolName),
		}
	}

	// Parse server name and MCP tool name
	serverName, mcpToolName := parseMCPToolName(remainder)
	if serverName == "" {
		return &types.ToolResult{
			Success: false,
			Tool:    toolName,
			Error:   fmt.Sprintf("unknown MCP server in tool name: %s", toolName),
		}
	}

	// Look up server URL and transport from user config
	serverURL, transport := lookupMCPServerConfig(serverName)
	if serverURL == "" {
		return &types.ToolResult{
			Success: false,
			Tool:    toolName,
			Error:   fmt.Sprintf("MCP server '%s' not found in global config", serverName),
		}
	}

	if mcpToolName == "" {
		// Format 2 (generic): extract tool_name and arguments from LLM args
		mcpToolName, _ = args["tool_name"].(string)
		if mcpToolName == "" {
			return &types.ToolResult{
				Success: false,
				Tool:    toolName,
				Error:   "MCP call requires 'tool_name' parameter for generic server tool",
			}
		}

		// Build the actual MCP tool arguments
		mcpArgs := make(map[string]interface{})
		if argsMap, ok := args["arguments"].(map[string]interface{}); ok {
			mcpArgs = argsMap
		} else {
			for k, v := range args {
				if k != "tool_name" && k != "arguments" {
					mcpArgs[k] = v
				}
			}
		}
		args = mcpArgs
	}

	// Reuse the existing handleMCPTool logic via models.ToolConfig
	tool := models.ToolConfig{
		Name: toolName,
		Type: "mcp",
		Parameters: map[string]interface{}{
			"server_url":    serverURL,
			"mcp_tool_name": mcpToolName,
			"transport":     transport,
		},
	}
	return h.handleMCPTool(tool, args)
}

// parseMCPToolName splits a remainder (after "mcp_" prefix) into server name and tool name.
// It checks known MCP servers from user config to correctly identify the boundary.
// If remainder is just a server name, toolName will be empty (generic fallback).
func parseMCPToolName(remainder string) (serverName, toolName string) {
	ucfg, err := icfg.EnsureUserConfig()
	if err != nil {
		// Fallback: split on first underscore
		parts := strings.SplitN(remainder, "_", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
		return remainder, ""
	}

	// Try to match the longest known server name prefix
	for _, srv := range ucfg.MCPServers {
		if srv.Name == "" || !srv.Enabled {
			continue
		}
		if remainder == srv.Name {
			return srv.Name, "" // server name only, generic call
		}
		if strings.HasPrefix(remainder, srv.Name+"_") {
			return srv.Name, strings.TrimPrefix(remainder, srv.Name+"_")
		}
	}

	// No known server found — return remainder as server name (will fail lookup)
	return remainder, ""
}

// lookupMCPServerConfig finds the URL and transport for a named MCP server in user config.
// Returns ("", "") if not found.
func lookupMCPServerConfig(name string) (url, transport string) {
	ucfg, err := icfg.EnsureUserConfig()
	if err != nil {
		return "", ""
	}
	for _, srv := range ucfg.MCPServers {
		if srv.Name == name && srv.Enabled {
			return srv.URL, srv.Transport
		}
	}
	return "", ""
}

// SetFetchTimeout sets the HTTP fetch timeout (re-export from file package).
func SetFetchTimeout(seconds int) {
	file.SetFetchTimeout(seconds)
}
