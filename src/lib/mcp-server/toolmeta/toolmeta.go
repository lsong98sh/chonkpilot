// Package toolmeta 是 meta（mcp_find / mcp_load / mcp_invoke）与 domain（ask_user /
// next_run / tool_stop / tool_result / subchat_open / subchat_finish）控制面工具的
// **进程内**实现（GATEWAY-2 / L3）。
//
// 2026-10-19 这些工具曾是「一工具一 exe」（src/lib/llm/cmd/*-exec + servertools/exec），
// 由 core-mcp-server 每次 spawn；L3 起契约 runtime = builtin，由内置 mcp-server 进程内调用，
// 不再 spawn。执行语义与原 exe 逐行等价：经插件桥 `plugin/v1/*` 取 server 原子能力
// （turn / task 节点 / ask 通道）。
//
// 调用上下文由调用方显式传入（Context），插件桥地址/令牌亦作为参数传入（Bridge）——
// 本包**不读取任何 CHONKPILOT_* 环境变量**（进程内没有 executor 子进程环境）。
package toolmeta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/transport"
)

// pluginID 是插件桥身份（与既有 server-tools 一致：能力面审计/归属不变）。
const pluginID = "builtin.server-tools"

// Group 是工具分组（决定执行面）。
type Group string

const (
	// GroupMeta 是 meta 组（mcp_find / mcp_load / mcp_invoke）。
	GroupMeta Group = "meta"
	// GroupDomain 是 domain 组（ask_user / tool_result / tool_stop / next_run / subchat_*）。
	GroupDomain Group = "domain"
)

// Context 是本次工具调用的上下文字段（来自协议 _meta.chonkpilot）。
type Context struct {
	InstanceID string
	Session    string
	Turn       string
	ToolCallID string
	Purpose    string
}

// Bridge 是插件桥连接参数（core-mcp-server 的 Config.BridgeAddr / BridgeToken）。
// Token 仅为与宿主注入口径保持一致而保留；UDS/TCP 帧协议本身不消费。
type Bridge struct {
	Addr  string
	Token string
}

// Result 是执行结果（Text 原样文本；IsError 对齐旧 exe 的非 0 退出码）。
type Result struct {
	Text    string
	IsError bool
}

type call struct {
	tool string
	args map[string]any
	cx   Context
}

// Run 执行一个 meta / domain 控制面工具。
func Run(ctx context.Context, group Group, tool string, args map[string]any, cx Context, bridge Bridge) *Result {
	switch group {
	case GroupMeta:
		text, isErr := runMeta(ctx, tool, args, cx, bridge)
		return &Result{Text: text, IsError: isErr}
	case GroupDomain:
		text, isErr := runDomain(ctx, tool, args, cx, bridge)
		return &Result{Text: text, IsError: isErr}
	}
	return &Result{Text: "错误: 未知工具组 " + string(group), IsError: true}
}

// runMeta 执行一个 gateway 元工具，返回（文本, isError）。
func runMeta(ctx context.Context, tool string, args map[string]any, cx Context, bridge Bridge) (string, bool) {
	in := call{tool: tool, args: args, cx: cx}
	cli, err := dial(ctx, bridge)
	if err != nil {
		return "错误: " + err.Error(), true
	}
	defer cli.Close()
	switch tool {
	case "mcp_find":
		return metaFind(ctx, cli, in)
	case "mcp_load":
		return metaLoad(ctx, cli, in)
	case "mcp_invoke":
		return metaInvoke(ctx, cli, in)
	}
	return "错误: 未知元工具 " + tool, true
}

// runDomain 执行一个 server 域工具，返回（文本, isError）。
func runDomain(ctx context.Context, tool string, args map[string]any, cx Context, bridge Bridge) (string, bool) {
	in := call{tool: tool, args: args, cx: cx}
	switch tool {
	case "tool_stop", "tool_result", "ask_user", "next_run", "subchat_open", "subchat_finish":
	default:
		return "错误: 未知 server 工具 " + tool, true
	}
	cli, err := dial(ctx, bridge)
	if err != nil {
		return "错误: " + err.Error(), true
	}
	defer cli.Close()

	// 任务节点（对齐进程内 domainExecute）：先建节点再执行；ask_user 的节点由回答回填收尾。
	nodeOut, err := callPlugin(ctx, cli, "plugin/v1/task.node.start", map[string]any{
		"instance_id":  in.cx.InstanceID,
		"turn":         in.cx.Turn,
		"tool":         in.tool,
		"tool_call_id": in.cx.ToolCallID,
		"purpose":      in.cx.Purpose,
		"args":         in.args,
	})
	if err != nil {
		return "域工具必须经 server turn 调用（turn 上下文缺失）", true
	}
	taskID, _ := nodeOut["task_id"].(string)

	switch tool {
	case "ask_user":
		if _, err := callPlugin(ctx, cli, "plugin/v1/ask.user", map[string]any{
			"turn":         in.cx.Turn,
			"tool_call_id": in.cx.ToolCallID,
			"task_id":      taskID,
			"args":         in.args,
		}); err != nil {
			finishNode(ctx, cli, taskID, true, err.Error())
			return "错误: " + err.Error(), true
		}
		return "已向用户提问，等待回答", false

	case "tool_stop":
		out, err := callPlugin(ctx, cli, "plugin/v1/tool.stop", map[string]any{
			"instance_id": in.cx.InstanceID,
			"turn":        in.cx.Turn,
			"args":        in.args,
		})
		if err != nil {
			finishNode(ctx, cli, taskID, true, err.Error())
			return "错误: " + err.Error(), true
		}
		text := strArg(out, "text")
		finishNode(ctx, cli, taskID, false, text)
		// 进程内口径：task 型工具的失败记在任务节点上，工具结果本身仍为 complete。
		return text, false

	case "tool_result":
		return toolResult(ctx, cli, in.cx, in.args, taskID), false

	case "next_run":
		// NEXTRUN-1：登记下一轮排队输入（同步快操作；结果即终态）。
		out, err := callPlugin(ctx, cli, "plugin/v1/next.run", map[string]any{
			"instance_id": in.cx.InstanceID,
			"turn":        in.cx.Turn,
			"args":        in.args,
		})
		if err != nil {
			finishNode(ctx, cli, taskID, true, err.Error())
			return "错误: " + err.Error(), true
		}
		text := strArg(out, "text")
		isErr, _ := out["is_error"].(bool)
		finishNode(ctx, cli, taskID, isErr, text)
		return text, isErr

	case "subchat_open":
		out, err := callPlugin(ctx, cli, "plugin/v1/subchat.open", map[string]any{
			"instance_id": in.cx.InstanceID,
			"turn":        in.cx.Turn,
			"purpose":     in.cx.Purpose,
			"args":        in.args,
		})
		if err != nil {
			finishNode(ctx, cli, taskID, true, err.Error())
			return "错误: " + err.Error(), true
		}
		text := strArg(out, "text")
		finishNode(ctx, cli, taskID, false, text)
		return text, false

	default: // subchat_finish
		out, err := callPlugin(ctx, cli, "plugin/v1/subchat.finish", map[string]any{
			"instance_id": in.cx.InstanceID,
			"turn":        in.cx.Turn,
		})
		if err != nil {
			finishNode(ctx, cli, taskID, true, err.Error())
			return "错误: " + err.Error(), true
		}
		text := strArg(out, "text")
		finishNode(ctx, cli, taskID, false, text)
		return text, false
	}
}

// ── 元工具 ──────────────────────────────────────────────────

func metaFind(ctx context.Context, cli transport.Conn, in call) (string, bool) {
	inst := in.cx.InstanceID
	query := strArg(in.args, "query")
	purpose := strArg(in.args, "purpose")
	typ := strArg(in.args, "type")
	if typ == "" {
		typ = "all"
	}
	kinds, includeTools, ok := findTypeScope(typ)
	if !ok {
		return fmt.Sprintf("unknown type %q（支持：%s）", typ, findTypeNames()), true
	}
	limit := 20
	if l, ok := in.args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if purpose != "" {
		if res, ok := metaLLMPick(ctx, cli, inst, purpose, typ, limit); ok {
			return res, false
		}
		query = purpose
	}
	q := strings.ToLower(query)

	tools, _ := listTools(ctx, cli, inst)
	assets := listAssets(ctx, cli, inst, "")

	matches := make([]map[string]any, 0, limit)
	for _, t := range tools {
		if !includeTools || len(matches) >= limit {
			break
		}
		name := strArg(t, "name")
		desc := strArg(t, "description")
		if !matchQuery(name, desc, q) {
			continue
		}
		meta, _ := t["_meta"].(map[string]any)
		entry := map[string]any{
			"kind": "tool", "name": name, "description": desc,
			"category": meta["category"], "hot": meta["hot"],
		}
		if v, ok := t["inputSchema"]; ok && v != nil {
			entry["inputSchema"] = v
		}
		if srv, ok := meta["server"]; ok && srv != nil {
			entry["server"] = srv
		}
		matches = append(matches, entry)
	}
	for _, a := range assets {
		if len(matches) >= limit {
			break
		}
		if !kindInScope(kinds, strArg(a, "kind")) {
			continue
		}
		if !matchQuery(strArg(a, "name"), strArg(a, "description"), q) {
			continue
		}
		entry := map[string]any{
			"kind": strArg(a, "kind"), "name": strArg(a, "name"),
			"description": strArg(a, "description"), "category": strArg(a, "kind"),
		}
		if node := strArg(a, "node"); node != "" {
			entry["node"] = node
		}
		matches = append(matches, entry)
	}
	out, _ := json.Marshal(map[string]any{
		"query": query, "type": typ, "count": len(matches), "tools": matches,
	})
	return string(out), false
}

func metaLoad(ctx context.Context, cli transport.Conn, in call) (string, bool) {
	inst := in.cx.InstanceID
	name := strArg(in.args, "name")
	if name == "" {
		return "name is required", true
	}
	kind := strArg(in.args, "kind")
	if kind == "" {
		kind = "tool"
	}
	switch kind {
	case "tool":
		tools, _ := listTools(ctx, cli, inst)
		var hit map[string]any
		for _, t := range tools {
			if strArg(t, "name") != name {
				continue
			}
			if hit == nil || scopeOf(t) == inst {
				hit = t
			}
		}
		if hit == nil {
			return fmt.Sprintf("tool %q not found", name), true
		}
		meta, _ := hit["_meta"].(map[string]any)
		out := map[string]any{
			"kind": "tool", "name": name,
			"description": strArg(hit, "description"),
			"meta":        meta, "hot": meta["hot"],
		}
		if v, ok := hit["inputSchema"]; ok && v != nil {
			out["inputSchema"] = v
		}
		if srv, ok := meta["server"]; ok && srv != nil {
			out["server"] = srv
		}
		b, _ := json.Marshal(out)
		return string(b), false

	case "skill":
		asset := findAsset(ctx, cli, inst, "skill", name, "")
		if asset == nil {
			return fmt.Sprintf("asset %q (kind=skill) not found", name), true
		}
		out, err := callPlugin(ctx, cli, "plugin/v1/prompts.get", map[string]any{
			"name": name, "instance_id": inst,
		})
		if err != nil {
			return "错误: " + err.Error(), true
		}
		m := map[string]any{
			"kind": "skill", "name": name, "content": strArg(out, "content"),
			"node": strArg(asset, "node"), "scope": scopeOf(asset),
		}
		if d := strArg(asset, "description"); d != "" {
			m["description"] = d
		}
		b, _ := json.Marshal(m)
		return string(b), false

	case "resource":
		asset := findAsset(ctx, cli, inst, "resource", name, "")
		if asset == nil {
			return fmt.Sprintf("asset %q (kind=resource) not found", name), true
		}
		uri := strArg(asset, "uri")
		out, err := callPlugin(ctx, cli, "plugin/v1/resources.read", map[string]any{
			"uri": uri, "instance_id": inst,
		})
		if err != nil {
			return "错误: " + err.Error(), true
		}
		m := map[string]any{
			"kind": "resource", "name": name, "uri": uri,
			"node": strArg(asset, "node"), "scope": scopeOf(asset),
		}
		if mt := strArg(out, "mimeType"); mt != "" {
			m["mimetype"] = mt
		}
		if d := strArg(asset, "description"); d != "" {
			m["description"] = d
		}
		if c, ok := out["content"].(string); ok {
			m["content"] = c
		}
		b, _ := json.Marshal(m)
		return string(b), false
	}
	return fmt.Sprintf("unknown kind %q（支持：%s）", kind, findTypeNames()), true
}

// metaInvoke 经插件桥执行目标工具（async=never 强制同步；request_id 同步等待结果）。
func metaInvoke(ctx context.Context, cli transport.Conn, in call) (string, bool) {
	name := strArg(in.args, "name")
	if name == "" {
		return "name is required", true
	}
	args, _ := in.args["arguments"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	args["async"] = "never"
	reqID := "meta-invoke-" + in.cx.ToolCallID
	if in.cx.ToolCallID == "" {
		reqID = fmt.Sprintf("meta-invoke-%d", time.Now().UnixNano())
	}
	out, err := callPlugin(ctx, cli, "plugin/v1/tools.call", map[string]any{
		"name": name, "arguments": args,
		"instance_id":  in.cx.InstanceID,
		"session":      in.cx.Session,
		"turn":         in.cx.Turn,
		"tool_call_id": in.cx.ToolCallID,
		"purpose":      in.cx.Purpose,
		"request_id":   reqID,
	})
	if err != nil {
		return "错误: " + err.Error(), true
	}
	if content, ok := out["content"]; ok {
		isErr, _ := out["isError"].(bool)
		return flattenContent(content, isErr)
	}
	b, _ := json.Marshal(out)
	return string(b), false
}

// flattenContent 把 MCP content 列表压成文本（单文本项优先；其余 JSON 化）。
func flattenContent(content any, isErr bool) (string, bool) {
	if items, ok := content.([]any); ok && len(items) == 1 {
		if m, ok := items[0].(map[string]any); ok {
			if t, ok := m["text"].(string); ok {
				return t, isErr
			}
		}
	}
	b, _ := json.Marshal(content)
	return string(b), isErr
}

// ── capability 辅助（与 server-tools 同口径）──────────────

func listTools(ctx context.Context, cli transport.Conn, inst string) ([]map[string]any, error) {
	out, err := callPlugin(ctx, cli, "plugin/v1/tools.list", map[string]any{"instance_id": inst})
	if err != nil {
		return nil, err
	}
	return objList(out["tools"]), nil
}

func listAssets(ctx context.Context, cli transport.Conn, inst, kind string) []map[string]any {
	var out []map[string]any
	seen := map[string]bool{}
	add := func(k, name, desc, uri, node, scope string) {
		if k == "" || name == "" || !visibleScope(scope, inst) {
			return
		}
		key := k + "|" + name
		if seen[key] {
			return
		}
		seen[key] = true
		m := map[string]any{"kind": k, "name": name, "description": desc, "node": node, "scope": scope}
		if uri != "" {
			m["uri"] = uri
		}
		out = append(out, m)
	}
	if kind == "" || kind == "skill" {
		if p, err := callPlugin(ctx, cli, "plugin/v1/prompts.list", map[string]any{}); err == nil {
			for _, e := range objList(p["prompts"]) {
				if strArg(e, "type") != "skill" {
					continue
				}
				add("skill", strArg(e, "name"), strArg(e, "description"), "", strArg(e, "node"), scopeOf(e))
			}
		}
	}
	if kind == "" || kind == "resource" {
		if r, err := callPlugin(ctx, cli, "plugin/v1/resources.list", map[string]any{}); err == nil {
			for _, e := range objList(r["resources"]) {
				add("resource", strArg(e, "name"), strArg(e, "description"), strArg(e, "uri"), strArg(e, "node"), scopeOf(e))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if strArg(out[i], "kind") != strArg(out[j], "kind") {
			return strArg(out[i], "kind") < strArg(out[j], "kind")
		}
		return strArg(out[i], "name") < strArg(out[j], "name")
	})
	return out
}

func findAsset(ctx context.Context, cli transport.Conn, inst, kind, name, uri string) map[string]any {
	var fallback map[string]any
	for _, a := range listAssets(ctx, cli, inst, kind) {
		if strArg(a, "kind") != kind {
			continue
		}
		if name != "" && strArg(a, "name") != name {
			continue
		}
		if uri != "" && strArg(a, "uri") != uri {
			continue
		}
		if scopeOf(a) == inst {
			return a
		}
		if fallback == nil {
			fallback = a
		}
	}
	return fallback
}

func metaLLMPick(ctx context.Context, cli transport.Conn, inst, purpose, typ string, limit int) (string, bool) {
	kinds, includeTools, ok := findTypeScope(typ)
	if !ok {
		return "", false
	}
	var sb strings.Builder
	sb.WriteString("当前可用候选条目（name 或 name(kind): 描述）：\n")
	count := 0
	appendLine := func(line string) {
		if count >= 200 {
			return
		}
		sb.WriteString(line)
		count++
	}
	if includeTools {
		tools, _ := listTools(ctx, cli, inst)
		for _, t := range tools {
			desc := strArg(t, "description")
			if len(desc) > 120 {
				desc = desc[:120]
			}
			appendLine(fmt.Sprintf("- %s: %s\n", strArg(t, "name"), desc))
		}
	}
	for _, a := range listAssets(ctx, cli, inst, "") {
		if !kindInScope(kinds, strArg(a, "kind")) {
			continue
		}
		desc := strArg(a, "description")
		if len(desc) > 120 {
			desc = desc[:120]
		}
		appendLine(fmt.Sprintf("- %s(%s): %s\n", strArg(a, "name"), strArg(a, "kind"), desc))
	}
	prompt := fmt.Sprintf("任务目标：%s\n请从候选条目中选择完成该任务最相关者（可多个/组合，按相关性排序，至多 %d 个，条目可为工具或资产），"+
		"输出 JSON {\"tools\":[{\"name\":\"\",\"reason\":\"\"}]}（无匹配输出 {\"tools\":[]}），仅输出 JSON。", purpose, limit)
	lctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := callPlugin(lctx, cli, "plugin/v1/llm.step", map[string]any{
		"prompt": prompt, "system": sb.String(), "instance_id": inst,
	})
	if err != nil {
		return "", false
	}
	if text := strArg(out, "text"); text != "" {
		return text, true
	}
	return "", false
}

// ── 域工具辅助 ──────────────────────────────────────────────

// toolResult 对齐进程内 runToolResult：id 支持 server 任务节点 / gateway 异步任务 /
// 子会话 turn id；timeout 秒内轮询至终态（0 = 立即查询一次），超时返回「任务尚未结束」。
func toolResult(ctx context.Context, cli transport.Conn, cx Context, args map[string]any, taskID string) string {
	id, _ := args["id"].(string)
	if id == "" {
		finishNode(ctx, cli, taskID, true, "错误: id 必填")
		return "错误: id 必填"
	}
	timeout := 30.0
	if v, ok := args["timeout"].(float64); ok {
		timeout = v
	}
	if timeout < 0 {
		timeout = 0
	}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for {
		if ctx.Err() != nil {
			msg := fmt.Sprintf("已取消: 任务 %s 查询中止（gateway 执行侧取消；被查询任务本身未受影响，可重新调用 tool_result）", id)
			finishNode(ctx, cli, taskID, false, msg)
			return msg
		}
		out, err := callPlugin(ctx, cli, "plugin/v1/task.result", map[string]any{
			"instance_id": cx.InstanceID, "turn": cx.Turn, "id": id,
		})
		if err != nil {
			finishNode(ctx, cli, taskID, true, err.Error())
			return "错误: " + err.Error()
		}
		text := strArg(out, "text")
		state := strArg(out, "state")
		found, _ := out["found"].(bool)
		if !found {
			msg := "错误: 未找到任务 " + id
			finishNode(ctx, cli, taskID, true, msg)
			return msg
		}
		if state == "running" {
			if time.Now().After(deadline) {
				msg := fmt.Sprintf("任务尚未结束：%s 仍在运行（已等待 %.0fs）。可再次调用 tool_result(id=%q, timeout=更长秒数) 继续等待", id, timeout, id)
				finishNode(ctx, cli, taskID, false, msg)
				return msg
			}
			select {
			case <-ctx.Done():
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		finishNode(ctx, cli, taskID, false, text)
		return text
	}
}

// finishNode 收尾任务节点（对齐进程内 tasks.done：error 态写 error 文本，done 态写 result）。
func finishNode(ctx context.Context, cli transport.Conn, taskID string, isErr bool, text string) {
	if taskID == "" {
		return
	}
	state, result, errText := "done", text, ""
	if isErr {
		state, result, errText = "error", "", text
	}
	// 收尾不随执行 ctx 取消（tool_result 被取消时仍需落终态）。
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = callPlugin(callCtx, cli, "plugin/v1/task.node.done", map[string]any{
		"task_id": taskID, "state": state, "result": result, "error": errText,
	})
}

// ── 通用小工具 ───────────────────────────────────────────

// dial 按显式桥地址连接插件桥（不读进程环境：进程内调用无 executor 子进程环境）。
func dial(ctx context.Context, bridge Bridge) (transport.Conn, error) {
	addr := strings.TrimSpace(bridge.Addr)
	opts := transport.Options{Addr: addr}
	if strings.HasPrefix(addr, "tcp://") {
		opts.Backend = transport.BackendTCP
		opts.Addr = strings.TrimPrefix(addr, "tcp://")
	}
	c, err := transport.Dial(ctx, opts)
	if err != nil {
		return nil, err
	}
	if _, err := callPlugin(ctx, c, "plugin.hello", map[string]any{"id": pluginID}); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func callPlugin(ctx context.Context, cli transport.Conn, method string, args any) (map[string]any, error) {
	res, err := cli.Call(ctx, method, args)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		if len(res.Errors) > 0 {
			return nil, errors.New(res.Errors[0])
		}
		return nil, errors.New("调用失败")
	}
	m, _ := res.Value.(map[string]any)
	return m, nil
}

func findTypeScope(typ string) (kinds []string, includeTools bool, ok bool) {
	switch typ {
	case "", "all":
		return []string{"skill", "resource"}, true, true
	case "tool":
		return nil, true, true
	case "skill", "resource":
		return []string{typ}, false, true
	}
	return nil, false, false
}

func findTypeNames() string { return "tool / skill / resource / all" }

func kindInScope(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func visibleScope(scope, inst string) bool {
	return scope == "" || scope == "global" || scope == inst
}

func scopeOf(m map[string]any) string { return strArg(m, "scope") }

func matchQuery(name, desc, q string) bool {
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(name), q) || strings.Contains(strings.ToLower(desc), q)
}

func strArg(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func objList(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
