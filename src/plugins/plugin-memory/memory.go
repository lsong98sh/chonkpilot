// Package memory 是记忆库沉淀插件（server 内嵌形态，对齐 docs/spec/20-modules/28-plugins.md）。
//
// 定位（42 §2 (27) / 41 D-16）：**每轮（turn 结束）异步**把本轮新信息沉淀进记忆库，
// 与 capability 原语库（LLM 经 mcp_find 获取的"普通知识"）无关。
//
// 触发：订阅 server 每轮终态事件 `session-compress`（与压缩插件同源，**消息面零新增**；
// 事件由 server.finish 在落库 + 写快照后发出）。流程：
//
//	读项目配置 memory.enabled（默认 false）→ 关则不动
//	→ 读本轮消息（data-session-load-messages）估算 token
//	→ 低于 memory.min-turn-tokens（默认 200）→ 跳过（寒暄类不产生记忆）
//	→ 取类别清单（data-memory-list；首次访问由 persist 预置类别文件）
//	→ 按启用类别**并行**：读旧全文（data-memory-read）+ 本轮新信息 → 经 llm-simple 重写全文
//	   （请求按 usr `llm.memory` 带可选 `llm` = 子系统默认 LLM，缺省回落 defaultLLM；SL-3；
//	    `system` = 该类别的沉淀提示词：项目级读 prj `memory.prompt.<类别名>`、用户偏好读 usr
//	    自由键 `memory_prompts`，未配置回落内置默认 defaultRewriteSystemPrompt）
//	   → 保存（data-memory-save）
//
// 手动沉淀（2026-09-20，批 3 ⑱）：另订阅点分主题 `memory.flush`（payload `{instance_id, session}`），
// 取该会话**最近一轮**（data-session-history）→ 复用同一沉淀回路（distill），**不受
// memory.min-turn-tokens 门控**（显式动作即用户意图），同步把 `{ok, saved, failed, enabled}`
// 写回 Value.Result 供设置页反馈；结果为空/未启用等前置不满足 → `{ok:false, reason}`（不静默）。
//
// 不阻塞对话：任一环节失败只记日志 + 上报一次用户可见提示（Deps.Notify），跳过该类，
// 不降级、不抛出（宿主负责提示的去重/限频，见 chonkpilot-llm/server/pluginnotice.go）。
package memory

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// 订阅/请求主题（相对主题；chonk. 前缀在 mq 初始化注入一次）。
const (
	// turnEndSubject 每轮终态事件（server.finish 落库后发；同 compress 订阅源，不新增消息面）。
	turnEndSubject = "session-compress"
	// flushSubject 手动触发一次沉淀（点分相对主题；桥/服务端「点分直通总线」分支受理 →
	// 不属 data-* 数据面，故由本插件订阅应答）。payload `{instance_id, session}`
	// （instance_id 由前端 mq / 桥统一注入）；作用域 = 当前 instance 的**指定会话**，
	// 不跨实例、不跨会话（61-消息一览 §7.1 插件视图新增行，草稿见任务报告）。
	flushSubject = "memory.flush"
	// 数据面（persist 订阅）。
	prjConfigListSubject  = "data-prj-config-list"
	userConfigLoadSubject = "data-user-config-load"
	sessionLoadSubject    = "data-session-load-messages"
	sessionHistorySubject = "data-session-history"
	memoryListSubject     = "data-memory-list"
	memoryReadSubject     = "data-memory-read"
	memorySaveSubject     = "data-memory-save"
	llmSimpleSubject      = "llm-simple"
	dataTimeout           = 5 * time.Second
	llmTimeout            = 60 * time.Second
	memoryUserLevel       = "user" // 记忆类别的"用户级"标识（persist 侧 memory-categories 的 level 值）
	memoryEnabledKey      = "memory.enabled"
	memoryMinTokensKey    = "memory.min-turn-tokens"
	// memoryLLMKey 是记忆子系统的默认 LLM 配置键（usr；SL-3 / SL-C2，40-演进计划 §SL）。
	// 值 = llmref（provider name；键缺失/空串的回落 defaultLLM 由数据层读侧完成，见 SL-1）。
	memoryLLMKey = "llm.memory"
	// memoryCategoryMaxTokensKey 是**单类别 token 告警阈值**（prj 键，见 64-配置项一览 §4.1）：
	// 语义与前端一致 —— **仅告警、不截断、不阻断沉淀**（前端按 data-memory-list 的 tokens 标红，
	// 本插件在沉淀后按同一口径记告警日志，使该键在**服务端亦有读点**；键缺失/非正 → 不限）。
	memoryCategoryMaxTokensKey = "memory.category-max-tokens"
	memoryCategoryPrefix       = "memory.category."
	// memoryPromptPrefix 是**项目级类别沉淀提示词**的 prj 配置键前缀：`memory.prompt.<类别名>`
	// （值 = 提示词全文；键缺失/空 → 回落内置默认 defaultRewriteSystemPrompt）。
	// 前端入口 = 上下文管理页每类别行的【编辑提示词】（走既有 data-prj-config-{list,save,delete}）。
	memoryPromptPrefix = "memory.prompt."
	// userMemoryPromptsKey 是**用户级类别沉淀提示词**的 usr 自由键（JSON 对象字符串
	// `{"<类别名>":"<提示词全文>"}`，见 64-配置项一览 §3）：承载唯一用户级类别「用户偏好」的
	// 自定义提示词（**键缺失/空/非法 JSON → 回落内置默认**）。读写走既有
	// data-user-config-{load,save,delete}（自由键通道）。
	userMemoryPromptsKey = "memory_prompts"
	defaultMinTurnTokens = 200
	// defaultRewriteSystemPrompt 是记忆沉淀的**内置默认**提示词：某类别未自定义提示词时使用
	// （自定义来源 = prj `memory.prompt.<类别名>` / usr `memory_prompts`，见 memoryPromptFor）。
	// 前端镜像常量见 src/frontend/src/composables/useMemoryCategories.js（跨端字面量有测试守卫）。
	defaultRewriteSystemPrompt = "你是记忆库沉淀器。给定某个记忆类别的现有全文与本轮对话的新增信息，" +
		"请把两者合并后重写该类别全文（累加 + 更新：修正过时内容、去重、条理化、不臆造）。" +
		"只输出重写后的 markdown 全文，不要任何解释或代码块围栏。"
)

// Options 是记忆库插件配置（构造基线；项目配置可覆盖阈值）。
type Options struct {
	// MinTurnTokens 沉淀阈值：本轮新增（未提取）token 低于该值 → 跳过（不调 LLM）。
	MinTurnTokens int
}

// DefaultOptions 提供默认阈值（寒暄类如"你好"远低于此，不产生记忆）。
func DefaultOptions() Options {
	return Options{MinTurnTokens: defaultMinTurnTokens}
}

// Plugin 记忆库沉淀插件（server 插件钩子实现）。
type Plugin struct {
	opts     Options
	deps     plugin.Deps
	sub      mq.Sub
	flushSub mq.Sub
}

// New 构建记忆库插件（阈值给零值 → 回落默认）。
func New(opts Options) *Plugin {
	if opts.MinTurnTokens <= 0 {
		opts.MinTurnTokens = DefaultOptions().MinTurnTokens
	}
	return &Plugin{opts: opts}
}

// Name 插件名。
func (p *Plugin) Name() string { return "memory" }

// Start 订阅每轮终态事件 + 手动沉淀入口（server 全部插件加载完成后由宿主调用）。
func (p *Plugin) Start(d plugin.Deps) error {
	p.deps = d
	sub, err := d.Bus.On(turnEndSubject, 0, func(_ context.Context, subj string, v *mq.Value) error {
		p.onTurnEnd(subj, v.Payload)
		return nil
	})
	if err != nil {
		return err
	}
	p.sub = sub
	// memory.flush：手动触发一次沉淀——**同步**执行（含各类别重写）并把结果写回 v.Result
	// （saved/failed），使 UI 能给出「进行中 → 成功/失败」的明确反馈（不新增回执主题）。
	fsub, err := d.Bus.On(flushSubject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		v.Result = p.flush(v.Payload)
		return nil
	})
	if err != nil {
		return err
	}
	p.flushSub = fsub
	p.logf()("memory: subscribed %s + %s (min_turn_tokens=%d, gate=%s)", turnEndSubject, flushSubject, p.opts.MinTurnTokens, memoryEnabledKey)
	return nil
}

// logf 取日志函数（未装配时静默）。
func (p *Plugin) logf() func(string, ...any) {
	if p.deps.Logf != nil {
		return p.deps.Logf
	}
	return func(string, ...any) {}
}

// notify 上报一次**用户可见**的沉淀失败（2026-09-20，B 批）：宿主未注入 → 静默（行为同改前）。
// 只上报事实（插件/类别/实例/会话/轮次/原因），是否提示、是否去重由宿主决定（不阻塞、不抛出）。
func (p *Plugin) notify(ev turnEvent, kind, reason string) {
	if p.deps.Notify == nil {
		return
	}
	p.deps.Notify(plugin.Notice{
		Plugin: "memory", Kind: kind,
		InstanceID: ev.InstanceID, Session: ev.Session, Turn: ev.LastTurn,
		Reason: reason,
	})
}

// turnEvent 是 session-compress 事件载荷（server.finish 发出）。
type turnEvent struct {
	InstanceID string `json:"instance_id"`
	WorkDir    string `json:"work_dir"`
	DataDir    string `json:"data_dir"`
	Session    string `json:"session"`
	LastTurn   string `json:"last_turn"`
}

// onTurnEnd 每轮终态 → 异步沉淀（不阻塞对话）。
func (p *Plugin) onTurnEnd(_ string, payload []byte) {
	var ev turnEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return
	}
	if ev.InstanceID == "" || ev.WorkDir == "" || ev.Session == "" || ev.LastTurn == "" {
		return
	}
	go p.extract(ev)
}

// memoryConfig 是本轮消费的项目配置（memory.*；缺失回落默认）。
type memoryConfig struct {
	Enabled   bool
	MinTokens int
	// MaxTokens 是单类别 token **告警阈值**（prj `memory.category-max-tokens`）：>0 = 超过即记
	// 告警日志（不截断、不阻断）；0 = 未设置 = 不限。
	MaxTokens  int
	Categories map[string]bool   // 类别名 → 启用（键缺失 → 默认启用）
	Prompts    map[string]string // 类别名 → 自定义沉淀提示词（prj `memory.prompt.<类别名>`；空 = 未配置）
}

// categoryEnabled 判断类别是否启用：读项目配置 memory.category.<类别名>；键缺失 → 默认启用。
// 用户级"用户偏好"同此口径（默认启用、可经设置页显式关闭，键 = memory.category.用户偏好）。
func (c memoryConfig) categoryEnabled(name string) bool {
	if v, ok := c.Categories[name]; ok {
		return v
	}
	return true
}

// extract 执行一次沉淀（可同步调用，供测试）：配置门控 → 阈值门控 → 按启用类别并行重写。
// 任一环节失败：记日志 + **上报一次用户可见提示**（宿主去重/限频）→ 跳过该类，**不阻塞对话**
// （不降级、不抛出）。
func (p *Plugin) extract(ev turnEvent) {
	logf := p.logf()
	cfg, err := p.resolveConfig(ev.InstanceID)
	if err != nil {
		logf("memory: 读项目配置失败（instance=%s）：%v（跳过）", ev.InstanceID, err)
		p.notify(ev, "config", err.Error())
		return
	}
	if !cfg.Enabled {
		return
	}
	msgs, err := p.loadTurnMessages(ev.InstanceID, ev.LastTurn)
	if err != nil {
		logf("memory: 读本轮消息失败（turn=%s）：%v（跳过）", ev.LastTurn, err)
		p.notify(ev, "messages", err.Error())
		return
	}
	tokens := data.EstimateTokensOfMessages(msgs)
	if tokens < cfg.MinTokens {
		logf("memory: 本轮新增 %d token < 阈值 %d → 跳过（turn=%s）", tokens, cfg.MinTokens, ev.LastTurn)
		return
	}
	cats, err := p.memoryCategories(ev.InstanceID)
	if err != nil {
		logf("memory: 读记忆类别失败（instance=%s）：%v（跳过）", ev.InstanceID, err)
		p.notify(ev, "categories", err.Error())
		return
	}
	p.distill(ev, cfg, cats, renderTurnInfo(msgs), tokens)
}

// distill 是按启用类别并行重写全文的**唯一沉淀回路**（自动沉淀 extract 与手动 flush 共用）：
// 逐类别 读旧全文（data-memory-read）→ 旧全文 + 新信息经 llm-simple 重写 → 保存（data-memory-save）。
// 单类别失败：记日志 + 上报一次用户可见提示 → 跳过该类（不降级、不抛出）。
// 返回成功/失败类别（供手动 flush 回执；自动沉淀忽略返回值）。可同步调用，供测试。
func (p *Plugin) distill(ev turnEvent, cfg memoryConfig, cats []categoryInfo, newInfo string, tokens int) ([]string, []map[string]string) {
	logf := p.logf()
	// 子系统默认 LLM（SL-3）+ 用户级类别沉淀提示词（2026-09-26）：**每次沉淀现读**同一份
	// usr 配置（不缓存到进程级/包级 → 配置改动热生效，生效粒度 = 按 turn）。本次沉淀各类别共用。
	userCfg := p.readUserConfig(ev.InstanceID)
	llm := subsystemLLM(userCfg)
	userPrompts := userMemoryPrompts(userCfg)
	saved := make([]string, 0, len(cats))
	failed := make([]map[string]string, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range cats {
		if !cfg.categoryEnabled(c.Category) {
			continue
		}
		wg.Add(1)
		go func(ci categoryInfo) {
			defer wg.Done()
			category := ci.Category
			old, err := p.memoryRead(ev.InstanceID, category)
			if err != nil {
				logf("memory: 读记忆失败（%s）：%v（跳过）", category, err)
				p.notify(ev, "read", category+"："+err.Error())
				mu.Lock()
				failed = append(failed, map[string]string{"category": category, "kind": "read", "reason": err.Error()})
				mu.Unlock()
				return
			}
			text, err := p.rewrite(ev.InstanceID, llm, memoryPromptFor(cfg, userPrompts, ci), category, old, newInfo)
			if err != nil {
				logf("memory: 沉淀 LLM 失败（%s）：%v（跳过）", category, err)
				p.notify(ev, "llm", category+"："+err.Error())
				mu.Lock()
				failed = append(failed, map[string]string{"category": category, "kind": "llm", "reason": err.Error()})
				mu.Unlock()
				return
			}
			// 单类别 token 告警阈值（prj `memory.category-max-tokens`）：语义 = **仅提醒**
			// （不截断、不阻断沉淀，与前端标红同口径）。超限只记日志，仍照常保存。
			if cfg.MaxTokens > 0 {
				if n := data.EstimateTokens(text); n > cfg.MaxTokens {
					logf("memory: 类别 %s 全文约 %d token 超告警阈值 %d（建议细分记忆；不截断）", category, n, cfg.MaxTokens)
				}
			}
			if err := p.memorySave(ev.InstanceID, category, text); err != nil {
				logf("memory: 保存记忆失败（%s）：%v（跳过）", category, err)
				p.notify(ev, "save", category+"："+err.Error())
				mu.Lock()
				failed = append(failed, map[string]string{"category": category, "kind": "save", "reason": err.Error()})
				mu.Unlock()
				return
			}
			logf("memory: 沉淀 %s（turn=%s, 本轮 %d token）", category, ev.LastTurn, tokens)
			mu.Lock()
			saved = append(saved, category)
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	return saved, failed
}

// flush 手动触发一次沉淀（memory.flush 订阅者）：取该会话**最近一轮**消息 → 复用 distill。
//
// 与自动沉淀（extract）的差异仅三处，其余（启用门控 / 类别门控 / 单类别 token 告警 /
// 读改写保存回路）完全同源：
//   - 触发源 = 用户显式动作（不经 session-compress 轮末事件）；
//   - **不受 `memory.min-turn-tokens` 门控**（显式动作即用户意图，短轮次也照沉淀）；
//   - 同步返回结果（saved/failed），供设置页给「成功/失败」反馈。
//
// 作用域 = payload 指定的 instance + session（不跨实例、不跨会话）；任何前置条件不满足
// 均以 `{ok:false, reason}` 明确作答（不抛错、不静默）。
func (p *Plugin) flush(payload []byte) map[string]any {
	var req struct {
		InstanceID string `json:"instance_id"`
		Session    string `json:"session"`
	}
	if err := json.Unmarshal(payload, &req); err != nil || req.InstanceID == "" || req.Session == "" {
		return map[string]any{"ok": false, "reason": "instance_id/session required"}
	}
	logf := p.logf()
	cfg, err := p.resolveConfig(req.InstanceID)
	if err != nil {
		logf("memory: 手动沉淀读项目配置失败（instance=%s）：%v", req.InstanceID, err)
		return map[string]any{"ok": false, "reason": err.Error()}
	}
	if !cfg.Enabled {
		return map[string]any{"ok": false, "reason": memoryEnabledKey + " not enabled"}
	}
	turn, err := p.latestTurn(req.InstanceID, req.Session)
	if err != nil {
		logf("memory: 手动沉淀读会话轮次失败（session=%s）：%v", req.Session, err)
		return map[string]any{"ok": false, "reason": err.Error()}
	}
	if turn == "" {
		return map[string]any{"ok": false, "reason": "no turn in session " + req.Session}
	}
	msgs, err := p.loadTurnMessages(req.InstanceID, turn)
	if err != nil {
		logf("memory: 手动沉淀读本轮消息失败（turn=%s）：%v", turn, err)
		return map[string]any{"ok": false, "reason": err.Error()}
	}
	if len(msgs) == 0 {
		return map[string]any{"ok": false, "reason": "empty turn " + turn}
	}
	cats, err := p.memoryCategories(req.InstanceID)
	if err != nil {
		logf("memory: 手动沉淀读记忆类别失败（instance=%s）：%v", req.InstanceID, err)
		return map[string]any{"ok": false, "reason": err.Error()}
	}
	enabled := 0
	for _, c := range cats {
		if cfg.categoryEnabled(c.Category) {
			enabled++
		}
	}
	ev := turnEvent{InstanceID: req.InstanceID, Session: req.Session, LastTurn: turn}
	saved, failed := p.distill(ev, cfg, cats, renderTurnInfo(msgs), data.EstimateTokensOfMessages(msgs))
	logf("memory: 手动沉淀完成（session=%s turn=%s 成功 %d/失败 %d/启用 %d）",
		req.Session, turn, len(saved), len(failed), enabled)
	return map[string]any{
		"ok": true, "session": req.Session, "turn": turn,
		"saved": saved, "failed": failed, "enabled": enabled,
	}
}

// resolveConfig 经 data-prj-config-list 一次读回项目配置并解析 memory.*（缺失回落默认）。
func (p *Plugin) resolveConfig(instanceID string) (memoryConfig, error) {
	cfg := memoryConfig{
		Enabled:    false,
		MinTokens:  p.opts.MinTurnTokens,
		Categories: map[string]bool{},
		Prompts:    map[string]string{},
	}
	res, err := p.request(prjConfigListSubject, map[string]any{"instance_id": instanceID})
	if err != nil {
		return cfg, err
	}
	list, _ := res["list"].(map[string]any)
	if raw, ok := list[memoryEnabledKey]; ok {
		cfg.Enabled = strval(raw) == "true"
	}
	if raw, ok := list[memoryMinTokensKey]; ok {
		if n := atoiOK(strval(raw)); n > 0 {
			cfg.MinTokens = n
		}
	}
	// 单类别 token 告警阈值（prj 键；非正/缺失 → 0 = 不限）。
	if raw, ok := list[memoryCategoryMaxTokensKey]; ok {
		cfg.MaxTokens = atoiOK(strval(raw))
	}
	for k, v := range list {
		if name, ok := strings.CutPrefix(k, memoryCategoryPrefix); ok && name != "" {
			cfg.Categories[name] = strval(v) == "true"
			continue
		}
		// 项目级类别沉淀提示词（`memory.prompt.<类别名>`；空值亦登记 → 与未配置同口径回落默认）。
		if name, ok := strings.CutPrefix(k, memoryPromptPrefix); ok && name != "" {
			cfg.Prompts[name] = strval(v)
		}
	}
	return cfg, nil
}

// readUserConfig 经既有 data-user-config-load 面一次读回 usr 配置对象（失败 → nil）。
//
// 热生效（SL-C9）：**每次沉淀现读**（消息面零新增），不缓存到进程级/包级变量 →
// 配置改动无需重启，生效粒度 = 按 turn。一次读回同时供「子系统默认 LLM」与「用户级类别
// 沉淀提示词」两个消费点使用（避免同一轮重复发两次请求）。
func (p *Plugin) readUserConfig(instanceID string) map[string]any {
	res, err := p.request(userConfigLoadSubject, map[string]any{"instance_id": instanceID})
	if err != nil {
		return nil // 读失败 → 两消费点各按"不指定/内置默认"处置；沉淀流程不中断
	}
	cfg, _ := res["data"].(map[string]any)
	return cfg
}

// subsystemLLM 取记忆子系统的默认 LLM（provider name）：读 usr `llm.memory`（SL-3）。
// 取值 = llmref（**键缺失/空串 → 回落 defaultLLM 已由数据层读侧完成**，此处不重做，见 SL-1）；
// 折算（旧 int 索引 → provider name）走 data.LLMRefName 单一来源。返回 ""（无法折算 /
// 无可用 LLM / 读失败）→ llm-simple 请求**不带 `llm` 字段**（server 回落 exe flags 默认）。
func subsystemLLM(userCfg map[string]any) string {
	if userCfg == nil {
		return ""
	}
	return data.LLMRefName(userCfg[memoryLLMKey], userCfg["llms"])
}

// userMemoryPrompts 解析用户级类别的自定义沉淀提示词（usr 自由键 `memory_prompts`，
// 值 = JSON 对象字符串 `{"<类别名>":"<提示词全文>"}`）。键缺失 / 空串 / 非法 JSON → nil
// （调用方回落内置默认），不视为失败。
func userMemoryPrompts(userCfg map[string]any) map[string]string {
	if userCfg == nil {
		return nil
	}
	raw := strval(userCfg[userMemoryPromptsKey])
	if raw == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	return m
}

// memoryPromptFor 取某类别的沉淀提示词（llm-simple 的 `system`）：
//   - 用户级类别（用户偏好）→ usr `memory_prompts` 中该类别的值；
//   - 项目级类别 → prj `memory.prompt.<类别名>`；
//   - 未配置 / 空白 → **内置默认** defaultRewriteSystemPrompt（逐字节等价于改前硬编码口径）。
func memoryPromptFor(cfg memoryConfig, userPrompts map[string]string, c categoryInfo) string {
	custom := cfg.Prompts[c.Category]
	if c.Level == memoryUserLevel {
		custom = userPrompts[c.Category]
	}
	if strings.TrimSpace(custom) == "" {
		return defaultRewriteSystemPrompt
	}
	return custom
}

// categoryInfo 是 memory 类别（持久化侧为唯一来源，插件不硬编码类别清单）。
type categoryInfo struct {
	Category string
	Level    string
}

// memoryCategories 经 data-memory-list 取类别清单。
func (p *Plugin) memoryCategories(instanceID string) ([]categoryInfo, error) {
	res, err := p.request(memoryListSubject, map[string]any{"instance_id": instanceID})
	if err != nil {
		return nil, err
	}
	raw, _ := res["list"].([]any)
	out := make([]categoryInfo, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		cat := strval(m["category"])
		if cat == "" {
			continue
		}
		out = append(out, categoryInfo{Category: cat, Level: strval(m["level"])})
	}
	return out, nil
}

// loadTurnMessages 经 data-session-load-messages 读本轮全部消息。
func (p *Plugin) loadTurnMessages(instanceID, turnID string) ([]data.ChatMsg, error) {
	res, err := p.request(sessionLoadSubject, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"turn_id": turnID},
	})
	if err != nil {
		return nil, err
	}
	raw, _ := res["messages"].([]any)
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var msgs []data.ChatMsg
	if err := json.Unmarshal(b, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// latestTurn 经 data-session-history（既有面，target_messages=1 → 至少含最近一轮）取该会话
// 最近一轮的 turn_id；会话无轮次 → 空串（调用方给明确文案，不当失败）。
func (p *Plugin) latestTurn(instanceID, session string) (string, error) {
	res, err := p.request(sessionHistorySubject, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"session_id": session, "target_messages": 1},
	})
	if err != nil {
		return "", err
	}
	msgs, _ := res["messages"].(map[string]any)
	turns, _ := msgs["turns"].([]any)
	for i := len(turns) - 1; i >= 0; i-- {
		m, _ := turns[i].(map[string]any)
		if m == nil {
			continue
		}
		if id := strval(m["turn_id"]); id != "" {
			return id, nil
		}
	}
	return "", nil
}

// memoryRead 读某类全文（不存在 → 空串，不算失败）。
func (p *Plugin) memoryRead(instanceID, category string) (string, error) {
	res, err := p.request(memoryReadSubject, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"category": category},
	})
	if err != nil {
		return "", err
	}
	d, _ := res["data"].(map[string]any)
	return strval(d["content"]), nil
}

// memorySave 保存某类全文。
func (p *Plugin) memorySave(instanceID, category, content string) error {
	_, err := p.request(memorySaveSubject, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"category": category, "content": content},
	})
	return err
}

// rewrite 经无上下文单轮 LLM（llm-simple）重写类别全文：读旧全文 + 本轮新信息 → 新全文。
// instanceID 为触发事件（session-compress）载荷中的实例 id，随请求透传（61-消息一览 §0
// 实例字段必带）。
// llm = 本子系统的默认 LLM（provider name，见 subsystemLLM）；空则**不带该字段**
// （载荷与改前逐字节等价，server 回落 exe flags 默认）。
// system = 该类别的沉淀提示词（见 memoryPromptFor）；空白 → 回落内置默认（与改前等价）。
func (p *Plugin) rewrite(instanceID, llm, system, category, oldText, newInfo string) (string, error) {
	if p.deps.Bus == nil {
		return "", errors.New("no bus (llm-simple unavailable)")
	}
	if strings.TrimSpace(system) == "" {
		system = defaultRewriteSystemPrompt
	}
	prompt := fmt.Sprintf("【类别】%s\n\n【现有全文】\n%s\n\n【本轮新增信息】\n%s", category, oldText, newInfo)
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	defer cancel()
	req := map[string]any{"prompt": prompt, "system": system, "instance_id": instanceID}
	if llm != "" {
		req["llm"] = llm // SL-2 可选字段：子系统默认 LLM
	}
	v := p.deps.Bus.Emit(ctx, llmSimpleSubject, req).Wait()
	if err := v.Err(); err != nil {
		return "", err
	}
	if m, ok := v.Result.(map[string]any); ok {
		if text, ok := m["text"].(string); ok && text != "" {
			return text, nil
		}
	}
	return "", errors.New("llm-simple empty result")
}

// renderTurnInfo 把本轮消息渲染为自然文本（供沉淀 LLM 阅读）。
func renderTurnInfo(msgs []data.ChatMsg) string {
	var b strings.Builder
	for _, m := range msgs {
		role := m.Role
		if role == "" {
			role = "msg"
		}
		fmt.Fprintf(&b, "[%s] %s\n", role, m.Content)
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "  → 工具 %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
		}
	}
	return b.String()
}

// ─── data-* 请求-响应（persist 把应答发布到请求同主题，须按 req_id 关联收敛）──

// newReqID 生成一次请求的唯一 id（前缀 memory- 仅用于日志辨识，不参与过滤语义）。
//
// 为何**不用** `time.Now().UnixNano()`（I-78）：Windows 上该时钟实际刻度 ≈1ms
// （实测 999900ns），同一 tick 内并发发出的请求会拿到**相同**数值 → 撞号；
// 应答按 req_id 等值过滤随即失效，后发请求会收下先发请求的应答
// （记忆库 8 类并行读取 → 类别串味）。
//
// 为何**用随机 id** 而非 history/codegraph/vfts 的 `UnixNano + atomic.Uint64`：
// 自增序号能消除同 tick 撞号，但仍带时间戳（跨进程/跨实例/时钟回拨面存在）；
// 本插件每轮每类仅发一次请求，量级极低，随机 id 无包级可变状态、无需加锁、
// 与刻度完全解耦，最贴合"请求自身唯一"的语义。随机源用标准库 crypto/rand
// （Text() 返回 26 字符 base32 随机串，零新增第三方依赖）。
func newReqID() string {
	return "memory-" + rand.Text()
}

// request 发一次 data-* 请求并等应答（镜像 chonkpilot-plugin-history dataEmit 形态）。
func (p *Plugin) request(subject string, req map[string]any) (map[string]any, error) {
	if p.deps.Bus == nil {
		return nil, errors.New("no bus")
	}
	reqID := newReqID()
	req["req_id"] = reqID
	done := make(chan map[string]any, 1)
	sub, err := p.deps.Bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != reqID || m.OK == nil {
			return nil // 他人请求 / 非应答载荷 忽略
		}
		select {
		case done <- m.Result: // ok=false → Result 为 nil，下方按失败处理
		default:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if f := p.deps.Bus.Emit(context.Background(), subject, req); f.Wait().Err() != nil {
		return nil, f.Wait().Err()
	}
	select {
	case res := <-done:
		if res == nil {
			return nil, fmt.Errorf("%s 失败应答", subject)
		}
		return res, nil
	case <-time.After(dataTimeout):
		return nil, fmt.Errorf("%s 应答超时", subject)
	}
}

// strval 任意值 → 字符串（配置/结果值归一）。
func strval(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// atoiOK 解析十进制整数（失败 → 0）。
func atoiOK(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
