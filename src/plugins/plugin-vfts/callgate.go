// callgate.go：gateway 工具面——查询工具的定义/注册/注销，与执行回调（onToolCall）。
//
// 工具契约（与引擎 chonkpilot-vfts-mcp-server 同名同描述，但 schema 去掉 workdir）：
//   - 对外工具名与引擎一致（vfts_ 前缀），LLM 无需感知 workdir（插件按回调
//     context.instance_id 定位 workdir 后注入）；
//   - 管理工具（vfts_configure/index/status）只由插件直接调引擎，不注册给 gateway；
//   - 注册/注销走 gateway 方法面 tools/register|unregister（mcp-tools-register/unregister），
//     payload 字段名对齐 mcpgateway.regMsg：name/description/schema/handler_subject/owner/hot/category。
package vfts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// 索引库被另一进程占用（跨进程排他 LOCK）时的有界等待与失败文本
// （2026-10-19 用户定稿：等 2-3 秒仍失败 → 工具失败「正在索引，不可用」）。
const (
	indexBusyWait    = 3 * time.Second
	indexBusyPoll    = 200 * time.Millisecond
	indexBusyMessage = "正在索引，不可用（索引库被另一进程占用，已等待约 3 秒，请稍后重试）"
)

// isIndexBusyText 判定引擎错误文本是否为索引库被占用。
func isIndexBusyText(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "lock") || strings.Contains(s, "timeout") ||
		strings.Contains(s, "being used by another process") || strings.Contains(s, "另一个进程")
}

// isIndexBusyReply 判定「结构化就绪门应答」是否表示库被占用：引擎把 EnsureReady 的
// 瞬时 lock 错误折叠成 `{status:"error", message:"...lock..."}`（isErr=false）。
func isIndexBusyReply(s string) bool {
	if len(s) == 0 || s[0] != '{' {
		return false
	}
	var gate struct {
		Status  string `json:"status"`
		State   string `json:"state"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(s), &gate) != nil {
		return false
	}
	if gate.Status != "error" && gate.State != "error" {
		return false
	}
	return isIndexBusyText(gate.Message)
}

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

// queryTools 对外查询工具全集（v1；对齐引擎 toolSpecs hot=true 子集，去掉 workdir 参数）。
var queryTools = []gatewayTool{
	{
		name:        "vfts_query",
		description: "在某 workdir 的 vfts 全文索引中检索：match 为自然语言匹配串，query 为布尔/高级表达式（二者至少给其一，query 不支持字段前缀）；返回命中文件 + 片段/行号（topK，按相关度）。",
		props: map[string]any{
			"match": map[string]any{"type": "string", "description": "自然语言匹配串（如：全文索引）"},
			"query": map[string]any{"type": "string", "description": "布尔/高级表达式（如：fox AND brown；不支持 content: 字段前缀）"},
			"topK":  map[string]any{"type": "integer", "description": "返回命中文件数上限（默认 20）"},
			"path":  map[string]any{"type": "string", "description": "文件路径子串过滤（可选）"},
		},
	},
}

// registerAll 逐个向 gateway 注册查询工具（同主题 promise：await v.Result/v.Err）。
func (p *Vfts) registerAll() error {
	ctx, cancel := context.WithTimeout(context.Background(), dataTimeout)
	defer cancel()
	var errs []string
	for _, t := range queryTools {
		payload := map[string]any{
			"name":            t.name,
			"description":     t.description,
			"schema":          t.schemaJSON(),
			"handler_subject": toolCallSubject,
			"owner":           "vfts",
			"hot":             true,
			"category":        "vfts",
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
func (p *Vfts) unregisterAll() error {
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
		Purpose    string `json:"purpose"`
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
// 记录不存在或未启用 → 提示文本；否则注入 workdir 调引擎并原样回传文本/isErr。
// 同一 workdir 的 client 经 rec.cmu 串行调用。
func (p *Vfts) onToolCall(ctx context.Context, _ string, v *mq.Value) error {
	reply := func(text string, isErr bool) error {
		v.Result = toolReply(text, isErr)
		return nil
	}
	var msg toolCallMsg
	if err := json.Unmarshal(v.Payload, &msg); err != nil {
		return reply("vfts: 回调载荷解析失败: "+err.Error(), true)
	}
	if msg.Tool == "" {
		return reply("vfts: 回调载荷缺 tool", true)
	}
	if p.engineMissing {
		return reply("vfts: 引擎不可用（未就绪）", true)
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
		return reply("vfts: 无法定位调用实例（context.instance_id 缺失）", true)
	}

	p.mu.Lock()
	ir, ok := p.insts[inst]
	if !ok {
		p.mu.Unlock()
		return reply("vfts: 未知实例 "+inst, true)
	}
	r := p.works[ir.workdir]
	enabled := r != nil && r.enabled
	p.mu.Unlock()
	if r == nil || !enabled {
		return reply("vfts: 该项目未启用（enable-vfts=false）", true)
	}

	// 拷贝 args 并注入 workdir（不改 gateway 传入的原始 map）
	args := make(map[string]any, len(msg.Args)+1)
	for k, val := range msg.Args {
		args[k] = val
	}
	args["workdir"] = r.workDir

	r.cmu.Lock()
	defer r.cmu.Unlock()
	if r.busy { // 该 workdir 正在后台 configure/index：拒绝并发查询（引擎单会话串行更稳）
		return reply("vfts: 该项目索引正在初始化中，请稍后重试（vfts_status 可查进度）", false)
	}
	// 查询未就绪（not_initialized/indexing/error 等结构化 JSON）→ 原样透传，可附说明；
	// 索引未初始化不自动补 index（自动初始化仅由启用流程触发）。
	// 按结构化应答字段判定（而非子串匹配）——命中文件内容含 "not_initialized" 的正常结果不再误附提示。
	annotate := func(text string) string {
		var gate struct {
			Status string `json:"status"`
			State  string `json:"state"`
		}
		if len(text) > 0 && text[0] == '{' &&
			json.Unmarshal([]byte(text), &gate) == nil &&
			(gate.Status == "not_initialized" || gate.State == "not_initialized") {
			text += "\n（提示：vfts 全文索引尚未初始化/正在初始化——请确认项目配置已开启 vfts 后稍候重试）"
		}
		return text
	}

	// 委托 core-mcp-server（一工具一 exe，取消只杀本次 executor）；core 未就绪/不可达 → 回落本地引擎。
	if p.toolExec != nil {
		if te := p.toolExec(); te != nil {
			var session, turn, callID, purpose string
			if msg.Ctx != nil {
				session, turn, callID, purpose = msg.Ctx.Session, msg.Ctx.Turn, msg.Ctx.ToolCallID, msg.Ctx.Purpose
			}
			text, isErr, terr := te.Exec(ctx, msg.Tool, args, plugin.ExecContext{
				InstanceID: inst,
				Session:    session,
				Turn:       turn,
				ToolCallID: callID,
				WorkDir:    r.workDir,
				DataDir:    r.dataDir,
				Purpose:    purpose,
			})
			if terr == nil {
				return reply(annotate(text), isErr)
			}
			if ctx.Err() != nil {
				return reply("vfts: 调用已取消", true)
			}
			p.logf("vfts: %s 经 core 执行失败 → 回落本地引擎：%v", msg.Tool, terr)
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	c := p.sharedClient()
	if c == nil { // Stop 后共享 client 置 nil：在飞回调不得解引用
		cancel()
		return reply("vfts: 插件已停止", true)
	}
	// 本地引擎回落：索引库可能被 resident indexer / 管理动作短窗口占用 → 有界等待。
	deadline := time.Now().Add(indexBusyWait)
	var text string
	var isErr bool
	var err error
	for {
		text, isErr, err = c.call(callCtx, msg.Tool, args)
		busy := (err != nil && isIndexBusyText(err.Error())) || (isErr && isIndexBusyText(text)) || isIndexBusyReply(text)
		if !busy || !time.Now().Before(deadline) || callCtx.Err() != nil {
			break
		}
		select {
		case <-callCtx.Done():
		case <-time.After(indexBusyPoll):
		}
	}
	cancel()
	if err != nil {
		if isIndexBusyText(err.Error()) {
			return reply(indexBusyMessage, true)
		}
		p.logf("vfts: %s 调用引擎失败（workdir=%s）：%v", msg.Tool, r.workDir, err)
		return reply("vfts: 引擎调用失败: "+err.Error(), true)
	}
	if (isErr && isIndexBusyText(text)) || isIndexBusyReply(text) {
		return reply(indexBusyMessage, true)
	}
	return reply(annotate(text), isErr)
}
