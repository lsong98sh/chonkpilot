// ExecClient 是 core-mcp-server 内部执行面（POST /internal/exec）的客户端。
//
// 用途（G-68 第二步）：插件（codegraph / vfts）的 LLM 工具仍按启用状态注册给 gateway，
// 但执行不再占用插件进程 —— 委托 core：core 按契约 spawn 一个**单工具 executor**，
// 取消 = 本 HTTP 请求 ctx 取消 → core 只杀该次 executor 进程树。
package coretools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
)

// ExecClient 实现 hook.ToolExec（结构化接口，无需显式声明）。
type ExecClient struct {
	base  string // http://127.0.0.1:<port>（不含 /mcp）
	token string
	hc    *http.Client
}

// NewExecClient 由 core 的 MCP 端点 URL（.../mcp）构造内部执行客户端。
func NewExecClient(mcpURL, token string) *ExecClient {
	return &ExecClient{
		base:  strings.TrimSuffix(strings.TrimSpace(mcpURL), "/mcp"),
		token: token,
		hc:    &http.Client{},
	}
}

type execReq struct {
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
	Context execCtx        `json:"context"`
}

type execCtx struct {
	InstanceID string `json:"instance_id,omitempty"`
	Session    string `json:"session,omitempty"`
	Turn       string `json:"turn,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	WorkDir    string `json:"work_dir,omitempty"`
	DataDir    string `json:"data_dir,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
}

type execResp struct {
	Text    string `json:"text"`
	IsError bool   `json:"isError"`
}

// Exec 执行一次工具（ctx 取消 → core 取消并杀本次 executor）。
func (c *ExecClient) Exec(ctx context.Context, tool string, args map[string]any, cx hook.ExecContext) (string, bool, error) {
	if c == nil || c.base == "" {
		return "", false, fmt.Errorf("core exec: 未配置")
	}
	raw, err := json.Marshal(execReq{
		Tool: tool,
		Args: args,
		Context: execCtx{
			InstanceID: cx.InstanceID,
			Session:    cx.Session,
			Turn:       cx.Turn,
			ToolCallID: cx.ToolCallID,
			WorkDir:    cx.WorkDir,
			DataDir:    cx.DataDir,
			Purpose:    cx.Purpose,
		},
	})
	if err != nil {
		return "", false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/exec", bytes.NewReader(raw))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("core exec %s: HTTP %d: %s", tool, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out execResp
	if err := json.Unmarshal(body, &out); err != nil {
		return "", false, fmt.Errorf("core exec %s: 解析应答: %w", tool, err)
	}
	return out.Text, out.IsError, nil
}
