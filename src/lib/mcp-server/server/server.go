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
	"path"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/contractmd"
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
	// Purpose 是本次调用的运行目的（C1 2026-10-15）：= gateway 展示名参数 tool_call_display_name，
	// 由 doCall 剥离前取出、随调用上下文透传 → spawn executor 时落 CHONKPILOT_PURPOSE。
	Purpose string
	// 会话/轮次/调用 id（2026-10-19，core-mcp-server 域工具 executor）：meta/domain 控制面
	// 执行器经 plugin/v1/* 恢复 turn（ask_user / tool_result / subchat_*）。落 CHONKPILOT_*。
	SessionID  string
	TurnID     string
	ToolCallID string
	// TopSession / Parent（2026-10-19，dsl_run 独立 executor）：任务树归属（主会话 / 父节点），
	// 由 gateway 随 _meta 透传；spawn executor 时落 CHONKPILOT_TOP_SESSION / CHONKPILOT_PARENT。
	TopSession string
	Parent     string
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
	cx.Purpose, _ = raw["purpose"].(string)
	cx.SessionID, _ = raw["session"].(string)
	cx.TurnID, _ = raw["turn"].(string)
	cx.ToolCallID, _ = raw["tool_call_id"].(string)
	cx.TopSession, _ = raw["top_session"].(string)
	cx.Parent, _ = raw["parent"].(string)
	return cx
}

// err 校验调用上下文：存在但 instance 为空 → 异常（instance 不应为空）；无上下文 → nil。
func (cx CallContext) err() error {
	if cx.Present && strings.TrimSpace(cx.InstanceID) == "" {
		return paths.ErrNoInstance
	}
	return nil
}

// CallContextMeta 构造协议 _meta（命名空间 chonkpilot：instance_id/work_dir/data_dir/purpose）。
// 供调用链上游（gateway）在 tools/call 时透传调用上下文；第三方 client 不应携带。
// purpose（C1 2026-10-15）= 本次调用的运行目的，空串则不写该键（保持既有形态）。
func CallContextMeta(instanceID, workDir, dataDir, purpose string) mcp.Meta {
	return CallContextMetaFull(instanceID, workDir, dataDir, purpose, "", "", "")
}

// CallContextMetaFull 同 CallContextMeta，并携带会话/轮次/调用 id（域工具 executor 恢复 turn 用）。
// 空串键不写出（保持既有形态）。
func CallContextMetaFull(instanceID, workDir, dataDir, purpose, session, turn, toolCallID string) mcp.Meta {
	ns := map[string]any{
		"instance_id": instanceID,
		"work_dir":    workDir,
		"data_dir":    dataDir,
	}
	if purpose != "" {
		ns["purpose"] = purpose
	}
	if session != "" {
		ns["session"] = session
	}
	if turn != "" {
		ns["turn"] = turn
	}
	if toolCallID != "" {
		ns["tool_call_id"] = toolCallID
	}
	return mcp.Meta{metaNamespace: ns}
}

// CallContextMetaTree 同 CallContextMetaFull，并携带任务树归属（top_session / parent）。
// dsl_run 等 executor 据此把作业节点挂到正确的主会话 / 父节点（空串键不写出）。
func CallContextMetaTree(instanceID, workDir, dataDir, purpose, session, turn, toolCallID, topSession, parent string) mcp.Meta {
	meta := CallContextMetaFull(instanceID, workDir, dataDir, purpose, session, turn, toolCallID)
	ns, _ := meta[metaNamespace].(map[string]any)
	if ns == nil {
		return meta
	}
	if topSession != "" {
		ns["top_session"] = topSession
	}
	if parent != "" {
		ns["parent"] = parent
	}
	return meta
}

// RegisterContracts 扫描契约根 root（递归四后缀）并把原语注册进调用方传入的 go-sdk server：
//   - *.tool.md    → tool（含 executor handler，调用参数由调用方经 tools/call 传入）
//   - *.prompt.md  → prompt（_meta.type=prompt）
//   - *.skill.md   → prompt（_meta.type=skill，与 prompt 同构注册以便 prompts/list 区分）
//   - *.resource.md→ resource（URI 缺省补 file://<name>）
//
// 例外：category=server 且**无 runtime** 的 *.tool.md（当前无；`dsl_run` 已于 2026-10-19 外移为
// runtime executor）**不注册**（否则产出无 runtime 的占位工具 + 热重扫幽灵工具）；其定义经 ServerTools 单源提供。
// P6（2026-10-19）：cfg.SkipTools=true → **只注册资产**（prompts/skills/resources），跳过 tools
// （系统级 app 根工具改由外部进程经插件桥注册/执行）。
//
// root 缺失或为空目录 → 空注册（no-op，返回 nil），调用方决定空能力面语义。
// cfg 为执行配置（nil → DefaultConfig()）；ExecDir 已移除——exe 型 runtime 按契约文件
// 所在目录相对解析（见 resolveRuntime），解释器走 PATH。
func RegisterContracts(srv *mcp.Server, root string, cfg *Config) error {
	return registerContracts(srv, root, cfg, false)
}

// RegisterContractsWithTools 与 RegisterContracts 相同，但**忽略 cfg.SkipTools**（强制注册工具）。
// 用途（P6）：dir 节点扫描（用户/项目级 capability 根）仍由本进程执行工具，不受系统级 app 根
// 「工具外移」影响 —— 二者共享同一 cfg（I-82），故不能直接改 cfg 字段。
func RegisterContractsWithTools(srv *mcp.Server, root string, cfg *Config) error {
	return registerContracts(srv, root, cfg, true)
}

// registerContracts 是 RegisterContracts / RegisterContractsWithTools 的共同实现。
// forceTools=true → 忽略 cfg.SkipTools（dir 节点：不做 embed 兜底）。
func registerContracts(srv *mcp.Server, root string, cfg *Config, forceTools bool) error {
	if srv == nil {
		return fmt.Errorf("RegisterContracts: server is required")
	}
	if cfg == nil {
		cfg = DefaultConfig()
	}
	sources := contractSources(root, forceTools)
	if len(sources) == 0 {
		return nil // 契约根缺失且无内置兜底：空注册
	}
	files, shadowed, err := ScanAll(sources)
	if err != nil {
		return err
	}
	for _, sh := range shadowed {
		log.Printf("[mcp-server] contract shadowed: %s %q: %s(%s) overrides %s(%s)",
			sh.Kind, sh.Name, sh.Winner, sh.WinnerRel, sh.Loser, sh.LoserRel)
	}
	// 内嵌来源无磁盘路径：executor 相对解析基准按**部署根同位目录**补（部署布局与 embed 同构）。
	fillEmbeddedDirs(files, root)
	lim := cfg.ensureLimiter() // 并发限流器（tools/call 内生效；上限可被 prj max_concurrency 运行期覆盖）

	// tools（P6：外部工具模式下由外部进程注册执行 → 跳过）
	if forceTools || !cfg.SkipTools {
		toolDocs, err := parseTools(files)
		if err != nil {
			return fmt.Errorf("load tools: %w", err)
		}
		nTools := 0
		for _, td := range toolDocs {
			// server 类别且无 runtime（当前无；`dsl_run` 已外移为 runtime executor）：
			// 本包按其契约注册会产出**无 runtime 的不可用占位工具**，且 app 根热重扫时会在 self 节点
			// 复现为幽灵工具 —— 故**不注册**；其定义经 ServerTools 单源提供给装配层。
			// core-only（meta/domain/dsl_run）与 internal（插件查询工具）均不由进程内 mcp-server 注册：
			// 前者只在 core 暴露，后者只经 core 的 /internal/exec 执行。
			if isServerTool(td) || td.CoreOnly || td.Internal {
				continue
			}
			t, err := buildTool(td, cfg)
			if err != nil {
				return fmt.Errorf("build tool %s: %w", td.Name, err)
			}
			srv.AddTool(t, makeToolHandler(td, cfg, lim))
			nTools++
		}
		if nTools > 0 {
			log.Printf("[mcp-server] %s: %d tools", root, nTools)
		}
	}

	// prompts（*.prompt.md 注册为 prompt，_meta.type=prompt）
	promptDocs, err := parsePrompts(files)
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
	skillDocs, err := parseSkills(files)
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
	resDocs, err := parseResources(files)
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

// contractSources 决定本次注册的契约来源（GATEWAY-2 C）：
//   - 磁盘契约根存在 → **磁盘单层**（部署产物 = 权威；与改前行为一致）；
//   - 磁盘契约根缺失 → 内置 embed 层（`//go:embed all:contracts`，构建期同步副本）。
//
// dir 节点（user/project/prjusr capability 根）走 RegisterContractsWithTools（forceTools=true）
// → **不带** embed：dir 根缺失 = 空能力面（用户/项目目录由用户自建，不能回落系统内置集）。
func contractSources(root string, dirNode bool) []Source {
	if strings.TrimSpace(root) != "" {
		if _, err := os.Stat(root); err == nil {
			return []Source{{Label: "app", Dir: root}}
		}
	}
	if dirNode {
		return nil
	}
	if fsys := builtinContractFS(); fsys != nil {
		return []Source{{Label: "app-embed", FS: fsys}}
	}
	return nil
}

// fillEmbeddedDirs 给内嵌来源的契约补 executor 相对解析基准：
// 按**部署根同位目录**（root + 相对路径目录）——部署产物的 capability 布局与 embed 副本同构，
// 故内嵌契约里的相对 runtime（如 `../../executors/x.exe`）仍能落到部署目录。
func fillEmbeddedDirs(files []*ContractFile, root string) {
	if strings.TrimSpace(root) == "" {
		return
	}
	for _, f := range files {
		if f.Path != "" || f.Dir != "" {
			continue
		}
		f.Dir = filepath.Join(root, filepath.FromSlash(path.Dir(f.Rel)))
	}
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
		// 并发限流：达上限排队；请求 ctx 取消/超时 → 明确失败、不执行工具（B3）。
		if err := lim.acquire(ctx); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: wrapFail(err.Error())}},
				IsError: true,
			}, nil
		}
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

// makeResourceHandler 返回静态资源内容：① meta `path` 外部内容文件非空 → **实时读盘**取该文件
// 字节（文本 → Text、二进制 → Blob，内容不驻留）；② 否则来源契约文件路径（r.Path）非空 →
// **实时读盘**取「正文」段（内容不驻留）；③ 都没有 → 返回扫描期副本 r.Content（兼容兜底）。
func makeResourceHandler(r *ResourceDoc) mcp.ResourceHandler {
	return func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if r.File != "" {
			b, err := os.ReadFile(r.File)
			if err != nil {
				return nil, err
			}
			c := &mcp.ResourceContents{URI: r.URI, MIMEType: r.MIMEType}
			if contractmd.IsTextMime(r.MIMEType) {
				c.Text = string(b)
			} else {
				c.Blob = b
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{c}}, nil
		}
		text := r.Content
		if r.Path != "" {
			if b, err := os.ReadFile(r.Path); err == nil {
				if c, perr := contractmd.Parse(string(b)); perr == nil {
					text = c.Content
				}
			}
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{URI: r.URI, MIMEType: r.MIMEType, Text: text}},
		}, nil
	}
}
