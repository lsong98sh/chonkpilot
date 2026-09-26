// Package server 是 chonkpilot-mcp-server 的可复用核心：契约扫描注册原语 + 执行器。
//
// lib 形态收敛（2026-09-07）：不做容器/实例/进程内注册表/传输层——go-sdk mcp.Server
// 由装配方自建并作为参数传入 RegisterContracts，扫描四原语（*.tool.md / *.prompt.md /
// *.skill.md / *.resource.md）解析后经官方 AddTool/AddPrompt/AddResource 注册，
// 执行器 handler（spawn-on-call executor，按契约 runtime 相对 md 解析）一并挂载。
// 独立 exe 形态见模块根 main.go（args 处理 + stdio/http/service 传输）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// metaNamespace 是调用上下文在协议 _meta 下的命名空间（键：instance_id/work_dir/data_dir）。
// 仅内部调用链使用（gateway → 内嵌/self mcp-server）；第三方 client 不携带。
const metaNamespace = "chonkpilot"

// CallContext 是一次 tools/call 的调用上下文（决策 R-11 二次升级：不再经 tool arguments 传递）。
// Present 表示 _meta 是否携带 chonkpilot 命名空间——存在但 instance 为空 = 异常；
// 完全无上下文（外部直连 stdio/HTTP 客户端）→ 不注入、不报错。
type CallContext struct {
	InstanceID string
	WorkDir    string
	DataDir    string
	Present    bool
}

// callContextFromMeta 从 tools/call 请求的 _meta 解析调用上下文（无命名空间 → Present=false）。
func callContextFromMeta(meta mcp.Meta) CallContext {
	if meta == nil {
		return CallContext{}
	}
	raw, ok := meta[metaNamespace].(map[string]any)
	if !ok {
		return CallContext{}
	}
	cx := CallContext{Present: true}
	cx.InstanceID, _ = raw["instance_id"].(string)
	cx.WorkDir, _ = raw["work_dir"].(string)
	cx.DataDir, _ = raw["data_dir"].(string)
	return cx
}

// err 校验调用上下文：存在但 instance 为空 → 异常（instance 不应为空）；无上下文 → nil。
func (cx CallContext) err() error {
	if cx.Present && strings.TrimSpace(cx.InstanceID) == "" {
		return paths.ErrNoInstance
	}
	return nil
}

// CallContextMeta 构造协议 _meta（命名空间 chonkpilot：instance_id/work_dir/data_dir）。
// 供调用链上游（gateway）在 tools/call 时透传调用上下文；第三方 client 不应携带。
func CallContextMeta(instanceID, workDir, dataDir string) mcp.Meta {
	return mcp.Meta{metaNamespace: map[string]any{
		"instance_id": instanceID,
		"work_dir":    workDir,
		"data_dir":    dataDir,
	}}
}

// RegisterContracts 扫描契约根 root（递归四后缀）并把原语注册进调用方传入的 go-sdk server：
//   - *.tool.md    → tool（含 executor handler，调用参数由调用方经 tools/call 传入）
//   - *.prompt.md  → prompt（_meta.type=prompt）
//   - *.skill.md   → prompt（_meta.type=skill，与 prompt 同构注册以便 prompts/list 区分）
//   - *.resource.md→ resource（URI 缺省补 file://<name>）
//
// root 缺失或为空目录 → 空注册（no-op，返回 nil），调用方决定空能力面语义。
// cfg 为执行配置（nil → DefaultConfig()）；ExecDir 已移除——exe 型 runtime 按契约文件
// 所在目录相对解析（见 resolveRuntime），解释器走 PATH。
func RegisterContracts(srv *mcp.Server, root string, cfg *Config) error {
	if srv == nil {
		return fmt.Errorf("RegisterContracts: server is required")
	}
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil // 契约根缺失：空注册
		}
		return err
	}
	lim := cfg.ensureLimiter() // 并发限流器（tools/call 内生效；上限可被 prj max_concurrency 运行期覆盖）

	// tools
	toolDocs, err := loadTools(root)
	if err != nil {
		return fmt.Errorf("load tools: %w", err)
	}
	for _, td := range toolDocs {
		t, err := buildTool(td, cfg)
		if err != nil {
			return fmt.Errorf("build tool %s: %w", td.Name, err)
		}
		srv.AddTool(t, makeToolHandler(td, cfg, lim))
	}
	if len(toolDocs) > 0 {
		log.Printf("[mcp-server] %s: %d tools", root, len(toolDocs))
	}

	// prompts（*.prompt.md 注册为 prompt，_meta.type=prompt）
	promptDocs, err := loadPrompts(root)
	if err != nil {
		return fmt.Errorf("load prompts: %w", err)
	}
	for _, d := range promptDocs {
		p := buildPrompt(d)
		setPromptType(p, "prompt")
		srv.AddPrompt(p, makePromptHandler(d, cfg))
	}
	if len(promptDocs) > 0 {
		log.Printf("[mcp-server] %s: %d prompts", root, len(promptDocs))
	}

	// skills（*.skill.md 注册为 prompt，_meta.type=skill）
	skillDocs, err := loadSkills(root)
	if err != nil {
		return fmt.Errorf("load skills: %w", err)
	}
	for _, d := range skillDocs {
		pd := &PromptDoc{Name: d.Name, Description: d.Description, Body: d.Body}
		p := buildPrompt(pd)
		setPromptType(p, "skill")
		srv.AddPrompt(p, makePromptHandler(pd, cfg))
	}
	if len(skillDocs) > 0 {
		log.Printf("[mcp-server] %s: %d skills (as prompts)", root, len(skillDocs))
	}

	// resources（URI 缺省补 file://<name>）
	resDocs, err := loadResources(root)
	if err != nil {
		return fmt.Errorf("load resources: %w", err)
	}
	for _, r := range resDocs {
		if r.URI == "" {
			r.URI = "file://" + r.Name
		}
		srv.AddResource(&mcp.Resource{
			URI:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MIMEType:    r.MIMEType,
		}, makeResourceHandler(r))
	}
	if len(resDocs) > 0 {
		log.Printf("[mcp-server] %s: %d resources", root, len(resDocs))
	}
	return nil
}

// setPromptType 把类型标注写入 prompt 的 _meta（MCP 保留扩展字段，prompts/list 原样透出）。
// type 标识该原语类别：本模块契约扫描只有 prompt（*.prompt.md）/ skill（*.skill.md）两类；
// agent 资产不属本模块扫描面（agent 扫描分支从未实现、后缀常量已按 G7 摘除）。
// 25 §5/T2（2026-09-25）：agent **只"注入"不"注册"** —— llm server 不再经
// `prompts/register asset_kind=agent` 注册 agent 资产（该 asset_kind 已不受理），
// 故 `_meta.type=agent` 不再于任何资产面产出。
func setPromptType(p *mcp.Prompt, t string) {
	if p.Meta == nil {
		p.Meta = mcp.Meta{}
	}
	p.Meta["type"] = t
}

// makeToolHandler 构造工具 handler：并发限流 → 调用上下文（_meta）→ 参数 → 契约驱动执行 →
// 统一 JSON 结果文本（executor 子进程环境注入 CHONKPILOT_*）。
func makeToolHandler(td *ToolDoc, cfg *Config, lim *limiter) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		lim.acquire()
		defer lim.release()
		args := map[string]any{}
		if raw := req.Params.Arguments; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &args); err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: wrapFail("arguments must be a JSON object")}},
					IsError: true,
				}, nil
			}
		}
		// 调用上下文经协议 _meta 透传（不进 arguments）：存在但 instance 为空 = 异常。
		cx := callContextFromMeta(req.Params.Meta)
		if cxErr := cx.err(); cxErr != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: wrapFail(cxErr.Error())}},
				IsError: true,
			}, nil
		}
		text, err := callTool(ctx, cfg, td, args, cfg.defaultsMap(), cx)
		if err != nil {
			// 失败：text 应为 JSON（handle* 已构造 fail JSON）；早期错误补 fail JSON
			if text == "" {
				text = wrapFail(err.Error())
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: text}},
				IsError: true,
			}, nil
		}
		if text == "" {
			text = wrapSuccess("")
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil
	}
}

// makePromptHandler 渲染 prompt 模板（{{arg}} 占位替换）。
// 顺序：先按调用实参替换 {{arg}}（MCP GetPrompt 实参语义不变），再替换
// {{toolchain.<key>}}（能力原语文本里的工具链路径占位，取值 = 装配层注入的 usr 配置；
// 未知 key 原样保留，见 ReplaceToolchain）。
func makePromptHandler(d *PromptDoc, cfg *Config) mcp.PromptHandler {
	return func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		text := d.Body
		for k, v := range req.Params.Arguments {
			text = strings.ReplaceAll(text, "{{"+k+"}}", v)
		}
		text = ReplaceToolchain(text, cfg.toolchainVars())
		res := &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: text}},
			},
		}
		return res, nil
	}
}

// makeResourceHandler 返回静态资源内容（RB-4 ①，2026-09-22）：来源契约文件路径（r.Path）非空 →
// **实时读盘**取 `[content]` 段（内容不驻留）；无 Path → 返回扫描期副本 r.Content（兼容兜底）。
func makeResourceHandler(r *ResourceDoc) mcp.ResourceHandler {
	return func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		text := r.Content
		if r.Path != "" {
			if b, err := os.ReadFile(r.Path); err == nil {
				text = splitSections(b)["content"]
			}
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{URI: r.URI, MIMEType: r.MIMEType, Text: text}},
		}, nil
	}
}
