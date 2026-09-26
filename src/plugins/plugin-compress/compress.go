// Package compress 是压缩插件（server 内嵌形态，对齐 docs/spec/20-modules/28-plugins.md）。
//
// 定位：server 的压缩扩展点实现——只消费 session-compress（相对主题，chonk. 前缀
// 在 mq 初始化时注入）一个事件；server 负责：终态落库 → 写快照 → 发 llm-compress（含 instance/work_dir/data_dir/session/
// last_turn/snapshot_turn/max_context_token/max_output_token）。
// 本插件收到后：读快照 → **三段定位**（口径 X，2026-09-25）：完整区（本轮 + 最近 N 轮，N/M 取先到，
// 原文）/ 简化区（再往前，brief token 累计 <= T，**仅 text**）/ 摘要区（更早）；
// 摘要区非空 → 经无上下文单轮 LLM mq（llm-simple，server 订阅）**摘要摘要区** → 回写快照
// （请求按 usr `llm.compress` 带可选 `llm` = 子系统默认 LLM，缺省回落 defaultLLM；SL-3）。
// **兜底归并**（口径 Y/Z3，2026-09-25）：`摘要 + 简化区 + 完整区 + max_output_token > max_context_token`
// → 合并【摘要 + 简化区原内容】 → 重新摘要（目标 = `max_output_token/2`）→ 简化区清空（发送 = 完整原文 + 新摘要）。
// 窗口来源 = provider `maxContextToken`（真上下文窗口；不再以输出上限 `maxTokens` 代理），
// 输出预留 = provider `maxOutputToken`（不再用常量 4096）；两者经 `session-compress` 载荷字段
// `max_context_token` / `max_output_token` 透传（**对外载荷统一 snake_case，口径 Z4**；旧名
// `maxContextToken` / `maxOutputToken` / `window` 只读兼容，见 onEvent）。
//
// 摘要失败（无可用 LLM / 调用错误）→ 直接返回不压缩（2026-09-02 决策：不降级）；
// 2026-09-20 起同时经 Deps.Notify 上报一次用户可见提示（宿主去重/限频，不阻塞、不抛出）。
// 无本插件订阅 = 不压缩（快照保持完整历史）。
// 2026-09-08 定稿：插件只 mq 通讯——不再依赖宿主函数注入（Deps.Summarize 移除）。
//
// 2026-09-21（阶段 4 试点）：快照读/写不再持 `*data.DB` 直调 `persist.GetSnapshot/SetSnapshot`
// （那两处带库参数的导出面已收进 `chonkpilot-data/internal/snapshot`，模块外不可达）——
// 一律经 **data 门面**（`chonkpilot-data/facade`，绑定由宿主装配处注入，见 New）。
//
// 2026-09-21（阶段 4 前置·类型双轨收敛，41 G-33）：本插件**全程只用门面 DTO**
// （`facade.Snapshot` / `facade.Message`）——不再把门面结果翻回内核类型 `data.ChatMsg`
// 再翻回去（消费方只认领域模型；协议嵌套/`_meta` 等消息面形状留在数据组件内部，见 23 §7）。
package compress

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// 订阅主题（相对主题 session-compress，对齐 61-消息一览 §4.3 压缩扩展点事件；
// 2026-09-03 llm-compress → session-compress；chonk. 前缀在 mq 初始化时注入）。
const llmCompressSubject = "session-compress"

// llmSubsystemKey 是压缩子系统的默认 LLM 配置键（usr；SL-3 / SL-C2，40-演进计划 §SL）。
// 值 = llmref（provider name；键缺失/空串的回落 defaultLLM 由数据层读侧完成，见 SL-1）。
const llmSubsystemKey = "llm.compress"

// Options 配置（内嵌默认；实际值按级读项目配置，见 resolveOpts）。
//
// 2026-09-24（A）+ 2026-09-25（口径 X/Y/Z3：三段结构 + 兜底归并改接真窗口）：
//   - RetainTurns（prj `keep_full_max_turns`，默认 10）：**完整区**的最近 N 轮；
//   - KeepFullTokens（prj `keep_full_max_tokens`，默认 24000）：**完整区**完整态 token 上限 M；
//   - TokenMax（prj `compress_token_threshold`，默认 20000）：**简化区** brief token 预算 T；
//   - MaxContextToken（**非配置项**；运行时经 `session-compress` 载荷字段 `max_context_token` 透传，
//     默认 0 = 兜底不启用）：**上下文窗口大小**（= provider `maxContextToken`）——
//     `摘要 + 简化区 + 完整区 + MaxOutputToken > MaxContextToken` 时触发**兜底归并**；
//   - MaxOutputToken（**非配置项**；同经载荷 `max_output_token` 透传，默认 0）：**输出预留**（判定）与
//     摘要目标基数（目标 = MaxOutputToken/2）= provider `maxOutputToken`。
//
// 三段上下文（口径 X）：完整区（原文）→ 简化区（**仅 text**，构造 `data.BriefFacadeMessages`）
// → 摘要区（压缩为摘要）。边界算法唯一来源 = `data.LocateZones`（与组装侧同一函数）。
// 摘要**无独立配置项**（目标 = 窗口输出上限 1/2，即 MaxOutputToken/2，见口径 Z2）。
//
// **边界（口径 W）**：N == 0 = 轮次条件不启用；M == 0 = token 条件不启用；**两者均 == 0 = 不压缩**；
// **负数 = 非法（按不启用处理，前端提示）**；T <= 0 = 简化区预算 0（不进简化区，直接摘要）。
// **未配置（键缺失/空）= 回落后两项默认 10 / 24000 / 20000**（见 DefaultOptions）。
type Options struct {
	RetainTurns    int // keep_full_max_turns：完整区最近 N 轮（口径 = **含最新轮**的整轮计；与组装侧同键）
	KeepFullTokens int // keep_full_max_tokens：完整区**完整态** token 上限 M（估算口径同 EstimateTokens）
	TokenMax       int // compress_token_threshold：**简化区** brief token 预算 T
	// MaxContextToken 上下文窗口大小（非配置项；载荷 `max_context_token` = provider `maxContextToken`）：
	// >0 才启用兜底归并；0/缺省 = **不启用兜底**（仅常规三层压缩，口径 Z3）。
	MaxContextToken int
	// MaxOutputToken 最大输出 token（非配置项；载荷 `max_output_token` = provider `maxOutputToken`）：
	// 兜底判定的**输出预留**（替代旧常量 4096）+ 摘要目标基数（目标 = /2，口径 Z2/Z3）。
	MaxOutputToken int
}

// DefaultOptions 提供默认阈值（`keep_full_max_turns`=10 / `keep_full_max_tokens`=24000 /
// `compress_token_threshold`=20000）。
func DefaultOptions() Options {
	return Options{RetainTurns: 10, KeepFullTokens: 24000, TokenMax: 20000}
}

// Compressor 内嵌压缩处理器（server 插件钩子实现）。
type Compressor struct {
	opts Options
	deps plugin.Deps
	api  facade.API // data 门面（绑定由宿主装配处注入，见 New）
	sub  mq.Sub
}

// New 构建压缩插件。
//
// api = data 门面（快照读/写入口）；**由宿主装配处注入**——本期 = inline 绑定
// （`chonkpilot-data/facade/inline`，同进程直调），后续 http / mq 绑定同为其实现
// （23 §7：编译期决定编入哪些绑定、装配期决定用哪个）。nil = 未接线 → 快照读写跳过（不压缩）。
//
// 三项阈值 ≤ 0 视为**未配置** → 回落 DefaultOptions（10 / 24000 / 20000）；
// 「显式配 0/负」的语义由 resolveOpts 在读到配置键后直接赋值实现（0 = 该条件不启用；
// **两者均 0 → 不压缩**；负数 = 非法、按不启用处理）——见其注释与 LocateRetention。
func New(opts Options, api facade.API) *Compressor {
	def := DefaultOptions()
	if opts.RetainTurns <= 0 {
		opts.RetainTurns = def.RetainTurns
	}
	if opts.KeepFullTokens <= 0 {
		opts.KeepFullTokens = def.KeepFullTokens
	}
	if opts.TokenMax <= 0 {
		opts.TokenMax = def.TokenMax
	}
	return &Compressor{opts: opts, api: api}
}

// Name 插件名。
func (c *Compressor) Name() string { return "compress" }

// summarize 经无上下文单轮 LLM mq（相对主题 llm-simple，server 订阅；chonk. 前缀注入）
// 请求摘要——插件只 mq 通讯，不依赖宿主函数注入（2026-09-08 定稿）。
// instanceID 为触发事件（session-compress）载荷中的实例 id，随请求透传（61-消息一览 §0
// 实例字段必带；对照 gateway/meta_tools.go llmToolPick 的写法）。
// system = 摘要提示词（resolveSummaryPrompt 按级读 summary.prompt.md，缺省内置默认）；
// 空则不带（llm-simple 请求 {prompt[, system], instance_id[, llm]}）。
// llm = 本子系统的默认 LLM（provider name，见 resolveSubsystemLLM）；空则**不带该字段**
// （载荷与改前逐字节等价，server 回落 exe flags 默认）。
func (c *Compressor) summarize(instanceID, system, llm, content string) (string, error) {
	if c.deps.Bus == nil {
		return "", fmt.Errorf("no bus (llm-simple unavailable)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := map[string]any{"prompt": content, "instance_id": instanceID}
	if system != "" {
		req["system"] = system
	}
	if llm != "" {
		req["llm"] = llm // SL-2 可选字段：子系统默认 LLM
	}
	v := c.deps.Bus.Emit(ctx, "llm-simple", req).Wait()
	if err := v.Err(); err != nil {
		return "", err
	}
	if m, ok := v.Result.(map[string]any); ok {
		if text, ok := m["text"].(string); ok && text != "" {
			return text, nil
		}
	}
	return "", fmt.Errorf("llm-simple empty result")
}

// Start 订阅 llm-compress（server 全部插件加载完成后由宿主调用）。
func (c *Compressor) Start(d plugin.Deps) error {
	c.deps = d
	if c.api == nil && d.Logf != nil {
		d.Logf("compress: data facade not injected (snapshot read/write disabled)")
	}
	sub, err := d.Bus.On(llmCompressSubject, 0, func(_ context.Context, subj string, v *mq.Value) error {
		c.onEvent(subj, v.Payload)
		return nil
	})
	if err != nil {
		return err
	}
	c.sub = sub
	if d.Logf != nil {
		d.Logf("compress: subscribed %s (max_turns=%d keep_full_tokens=%d token_max=%d)",
			llmCompressSubject, c.opts.RetainTurns, c.opts.KeepFullTokens, c.opts.TokenMax)
	}
	return nil
}

// onEvent 处理 llm-compress。
func (c *Compressor) onEvent(_ string, payload []byte) {
	d := c.deps
	logf := d.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var ev struct {
		InstanceID   string `json:"instance_id"`
		WorkDir      string `json:"work_dir"`
		DataDir      string `json:"data_dir"`
		Session      string `json:"session"`
		SnapshotTurn string `json:"snapshot_turn"`
		// 载荷字段（口径 Z4，2026-09-25：对外载荷统一 snake_case，**写入只发 snake_case**）：
		// 上下文窗口大小（真窗口，替代上批以 provider `maxTokens` 输出上限作的窗口代理）与最大输出 token
		// （判定预留 + 摘要目标基数）。各自未配置 / 为 0 → 兜底不启用（与旧载荷逐字节等价）。
		MaxContextToken *int `json:"max_context_token"`
		MaxOutputToken  *int `json:"max_output_token"`
		// **读兼容（只读不写）**：上批 camelCase `maxContextToken` / `maxOutputToken` +
		// 更早的 `window`（仅上下文窗口）。新名优先，旧名仅在新名缺省时生效（见 pickInt）。
		LegacyContextToken *int `json:"maxContextToken"`
		LegacyOutputToken  *int `json:"maxOutputToken"`
		LegacyWindow       *int `json:"window"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return
	}
	// 字段解析：新名（snake_case）优先；缺省 → 读兼容 camelCase → 更早 `window`。
	ctxToken := pickInt(ev.MaxContextToken, ev.LegacyContextToken, ev.LegacyWindow)
	outToken := pickInt(ev.MaxOutputToken, ev.LegacyOutputToken)
	if ev.Session == "" {
		return
	}
	if c.api == nil { // 未接线（见 New）：快照读写不可用 → 不压缩
		return
	}
	// 快照读/写**一律经 data 门面**（不再持 *data.DB 直调 persist 访问器）：
	// 事件载荷的 work_dir/data_dir 作为实例数据根提示带入（本进程尚未登记该实例时自登记，
	// 语义同原 prjUsrDB）。
	scope := facade.Scope{WorkDir: ev.WorkDir, DataDir: ev.DataDir}
	resp, err := c.api.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: ev.InstanceID, SessionID: ev.Session, Scope: scope,
	})
	if err != nil || !resp.Found || len(resp.Snapshot.Messages) == 0 {
		return
	}
	snap := resp.Snapshot // 门面 DTO（消费方只认领域模型；不再翻回内核类型）
	// 阈值读项目配置（12-数据层：compress 必须读 `keep_full_max_turns`/`keep_full_max_tokens`/
	// `compress_token_threshold`，不得写死 DefaultOptions）；缺失 → 回落默认常量。
	opts := c.resolveOpts(ev.InstanceID, ev.WorkDir, ev.DataDir)
	opts.MaxContextToken = ctxToken // 真窗口（非配置项；来自触发事件载荷，0 = 兜底不启用）
	opts.MaxOutputToken = outToken  // 输出预留 + 摘要目标基数（非配置项；同上）
	// 非法值可观测（口径 W：N<0 / M<0 = 非法）：后端不猜、按「不启用」处理，但必须留痕
	// （前端已显式提示；此处补日志，便于排查"配置被忽略"）。
	if opts.RetainTurns < 0 || opts.KeepFullTokens < 0 {
		logf("compress: illegal negative bound (treat as disabled): keep_full_max_turns=%d keep_full_max_tokens=%d",
			opts.RetainTurns, opts.KeepFullTokens)
	}
	// 摘要 system 提示词按级读 summary.prompt.md（项目级 → 用户级 → 系统级 → 内置默认），
	// 每次压缩读文件（保存提示词后下一次压缩即生效）。
	sys := c.resolveSummaryPrompt(ev.WorkDir)
	// 子系统默认 LLM（SL-3）：**每次压缩现读** usr `llm.compress`——不缓存到进程级/包级
	// → 配置改动**热生效**、生效粒度 = 按 turn（SL-C9）。
	subLLM := c.resolveSubsystemLLM(ev.InstanceID, ev.WorkDir, ev.DataDir)
	// 摘要失败 = 压缩未发生（不降级，见 DoCompress）→ 除日志外**上报一次用户可见提示**
	// （2026-09-20，B 批：此前用户与日志双不可见）；宿主去重/限频，不阻塞、不抛出。
	summarize := func(content string) (string, error) {
		text, err := c.summarize(ev.InstanceID, sys, subLLM, content)
		if err != nil {
			c.notify(ev.InstanceID, ev.Session, ev.SnapshotTurn, "llm", err.Error())
		}
		return text, err
	}
	newSnap, changed := DoCompress(snap, opts, summarize, logf)
	if !changed {
		return
	}
	newSnap.SessionID = ev.Session // 归属 = 触发事件的会话（同改前 SnapshotToFacade(ev.Session, …) 口径）
	if _, err := c.api.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: ev.InstanceID, Snapshot: newSnap, Scope: scope,
	}); err != nil {
		logf("compress: set snapshot failed for %s: %v", ev.Session, err)
		c.notify(ev.InstanceID, ev.Session, ev.SnapshotTurn, "save", err.Error())
		return
	}
	logf("compress: %s compressed (keep-full bound max_turns=%d/%d tokens, %d -> %d msgs)",
		ev.Session, opts.RetainTurns, opts.KeepFullTokens, len(snap.Messages), len(newSnap.Messages))
}

// notify 上报一次**用户可见**的压缩失败（2026-09-20，B 批）：宿主未注入 → 静默（行为同改前）。
// 只上报事实（插件/类别/实例/会话/轮次/原因），是否提示与是否去重由宿主决定（不阻塞、不抛出）。
func (c *Compressor) notify(instanceID, session, turn, kind, reason string) {
	if c.deps.Notify == nil {
		return
	}
	c.deps.Notify(plugin.Notice{
		Plugin: "compress", Kind: kind,
		InstanceID: instanceID, Session: session, Turn: turn,
		Reason: reason,
	})
}

// resolveSubsystemLLM 读 usr `llm.compress`（SL-3，40-演进计划 §SL）：**「子系统 → LLM」的
// 解析责任在插件侧**——读配置后随 llm-simple 请求带（`llm` 字段）。
//
// 热生效（SL-C9）：**每次压缩现读**，不缓存到进程级/包级变量 → 配置改动无需重启，
// 生效粒度 = 按 turn（当前轮不改，下一轮起用新值）。
//
// 取值 = llmref（provider name；**键缺失/空串 → 回落 defaultLLM 已由数据层读侧完成**，
// 此处不重做，见 SL-1）。折算（旧 int 索引 → provider name）走 data.LLMRefName 单一来源。
// 返回 provider name；无法折算（显式系统默认 / 无可用 LLM / 读失败）→ ""（= 不传该字段，
// server 回落 exe flags 默认，与改前行为一致）。
func (c *Compressor) resolveSubsystemLLM(instanceID, workDir, dataDir string) string {
	if c.api == nil {
		return ""
	}
	resp, err := c.api.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: facade.Scope{WorkDir: workDir, DataDir: dataDir},
	})
	if err != nil {
		return "" // 读失败 → 不指定（回落现状）；摘要流程不中断
	}
	return data.LLMRefName(resp.Config[llmSubsystemKey], resp.Config["llms"])
}

// resolveOpts 读项目级配置 keep_full_max_turns / keep_full_max_tokens / compress_token_threshold
// （经 **data 门面**，阶段 4 第二批 / 41 G-34：原 `data.Prj` + `data.GetConfig` 直连已删）。
//
// 基线 = 构造 Options（已由 New 补齐默认，即 {10, 24000, 20000}），门面读到的键覆盖。
// 读序 = 配置域口径（02-配置层级 §3：prjusr → prj → usr，首个非空命中）——与组装侧
// `assemble.go` 的 `prjConfigValue`（同键，同一条消息面）**同一读法**。
//
// 键语义（D1）：
//   - `keep_full_max_turns`（原键 `keep_full_turns`）→ RetainTurns；
//   - `keep_full_max_tokens` → KeepFullTokens；
//   - `compress_token_threshold` → TokenMax。
//
// **解析失败/键缺失/空串 → 保留基线（= 默认值）**；**解析成功但 ≤ 0 → 直接赋值（= 该条件不启用）**。
// **读时兼容**：`keep_full_max_turns` 缺失时回落旧键 `keep_full_turns`（2026-09-24 改名前的
// 历史项目配置）——只读不写，不静默丢弃既有配置。
func (c *Compressor) resolveOpts(instanceID, workDir, dataDir string) Options {
	opts := c.opts
	if c.api == nil {
		return opts
	}
	resp, err := c.api.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID,
		Keys: []string{
			"keep_full_max_turns", "keep_full_max_tokens", "compress_token_threshold",
			"keep_full_turns", // 读时兼容：旧键（改名前的历史项目配置）
		},
		Scope: facade.Scope{WorkDir: workDir, DataDir: dataDir},
	})
	if err != nil {
		return opts // 读失败（实例不可解析等）→ 保留构造基线（与改前同口径）
	}
	// 新键优先；缺失/空 → 回落旧键；两者皆空 → 保留基线默认。
	if n, ok := parseCfgInt(resp.Values, "keep_full_max_turns"); ok {
		opts.RetainTurns = n
	} else if n, ok := parseCfgInt(resp.Values, "keep_full_turns"); ok {
		opts.RetainTurns = n
	}
	if n, ok := parseCfgInt(resp.Values, "keep_full_max_tokens"); ok {
		opts.KeepFullTokens = n
	}
	if n, ok := parseCfgInt(resp.Values, "compress_token_threshold"); ok {
		opts.TokenMax = n
	}
	return opts
}

// parseCfgInt 解析配置键整数值：键缺失/空白/非数字 → (0, false)（调用方保留基线默认）；
// 解析成功（含 0 / 负数）→ (n, true)（0 / 负数 = 该条件不启用，由调用方语义决定）。
func parseCfgInt(values map[string]string, key string) (int, bool) {
	v := strings.TrimSpace(values[key])
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// pickInt 取首个非 nil 取值（载荷字段解析**新名优先**，其次读兼容旧名）；全 nil → 0
// （= 未配置 → 兜底不启用，与旧载荷逐字节等价）。
func pickInt(vals ...*int) int {
	for _, v := range vals {
		if v != nil {
			return *v
		}
	}
	return 0
}

// DoCompress 压缩判定 + 变换——压缩纯函数（导出供独立测试模块与内部复用）；
// 无预存 token 值时的薄封装（只用实时估算）；需要预存值时用 DoCompressWith。
func DoCompress(snap facade.Snapshot, opts Options, summarize func(string) (string, error), logf func(string, ...any)) (facade.Snapshot, bool) {
	return DoCompressWith(snap, opts, nil, nil, summarize, logf)
}

// DoCompressWith 同 DoCompress，另接受各轮**预存完整态 / 简化态 token**（storedFull / storedBrief，
// 升序、末尾 = 最新轮；缺值回退实时估算，见 `data.ResolveStoredTokens` / `LocateThreeZones`）——
// 三段边界与简化区预算 T 均据此，避免重复估算。
//
// 三段定位（口径 X，2026-09-25）：`LocateThreeZones`（算法 = `data.LocateZones`，与组装侧同一函数）
// → 完整区（本轮 + 最近 N/M，原文）/ 简化区（brief token 累计 <= T，仅 text）/ 摘要区（更早）。
//
// 判定与处置：
//  1. **无摘要区**（SummaryStart == BriefStart）→ 不压缩（保留段 = 完整区 + 简化区）；
//  2. **兜底归并**（口径 Y/Z3，`opts.MaxContextToken > 0` 且 `摘要 + 简化区 + 完整区 + MaxOutputToken > MaxContextToken`）：
//     合并【摘要（prelude）+ 简化区原内容】→ **重新摘要**（目标 = `MaxOutputToken/2`，经同一 summarize 路径
//     追加长度要求）→ 结果 = `[新摘要] + 完整区`（**简化区清空**）；
//  3. **常规压缩**：摘要区 → 摘要（简化区**原样保留**在快照；组装侧按 `data.BriefMessages` 投影为
//     【简化态原文】）→ 结果 = `[摘要] + 简化区 + 完整区`。
//
// 幂等 / 可重入：每次均自**入参快照**重算，不依赖上次结果 → 重试不重复膨胀；
// **失败不丢原文**：summarize 失败 → 原样返回 `(snap, false)`（回退未归并状态，不丢弃）。
// 入参/出参 = **门面 DTO**（消费方只认领域模型；协议形状不外溢，见 41 G-33）。
func DoCompressWith(snap facade.Snapshot, opts Options, storedFull, storedBrief []int, summarize func(string) (string, error), logf func(string, ...any)) (facade.Snapshot, bool) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if len(snap.Messages) == 0 {
		return snap, false
	}
	z := LocateThreeZones(snap.Messages, opts.RetainTurns, opts.KeepFullTokens, opts.TokenMax, storedFull, storedBrief)
	if z.BriefStart <= z.SummaryStart {
		return snap, false // 无摘要区 → 无可压缩内容
	}
	// 兜底归并（口径 Y/Z3）：三段 + 输出预留（MaxOutputToken）超真窗口（MaxContextToken）→ 合并【摘要 + 简化区原内容】
	// → 重新摘要（目标 MaxOutputToken/2）。MaxContextToken <= 0（未配置）→ 不启用兜底（仅常规三层压缩）。
	if opts.MaxContextToken > 0 && sendTokens(snap.Messages, z)+opts.MaxOutputToken > opts.MaxContextToken {
		sum, err := summarize(renderMessages(snap.Messages[:z.FullStart]) + fallbackDirective(opts.MaxOutputToken))
		if err != nil {
			logf("compress: fallback merge summarize failed: %v (skip)", err)
			return snap, false // 失败回退：原文不丢（快照原样返回）
		}
		newSnap := snap
		newSnap.Messages = append([]facade.Message{
			{Role: "system", Content: "[已压缩早前对话] " + sum},
		}, snap.Messages[z.FullStart:]...)
		return newSnap, true
	}
	// 常规压缩：摘要区（含 prelude 既有摘要）→ 摘要；简化区 + 完整区原样保留。
	sum, err := summarize(renderMessages(snap.Messages[:z.BriefStart]))
	if err != nil {
		logf("compress: summarize failed: %v (skip)", err)
		return snap, false
	}
	// 新快照 = system 摘要 + 简化区 + 完整区（doc §3：快照唯一权威；turn 不变）
	newSnap := snap
	newSnap.Messages = append([]facade.Message{
		{Role: "system", Content: "[已压缩早前对话] " + sum},
	}, snap.Messages[z.BriefStart:]...)
	return newSnap, true
}

// sendTokens 三段**当前发送量**估算（口径 Y/Z3 判定输入）：
// 摘要（prelude + 摘要区原文，其上界即待生成摘要的承载量）+ 简化区（**简化态** = 仅 text 实发量）
// + 完整区（完整态原文）。
func sendTokens(msgs []facade.Message, z Zones) int {
	summary := data.EstimateTokensOfFacadeMessages(msgs[:z.BriefStart])
	brief := data.EstimateTokensOfFacadeMessages(data.BriefFacadeMessages(msgs[z.BriefStart:z.FullStart]))
	full := data.EstimateTokensOfFacadeMessages(msgs[z.FullStart:])
	return summary + brief + full
}

// fallbackDirective 兜底归并的长度要求（目标 = `maxOutputToken/2`，口径 Z2；经**同一 summarize 路径**追加到
// 摘要输入，复用既有 llm-simple 摘要面，不新开 msg）。
func fallbackDirective(maxOutputToken int) string {
	target := maxOutputToken / 2
	if target < 1 {
		target = 1
	}
	return fmt.Sprintf("\n\n[要求] 请把以上内容合并压缩为一段摘要，长度不超过约 %d tokens。", target)
}

// renderMessages 把待压缩段渲染为自然文本（供摘要 LLM 阅读）。
func renderMessages(msgs []facade.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		role := m.Role
		if role == "" {
			role = "msg"
		}
		fmt.Fprintf(&b, "[%s] %s\n", role, m.Content)
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "  → 工具 %s(%s)\n", tc.Name, tc.Arguments)
		}
	}
	return b.String()
}
