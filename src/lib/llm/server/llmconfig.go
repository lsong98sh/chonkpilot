package server

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// llmProtocolOpenAI / llmProtocolResponses 是已知的协议取值（P1-2）：openai = OpenAI 兼容
// Chat Completions（/chat/completions，默认）；responses = OpenAI Responses API（/responses）。
// protocol 字段空/缺省 → openai；未知值 → warn 并按 openai 处理（不改变请求行为、不中断）。
const (
	llmProtocolOpenAI    = ProtocolOpenAI
	llmProtocolResponses = ProtocolResponses
)

// llmProtocolKnown 判断 protocol 是否为已知协议（未知值 → loadLLMProvider warn 并按 openai 处理）。
func llmProtocolKnown(p string) bool {
	return p == llmProtocolOpenAI || p == llmProtocolResponses || p == ProtocolEcho
}

// builtinFallbackName 是**内置兜底** provider 的保留名（D-30）：协议 `echo` —— **不发任何 HTTP**，
// 回显本轮最后一条真实用户消息（`llm.go` 的 `chatEcho`，无 baseUrl/apiKey、不注册/不下发任何工具）。
//
// **不再作为「可配置 provider」暴露**（2026-09-22 用户拍板 D-30）：不进 usr `llms` 表、
// 不出现在配置面（LLM 设置页与聊天选择器均不列出）、不参与「设为默认」。
// 仅当 `llm-start.llm` 命中本保留名时给出只读配置（兼容既有 usr `defaultLLM: "echo"` 记录），
// 即**兜底语义保留、配置面收窄**。
//
// **「无任何可用 provider」的自动兜底归 router**（`src/lib/router` 的 `builtinFallback`：
// `Call` 的 `Provider` 为空或等于保留名 → 回落内置 echo）；llm 侧接线归 LR-11（见 40 §LR），
// 届时本保留名解析随旧 `LLMClient` 路径一并迁出。
const builtinFallbackName = "echo"

// builtinFallbackProvider 返回内置兜底的只读 provider 配置（protocol = echo，代码写死）：仅保留名
// 命中；其余 → nil（调用方回落 exe flags）。每次返回新副本，调用方之间零共享。
func builtinFallbackProvider(name string) *llmProviderCfg {
	if name != builtinFallbackName {
		return nil
	}
	return &llmProviderCfg{Name: builtinFallbackName, Protocol: ProtocolEcho, Model: builtinFallbackName}
}

// llmProviderCfg 是命中的 LLM provider 记录（llm-start.llm = provider name，usr 配置 llms 数组）。
// P1-1：让配置真正生效——baseUrl/apiKey/model 供客户端与请求体；temperature/maxOutputToken
// 按**存在性**判定（允许 0 生效；缺键 = 不发送）；thinking/reasoningEffort 决定是否发送
// reasoning_effort；maxToolIterations 单轮工具循环上限（0 = 未配置 → 调用方回落缺省 20）。
// P1-2：Protocol 决定请求协议（openai/responses；空/未知 → openai）。
// 口径 Z1（2026-09-25）：`maxOutputToken` = 最大输出 token（由旧 `maxTokens` 改名）；`maxContextToken` = 上下文窗口。
type llmProviderCfg struct {
	Name              string
	Protocol          string   // 协议类型（归一后：openai / responses）
	BaseURL           string   // 空 → 回落 exe flag -llm-base
	APIKey            string   // 空 → 不发送 Authorization
	Model             string   // 空 → 回落 exe flag -llm-model
	Temperature       *float64 // 存在即生效（含 0）；缺键 → 不发送
	MaxOutputToken    *int     // 最大输出 token（请求体 max_tokens；键 maxOutputToken，读时兼容旧名 maxTokens / max_tokens，口径 Z1）
	MaxContextToken   *int     // 上下文窗口大小（兜底归并窗口来源；键 maxContextToken；未配置 → 兜底不启用，口径 Z3）
	Thinking          *bool    // thinking=false → 不发送 reasoning_effort；缺键 → 由请求体决定
	ReasoningEffort   string   // thinking=true 时的 reasoning_effort（缺省 high）
	MaxToolIterations int      // >0 生效；否则回落缺省
}

// loadLLMProvider 从 user-config 加载 llm-start.llm（provider name）对应的 LLM 配置。
// 命中 usr llms → 记录配置；未命中 → **内置兜底保留名**（D-30，现仅 echo）；
// 两者均未命中 / 空 name → nil（调用方回落 exe flag 现状）。空 name → **不读配置**（零额外开销）。
func (s *Server) loadLLMProvider(instanceID string, selectedModel string) *llmProviderCfg {
	if selectedModel == "" {
		return nil
	}
	d, err := s.loadUserConfig(instanceID)
	if err != nil {
		logf("[chonkpilot-server] loadLLMProvider: UserConfigGet failed: %v\n", err)
		return providerFromConfig(nil, selectedModel)
	}
	return providerFromConfig(d, selectedModel)
}

// loadUserConfig 读本实例的 usr 配置对象（data-user-config-load，一次往返）。失败 → (nil, err)，
// 调用方按「无配置」处理（回落缺省）。B-37：newTurnCtx 一次读取后分发（provider 解析 +
// 运行时超时/重试解析），避免同一轮对同一配置源重复往返。
func (s *Server) loadUserConfig(instanceID string) (map[string]any, error) {
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		return nil, err
	}
	return res.Config, nil
}

// providerFromConfig 从**已读** usr 配置对象折算 provider name 对应的 LLM 配置（纯函数，不再读配置）：
// 空 name → nil（回落 exe flags）；命中 usr llms → 记录配置；未命中 → **内置兜底保留名**（D-30，仅 echo）；
// 其余（近似名 / 已失效名 / 未配置）→ nil（保持既有「未命中即回落 exe flags」语义不变）。
// 供 loadLLMProvider（llm-start / llm-simple）与子系统级入口 promptOptimiseSpec 共用同一折算口径。
//
// D-65：保留名 `auto` 先解析为**默认 LLM**（`resolveAutoLLMName`）再取记录 —— 首次调用永不把
// `auto` 当模型名下发；同档次候选切换只走轮次路径（见 turn.go / router `AutoCandidates`）。
func providerFromConfig(cfg map[string]any, selectedModel string) *llmProviderCfg {
	if selectedModel == "" {
		return nil
	}
	name := resolveAutoLLMName(cfg, selectedModel)
	if p := providerFromLLMs(cfg, name); p != nil {
		return p
	}
	return builtinFallbackProvider(name)
}

// ── auto 档位调度（D-65，2026-11-04 用户拍板）──────────────────────────────

// autoLLMName 是 auto 档位调度的保留 LLM 名：会话 / 场景把 llm 名写成 `auto` 时，首次解析到
// **默认 LLM**（usr `defaultLLM`），可重试失败后在**同档次**候选间切换 provider（见 router
// `CallOptions.AutoCandidates`）。
const autoLLMName = "auto"

// autoTierDelta 是「同档次」的能力值半径（±0.5，**含端点**）。
const autoTierDelta = 0.5

// isAutoLLMName 判定 llm 名是否保留名 `auto`（大小写不敏感；其余名字原样参与解析）。
func isAutoLLMName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), autoLLMName)
}

// resolveAutoLLMName 把 `auto` 解析为默认 LLM 名（usr `defaultLLM` → `llms[].name`）：
// 非 auto 原样返回；auto 且默认值未配置 / 仍为 auto → ""（= 系统默认，exe 启动参数）。
func resolveAutoLLMName(cfg map[string]any, name string) string {
	if !isAutoLLMName(name) {
		return name
	}
	if cfg == nil {
		return ""
	}
	def := data.LLMRefName(cfg["defaultLLM"], cfg["llms"])
	if def == "" || isAutoLLMName(def) {
		return ""
	}
	return def
}

// capabilityDims 是一条 `llms` 记录的能力双维度（D-65 口径更新 2026-11-04）：各 0~5、一位小数，
// 0 = 不具备。`Reasoning` = 推理能力（auto 档位基准）；`Vision` = 识图能力（图像感知替换的依据）。
type capabilityDims struct {
	Reasoning float64
	Vision    float64
}

// capabilityLegacyDim 是旧 `capabilities:["reasoning","vision"]` 声明式数组 → 档位的映射值：
// 旧口径只表达「具备 / 不具备」、无档位信息 → 具备即映射为中位档 3.0（与新增表单初值一致）。
const capabilityLegacyDim = 3.0

// capabilityValueValid 判定单个档位是否合法读值（0~5；越界脏值按「未声明」处理）。
func capabilityValueValid(v float64) bool {
	return !math.IsNaN(v) && v >= 0 && v <= 5
}

// llmCapabilityDims 取一条 `llms` 记录的能力双维度。
// declared=false = 该记录**未声明任何能力信息**（`capability` 与 `capabilities` 均缺省）→ 调用方按
// 「未声明」处理（档位基准回落默认 LLM / 不参与调度，兼容存量配置）。
//
// 读侧兼容（旧数据）：只有 `capabilities:["reasoning","vision"]` 而无 `capability` 时，出现哪一项即
// 视为「具备」→ 映射为 capabilityLegacyDim；`capabilities: []`（显式空数组）→ 两项均 0（不具备）。
func llmCapabilityDims(llm map[string]any) (capabilityDims, bool) {
	if raw, ok := llm["capability"]; ok && raw != nil {
		obj, _ := raw.(map[string]any)
		if obj == nil {
			return capabilityDims{}, false // 形态非法（写侧已拒绝）→ 按未声明处理，不阻断调度
		}
		var d capabilityDims
		if v, ok := configFloat(obj, "reasoning"); ok && capabilityValueValid(v) {
			d.Reasoning = v
		}
		if v, ok := configFloat(obj, "vision"); ok && capabilityValueValid(v) {
			d.Vision = v
		}
		return d, true
	}
	if raw, ok := llm["capabilities"]; ok && raw != nil {
		var d capabilityDims
		switch caps := raw.(type) {
		case []any:
			for _, item := range caps {
				d = dimsWithLegacyCap(d, str(item))
			}
		case []string:
			for _, item := range caps {
				d = dimsWithLegacyCap(d, item)
			}
		default:
			return capabilityDims{}, false
		}
		return d, true
	}
	return capabilityDims{}, false
}

// dimsWithLegacyCap 把旧声明式能力名折进档位（具备 → capabilityLegacyDim；未知名忽略）。
func dimsWithLegacyCap(d capabilityDims, capName string) capabilityDims {
	switch strings.ToLower(strings.TrimSpace(capName)) {
	case "reasoning":
		d.Reasoning = capabilityLegacyDim
	case "vision":
		d.Vision = capabilityLegacyDim
	}
	return d
}

// capabilityDimsOfLLM 取指定 provider 名（`llms` 记录）的能力双维度；未命中 / 未声明 → ok=false。
func capabilityDimsOfLLM(cfg map[string]any, name string) (capabilityDims, bool) {
	if cfg == nil || name == "" {
		return capabilityDims{}, false
	}
	for _, item := range asAnySlice(cfg["llms"]) {
		llm, _ := item.(map[string]any)
		if llm == nil {
			continue
		}
		if strings.TrimSpace(str(llm["name"])) != name {
			continue
		}
		return llmCapabilityDims(llm)
	}
	return capabilityDims{}, false
}

// autoCandidates 计算 auto 模式下的「同档次」候选 provider 名（有序 = usr `llms` 顺序；
// **不含首选** base，也不含显式 0 的 LLM）。
//
// 口径（D-65，2026-11-04：基准 = **reasoning** 维度）：
//   - 基准 = 默认 LLM（base）的 `reasoning`；同档 = |reasoning − 基准| ≤ 0.5（**含端点**）；
//   - 显式 `reasoning: 0` = 不具备 → 不参与（既不作首选也不作候选）；
//   - **缺省（未配置 capability）= 按默认 LLM 的能力值** → 天然同档（不让存量配置全部失效）；
//   - 基准未声明档位（默认 LLM 未配置能力值 / base 为空 = 系统默认）→ 不做档位限制，全部
//     可用（reasoning>0 或未声明）provider 都是候选（顺序 = 配置顺序）。
func autoCandidates(cfg map[string]any, base string) []string {
	if cfg == nil {
		return nil
	}
	baseDims, baseDeclared := capabilityDimsOfLLM(cfg, base)
	baseReasoning, baseDeclared := baseDims.Reasoning, baseDeclared && baseDims.Reasoning > 0
	var out []string
	for _, item := range asAnySlice(cfg["llms"]) {
		llm, _ := item.(map[string]any)
		if llm == nil {
			continue
		}
		name := strings.TrimSpace(str(llm["name"]))
		if name == "" || name == base {
			continue
		}
		dims, declared := llmCapabilityDims(llm)
		if declared && dims.Reasoning <= 0 {
			continue // 显式 0 = 不具备 → 不参与调度
		}
		if baseDeclared && declared {
			if math.Abs(dims.Reasoning-baseReasoning) > autoTierDelta+1e-9 {
				continue // 不同档次
			}
		}
		// baseDeclared && !declared → 缺省 = 按默认 LLM 的能力值 → 同档；
		// !baseDeclared → 基准未声明档位 → 不做档位限制。
		out = append(out, name)
	}
	return out
}

// autoVisionCandidates 计算 auto 模式「图像感知」的首选替换候选（有序 = 最优在前）：
//   - 触发前提：默认 LLM **不具备识图能力**（`vision` 未声明 / 0）——已具备 → nil（无需替换）；
//   - 候选条件：`vision > 0`（必须显式具备）、`reasoning > 0`、且 `reasoning` 在默认值 ±0.5
//     （含端点；默认值未声明档位时不限档，与 autoCandidates 同口径）；
//   - 排序：先按 `|reasoning − 默认值|` 升序，再按 `vision` 降序，最后按配置顺序。
//
// 宿主另需把名字折算为调用快照（`Server.autoImageFallbacks`）；本函数只做纯配置判定。
func autoVisionCandidates(cfg map[string]any, base string) []string {
	if cfg == nil {
		return nil
	}
	baseDims, baseDeclared := capabilityDimsOfLLM(cfg, base)
	if baseDeclared && baseDims.Vision > 0 {
		return nil // 默认 LLM 已具备识图能力 → 无需替换
	}
	baseReasoning, baseReasoningDeclared := baseDims.Reasoning, baseDeclared && baseDims.Reasoning > 0
	type visionCand struct {
		name     string
		distance float64
		vision   float64
		order    int
	}
	var cands []visionCand
	for i, item := range asAnySlice(cfg["llms"]) {
		llm, _ := item.(map[string]any)
		if llm == nil {
			continue
		}
		name := strings.TrimSpace(str(llm["name"]))
		if name == "" || name == base {
			continue
		}
		dims, declared := llmCapabilityDims(llm)
		if !declared || dims.Vision <= 0 || dims.Reasoning <= 0 {
			continue // 未声明 = 不具备识图能力；显式 0 同理
		}
		distance := 0.0
		if baseReasoningDeclared {
			distance = math.Abs(dims.Reasoning - baseReasoning)
			if distance > autoTierDelta+1e-9 {
				continue // 不同档次
			}
		}
		cands = append(cands, visionCand{name: name, distance: distance, vision: dims.Vision, order: i})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if math.Abs(cands[i].distance-cands[j].distance) > 1e-9 {
			return cands[i].distance < cands[j].distance
		}
		if math.Abs(cands[i].vision-cands[j].vision) > 1e-9 {
			return cands[i].vision > cands[j].vision
		}
		return cands[i].order < cands[j].order
	})
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.name)
	}
	return out
}

// autoImageFallbacks 把「图像感知」候选名折算为**调用快照**（spec + 该 provider 专属参数）：
// 首次调用若需替换，llm 侧整体切换到该快照（协议 / baseUrl / apiKey / model / 采样参数 / 思考力度）。
//
// 第二个返回值 needsVision = 默认 LLM **不具备识图能力**（auto 模式下遇到含图像请求才需要替换）；
// 它与「候选为空」一起用于降级留痕（无合格候选 → 仍用默认 LLM，不阻断本轮）。
func (s *Server) autoImageFallbacks(cfg map[string]any, base string, req StartReq) (fallbacks []ImageFallback, needsVision bool) {
	baseDims, baseDeclared := capabilityDimsOfLLM(cfg, base)
	needsVision = !baseDeclared || baseDims.Vision <= 0
	names := autoVisionCandidates(cfg, base)
	if len(names) == 0 {
		return nil, needsVision
	}
	out := make([]ImageFallback, 0, len(names))
	for _, name := range names {
		pcfg := providerFromLLMs(cfg, name)
		if pcfg == nil {
			continue
		}
		think, effort := resolveThinkEffort(pcfg, req)
		out = append(out, ImageFallback{
			Name:           name,
			Spec:           s.specFor(pcfg),
			Model:          pcfg.Model,
			Temperature:    pcfg.Temperature,
			MaxOutputToken: pcfg.MaxOutputToken,
			Think:          think,
			Effort:         effort,
		})
	}
	return out, needsVision
}

// providerFromLLMs 从已读 usr 配置对象取 provider name 对应的记录配置：命中返回配置；
// 未命中 / 无数据（cfg 为 nil）→ nil（由 providerFromConfig 决定后续（内置兜底保留名 / 回落 exe flag））。
func providerFromLLMs(cfg map[string]any, selectedModel string) *llmProviderCfg {
	for _, item := range asAnySlice(cfg["llms"]) {
		llm, _ := item.(map[string]any)
		if llm == nil {
			continue
		}
		name, _ := llm["name"].(string)
		if name != selectedModel {
			continue
		}
		// 协议类型（P1-2）：openai（默认，Chat Completions）/ responses（Responses API）。
		// 空/缺省 → openai（旧记录兼容）；未知值 → 明确 warn 并按 openai 兼容处理。
		protocol := strings.TrimSpace(str(llm["protocol"]))
		if protocol != "" && !llmProtocolKnown(protocol) {
			logf("[chonkpilot-server] loadLLMProvider: 未知 protocol=%q（llm=%q），按 openai 兼容处理\n", protocol, name)
		}
		cfg := &llmProviderCfg{
			Name:            name,
			Protocol:        NormalizeLLMProtocol(protocol),
			BaseURL:         strings.TrimSpace(str(llm["baseUrl"])),
			APIKey:          strings.TrimSpace(str(llm["apiKey"])),
			Model:           strings.TrimSpace(str(llm["model"])),
			ReasoningEffort: strings.TrimSpace(str(llm["reasoningEffort"])),
		}
		// 存在性判定（允许 0 生效）：键缺失才回落「不发送」。
		if t, ok := configFloat(llm, "temperature"); ok {
			cfg.Temperature = &t
		}
		// 最大输出 token（口径 Z1 改名）：新名 `maxOutputToken` 优先；读时兼容旧名
		// `maxTokens` / `max_tokens`（改名前的历史配置）→ 只读不写，不静默丢弃既有配置。
		if mt, ok := configFloat(llm, "maxOutputToken"); ok {
			n := int(mt)
			cfg.MaxOutputToken = &n
		} else if mt, ok := configFloat(llm, "maxTokens"); ok { // 读时兼容（改名前的历史键）
			n := int(mt)
			cfg.MaxOutputToken = &n
		} else if mt, ok := configFloat(llm, "max_tokens"); ok { // 读时兼容（更早的旧键名）
			n := int(mt)
			cfg.MaxOutputToken = &n
		}
		// 上下文窗口大小（口径 Z1 新增）：兜底归并窗口来源；未配置 → 兜底不启用。
		if mc, ok := configFloat(llm, "maxContextToken"); ok {
			n := int(mc)
			cfg.MaxContextToken = &n
		}
		if it, ok := configFloat(llm, "maxToolIterations"); ok && it > 0 {
			cfg.MaxToolIterations = int(it)
		}
		if b, ok := llm["thinking"].(bool); ok {
			v := b
			cfg.Thinking = &v
		}
		return cfg
	}
	return nil
}

// configFloat 从 map 取数值（总线 JSON 数字 → float64；兼容直接传入的 int）。
// 返回值 ok 表示键**存在**（0 亦存在 → 允许 0 生效）。
func configFloat(d map[string]any, key string) (float64, bool) {
	switch v := d[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// loadLLMRuntimeConfig 读 usr 配置的超时/重试四项（T-27 接线，2026-09-11 审计 B）：
// data-user-config-load 标量项（responseTimeout/streamTimeout/retryBudgetSec 单位秒，retryCount 次数）。
// 缺省语义：
//   - retryCount 按**存在性**判定（P0-A）：显式 0 = **不重试**；键缺失 → 回落 llmRetryCount=5。
//   - retryBudgetSec：缺失 → 60s；显式 <=0 → **关闭预算**（router 回退固定指数，传 -1）。
//   - 其余两项（responseTimeout/streamTimeout）保持既有口径：读取失败 / 键缺失 / 非正值 →
//     回落常量（ResponseTimeout=120s / StreamTimeout=60s），避免行为回归。
func (s *Server) loadLLMRuntimeConfig(instanceID string) (responseTimeout, streamTimeout, retryBudget time.Duration, retryCount int) {
	d, err := s.loadUserConfig(instanceID)
	if err != nil {
		logf("[chonkpilot-server] loadLLMRuntimeConfig: UserConfigGet failed: %v\n", err)
		return llmRuntimeConfigFrom(nil)
	}
	return llmRuntimeConfigFrom(d)
}

// llmRuntimeConfigFrom 从**已读** usr 配置对象折算超时/重试四项（纯函数，不再读配置）；
// 缺省回落与存在性判定语义见 loadLLMRuntimeConfig。
func llmRuntimeConfigFrom(d map[string]any) (responseTimeout, streamTimeout, retryBudget time.Duration, retryCount int) {
	responseTimeout, streamTimeout = ResponseTimeout, StreamTimeout
	retryBudget = llmRetryBudget
	retryCount = llmRetryCount
	if d == nil {
		return
	}
	if n, ok := configInt(d, "responseTimeout"); ok && n > 0 {
		responseTimeout = time.Duration(n) * time.Second
	}
	if n, ok := configInt(d, "streamTimeout"); ok && n > 0 {
		streamTimeout = time.Duration(n) * time.Second
	}
	// 预算：显式 <=0 = 关闭（-1 让 router 回退固定指数）；>0 = 该秒数。
	if n, ok := configInt(d, "retryBudgetSec"); ok {
		if n <= 0 {
			retryBudget = -1
		} else {
			retryBudget = time.Duration(n) * time.Second
		}
	}
	// P0-A：存在性判定——显式 0 = 不重试（n >= 0 同时挡掉负数脏值）。
	if n, ok := configInt(d, "retryCount"); ok && n >= 0 {
		retryCount = n
	}
	return
}

// llmContinueConfigFrom 从**已读** usr 配置折算语义级续写三项（RETRY-2，纯函数）：
//   - autoContinue（默认 true）：关 → 中断即终态，不进入宽限期（提示条仍由前端显示）；
//   - autoContinueDelaySec（默认 600）：中断后宽限期秒数（0 = 立即自动续写）；
//   - continueText（默认「继续」）：续写消息文案。
func llmContinueConfigFrom(d map[string]any) (enabled bool, delay time.Duration, text string) {
	enabled, delay, text = true, 600*time.Second, "继续"
	if d == nil {
		return
	}
	if v, ok := configBool(d, "autoContinue"); ok {
		enabled = v
	}
	if n, ok := configInt(d, "autoContinueDelaySec"); ok && n >= 0 {
		delay = time.Duration(n) * time.Second
	}
	if v, ok := configString(d, "continueText"); ok && strings.TrimSpace(v) != "" {
		text = v
	}
	return
}

// configBool 从 user-config 标量 map 取 bool（兼容单测直接传 bool；数据层读写侧对 bool 类键
// 按标量字符串落库 / 回读 —— 故同时接受 "true"/"false" 与 "1"/"0"，否则 `autoContinue=false`
// 这类显式关闭永远回落缺省 true）。
func configBool(d map[string]any, key string) (bool, bool) {
	if v, ok := d[key].(bool); ok {
		return v, true
	}
	if v, ok := d[key].(string); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

// configString 从 user-config 标量 map 取 string。
func configString(d map[string]any, key string) (string, bool) {
	if v, ok := d[key].(string); ok {
		return v, true
	}
	return "", false
}

// configInt 从 user-config 标量 map 取 int（总线 JSON 数字 → float64；兼容直接传 int 的单测）。
func configInt(d map[string]any, key string) (int, bool) {
	switch v := d[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}
