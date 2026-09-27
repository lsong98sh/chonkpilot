// callgate.go：gateway 工具面——8 个查询工具的定义/注册/注销，与执行回调（onToolCall）。
//
// 工具契约（与引擎 chonkpilot-codegraph-mcp-server 同名同描述，但 schema 去掉 workdir）：
//   - 对外工具名与引擎完全一致（codegraph_ 前缀），LLM 无需感知 workdir（插件按回调
//     context.instance_id 定位 workdir 后注入）；
//   - 管理工具（codegraph_configure/initialize/status）只由插件直接调引擎，不注册给 gateway；
//   - 注册/注销走 gateway 方法面 tools/register|unregister（mcp-tools-register/unregister），
//     payload 字段名对齐 mcpgateway.regMsg：name/description/schema/handler_subject/owner/hot/category。
package codegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// gatewayTool 注册到 gateway 的查询工具定义。
type gatewayTool struct {
	name        string
	description string
	props       map[string]any // properties（不含 workdir；引擎查询工具仅 workdir 为必填 → 无 required）
}

// schemaJSON 序列化 JSON Schema 文本（{"type":"object","properties":{...}}）。
func (t *gatewayTool) schemaJSON() string {
	schema := map[string]any{"type": "object", "properties": t.props}
	b, _ := json.Marshal(schema)
	return string(b)
}

// queryTools 对外查询工具全集（v1；对齐引擎 toolSpecs hot=true 子集，逐个去掉 workdir 参数）。
var queryTools = []gatewayTool{
	{
		name:        "codegraph_symbol_search",
		description: "在已索引的某项目内按符号名（子串/前缀，忽略大小写）与 kind/文件过滤搜索。kind: func/method/class/type/interface/enum/trait/constructor。",
		props: map[string]any{
			"query": map[string]any{"type": "string", "description": "符号名关键字"},
			"kind":  map[string]any{"type": "string", "description": "符号类别过滤"},
			"file":  map[string]any{"type": "string", "description": "文件路径子串过滤"},
			"limit": map[string]any{"type": "integer", "description": "返回上限（默认 50）"},
		},
	},
	{
		name:        "codegraph_get_symbol_info",
		description: "查询单个符号详情（id 或 file+name 定位）：kind/行号/复杂度/签名。",
		props: map[string]any{
			"id":   map[string]any{"type": "string", "description": "symbol_search 返回的 id（file:line:name:kind）"},
			"file": map[string]any{"type": "string", "description": "文件绝对路径"},
			"name": map[string]any{"type": "string", "description": "符号名"},
		},
	},
	{
		name:        "codegraph_callers",
		description: "查「谁调用了它」：在已索引项目内按被调名（精确名或最后一段，忽略大小写，故 pkg.Foo 与 Foo 同目标）找调用方符号。语义为调用点文本的**名字级启发式**（无类型/重载解析，仅覆盖已索引文件中的直接调用）。",
		props: map[string]any{
			"name":  map[string]any{"type": "string", "description": "被调名（如 Foo 或 pkg.Foo）"},
			"id":    map[string]any{"type": "string", "description": "目标符号 id（file:line:name:kind）；与 name 二选一，id 优先"},
			"file":  map[string]any{"type": "string", "description": "只保留该文件内的调用方（路径子串过滤）"},
			"limit": map[string]any{"type": "integer", "description": "返回上限（默认 50）"},
		},
	},
	{
		name:        "codegraph_callees",
		description: "查「它调用了谁」：按 id 或 file+name 定位符号，返回其直接调用的目标名（含 resolved 与解析到的 file/line）。语义为调用点文本的**名字级启发式**（无类型/重载解析）。",
		props: map[string]any{
			"id":    map[string]any{"type": "string", "description": "符号 id（file:line:name:kind）"},
			"file":  map[string]any{"type": "string", "description": "符号所在文件（配 name 定位）"},
			"name":  map[string]any{"type": "string", "description": "符号名（配 file 定位）"},
			"limit": map[string]any{"type": "integer", "description": "返回上限（默认 50）"},
		},
	},
	{
		name:        "codegraph_get_dependency_graph",
		description: "某项目的文件级依赖：文件 → import/use/require 目标（原始串）。file 过滤可单查。",
		props: map[string]any{
			"file": map[string]any{"type": "string", "description": "只返回该文件的依赖"},
		},
	},
	{
		name:        "codegraph_find_circular_deps",
		description: "在可解析为仓库内文件的导入边上找环（JS/TS 相对、Go module 前缀、Py/Java 包路径、Rust crate 尽力）；解析不到的不参与。",
		props:       map[string]any{},
	},
	{
		name:        "codegraph_analyze_complexity",
		description: "圈复杂度分析：默认返回项目内复杂度最高 topN 的 func/method；file 限定单文件。复杂度为决策点启发式。",
		props: map[string]any{
			"file":  map[string]any{"type": "string", "description": "只看该文件"},
			"topN":  map[string]any{"type": "integer", "description": "返回前 N（默认 20）"},
			"minCc": map[string]any{"type": "integer", "description": "最低复杂度（默认 1）"},
		},
	},
	{
		name:        "codegraph_get_module_summary",
		description: "目录/项目摘要：文件数、语言分布、符号数、最高复杂度函数（基于索引）。",
		props: map[string]any{
			"path": map[string]any{"type": "string", "description": "目录前缀（可选）"},
		},
	},
}

// registerAll 逐个向 gateway 注册 8 个查询工具（同主题 promise：await v.Result/v.Err）。
func (p *Codegraph) registerAll() error {
	ctx, cancel := context.WithTimeout(context.Background(), dataTimeout)
	defer cancel()
	var errs []string
	for _, t := range queryTools {
		payload := map[string]any{
			"name":            t.name,
			"description":     t.description,
			"schema":          t.schemaJSON(),
			"handler_subject": toolCallSubject,
			"owner":           "codegraph",
			"hot":             true,
			"category":        "codegraph",
		}
		v := p.deps.Bus.Emit(ctx, subjectToolRegister, payload).Wait()
		if e := v.Err(); e != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.name, e))
			continue
		}
		if v.Result == nil {
			errs = append(errs, t.name+": gateway 未应答（未就绪？）")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// unregisterAll 逐个注销（kind=tool；gateway handleUnregister → unregisterTool）。
func (p *Codegraph) unregisterAll() error {
	ctx, cancel := context.WithTimeout(context.Background(), dataTimeout)
	defer cancel()
	var errs []string
	for _, t := range queryTools {
		payload := map[string]any{
			"name": t.name,
			"kind": "tool",
		}
		v := p.deps.Bus.Emit(ctx, subjectToolUnregister, payload).Wait()
		if e := v.Err(); e != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.name, e))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// toolCallMsg 是 gateway regProv 发往本插件回调主题的载荷（{tool, args, context}）。
type toolCallMsg struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
	Ctx  *struct {
		Turn       string `json:"turn"`
		Session    string `json:"session"`
		InstanceID string `json:"instance_id"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"context"`
}

// toolReply 构造 gateway regProv 期望的 v.Result（对齐 llm domainmcp 写回形态）。
func toolReply(text string, isErr bool) map[string]any {
	return map[string]any{
		"resultType": "complete",
		"content":    []any{map[string]any{"type": "text", "text": text}},
		"isError":    isErr,
	}
}

// onToolCall 执行 gateway 工具回调：instance → workdir → workMap 定位记录；
// 记录不存在或未启用 → 提示文本；否则注入 workdir 调**该 workdir 独立**的引擎子进程
// 并原样回传文本/isErr。同一 workdir 的调用经 rec.cmu 串行；不同 workdir 互不阻塞
// （多 workdir 场景下某 workdir 的长索引不再阻塞其它 workdir 的查询）。
func (p *Codegraph) onToolCall(_ context.Context, _ string, v *mq.Value) error {
	reply := func(text string, isErr bool) error {
		v.Result = toolReply(text, isErr)
		return nil
	}
	var msg toolCallMsg
	if err := json.Unmarshal(v.Payload, &msg); err != nil {
		return reply("codegraph: 回调载荷解析失败: "+err.Error(), true)
	}
	if msg.Tool == "" {
		return reply("codegraph: 回调载荷缺 tool", true)
	}
	// 定位 instance（context.instance_id 为主；args 内 instance_id 兜底）
	inst := ""
	if msg.Ctx != nil {
		inst = msg.Ctx.InstanceID
	}
	if inst == "" {
		if s, ok := msg.Args["instance_id"].(string); ok {
			inst = s
		}
	}
	if inst == "" {
		return reply("codegraph: 无法定位调用实例（context.instance_id 缺失）", true)
	}

	p.mu.Lock()
	ir, ok := p.insts[inst]
	if !ok {
		p.mu.Unlock()
		return reply("codegraph: 未知实例 "+inst, true)
	}
	r := p.works[ir.workdir]
	enabled := r != nil && r.enabled
	p.mu.Unlock()
	if r == nil || !enabled {
		return reply("codegraph: 该项目未启用（enable-codegraph=false）", true)
	}

	// 拷贝 args 并注入 workdir（不改 gateway 传入的原始 map）
	args := make(map[string]any, len(msg.Args)+1)
	for k, val := range msg.Args {
		args[k] = val
	}
	args["workdir"] = r.workDir

	r.cmu.Lock()
	defer r.cmu.Unlock()
	if r.busy { // 该 workdir 正在后台 configure/initialize：拒绝并发查询（引擎单会话串行更稳）
		return reply("codegraph: 该项目索引正在初始化中，请稍后重试（codegraph_status 可查进度）", false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	text, isErr, err := p.clientFor(r.workDir).call(ctx, msg.Tool, args)
	cancel()
	if err != nil {
		p.logf("codegraph: %s 调用引擎失败（workdir=%s）：%v", msg.Tool, r.workDir, err)
		return reply("codegraph: 引擎调用失败: "+err.Error(), true)
	}
	// 查询未就绪（not_initialized/indexing/error 等结构化 JSON）→ 原样透传，可附说明；
	// 索引未初始化不自动补 init（自动初始化仅由启用流程触发）。
	if strings.Contains(text, "not_initialized") {
		text += "\n（提示：codegraph 索引尚未初始化/正在初始化——请确认项目配置已开启 codegraph 后稍候重试）"
	}
	return reply(text, isErr)
}
