// LLM 桥接入口的**只读** provider 解析（BRIDGE-1，见
// docs/llm-bridge/LLM桥接端口设计方案.md §7 步骤 2）。
//
// 本文件只提供一份**只读查询**：给定 provider name（`/llm/<name>/...` 的 `<name>` 段）→
// `{baseURL, apiKey, protocol}`。**不 Register / 不 Call / 不经 canon**（§5）——对原 LLM
// 出网路径（router）**零影响**；也不新增 / 修改 / 删除任何 MQ 主题与 payload（纯读配置）。
//
// 读取链路复用既有 config 门面（`s.cfg.UserConfigGet`，与 `reconcileLLMProviders` 同一份
// usr `llms` 读取），**不改其行为**。
//
// 关键取舍（与设计稿 §3.3 对齐）：`protocol` 返回**原始配置值**（不经 `NormalizeLLMProtocol`
// 归一）——因为 llm server 的归一表把 `anthropic` 折叠为 `openai`（`llm.go NormalizeLLMProtocol`：
// 未知 → openai），而桥接入口必须按 protocol 区分鉴权头（`openai`/`responses` → Authorization:
// Bearer；`anthropic` → x-api-key；其它 → 400）。归一化只属 router 出网口径，桥接入口是**不经
// router 的透明反代**，故此处读原值，零行为变更。
package server

import (
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// bridgeDefaultName 是 LLM 桥接入口的**保留 provider 段**（设计稿 §3.2）：`/llm/default/...`
// = **系统默认 LLM**（exe flags 隐含默认，即 router 侧的保留名 `__default__`）。
const bridgeDefaultName = "default"

// BridgeTarget 是 LLM 桥接入口（`src/lib/llm/llmbridge`）解析出的**只读转发目标快照**。
//
// 语义与设计稿 §3.2/§3.3/§3.5 对齐：
//   - BaseURL  = 该 provider 的 API 前缀（空 → 已回落 exe flag `--llm-base`，与 `specFor` 同口径）；
//   - APIKey   = provider 配置的 apiKey（空 = 不注入鉴权头；**只随请求内存流转，不落日志**）；
//   - Protocol = `llms[].protocol` 的**原始值**（`openai` / `responses` / `anthropic` / 空 / 其它；
//     桥侧据此决定鉴权头，未识别 → 400）。
type BridgeTarget struct {
	BaseURL  string
	APIKey   string
	Protocol string
}

// ResolveBridgeTarget 只读解析 provider name 对应的桥接目标（BRIDGE-1 步骤 2）：
//   - name 为空 / == `default` → **系统默认**（exe flags 隐含默认，protocol = openai，无 apiKey）；
//   - 命中 usr `llms[].name` → 该记录（baseURL 空 → 回落 exe flag `--llm-base`；protocol 原值）；
//   - 未命中 → ok=false（桥侧按 §3.5 返回 404，不静默回落）。
//
// 读法 = 全局 usr 配置（provider 名单属 usr 基线、无 instance 维度）。
//
// LLM-P4：provider → 目标快照由 `reconcileLLMProviders`（启动 + `data-user-config-refresh`）
// **一次构建**，本函数先读快照（原实现每个桥接请求都 `UserConfigGet` 一次）；快照未命中
// （对账前 / 快照过期）→ 回落直读并顺带重建快照 —— 命中语义与改前逐字一致（不会因快照陈旧把
// 有效 provider 误判为 404）。**apiKey 不落任何日志**。
func (s *Server) ResolveBridgeTarget(name string) (BridgeTarget, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == bridgeDefaultName {
		// 系统默认 = exe flags 隐含默认（`exeDefaultSpec`：protocol=openai，无 apiKey）。
		return BridgeTarget{BaseURL: s.opts.LLMBase, Protocol: ProtocolOpenAI}, true
	}
	if s.cfg == nil {
		return BridgeTarget{}, false
	}
	if t, ok := s.cachedBridgeTarget(name); ok {
		return t, true
	}
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{})
	if err != nil {
		logf("[chonkpilot-server] ResolveBridgeTarget: UserConfigGet failed: %v\n", err)
		return BridgeTarget{}, false
	}
	s.storeBridgeTargets(res.Config) // 自愈：把快照刷新到本次读到的配置
	t, ok := bridgeTargetFromConfig(res.Config, name, s.opts.LLMBase)
	return t, ok
}

// bridgeTargetFromConfig 在 usr 配置对象里按 name 取转发目标（**首条**命中，与逐条查找同序）。
// baseURL 空 → 回落 exe flag `--llm-base`（与该 provider 出网口径一致）。
func bridgeTargetFromConfig(cfg map[string]any, name, baseURL string) (BridgeTarget, bool) {
	for _, item := range asAnySlice(cfg["llms"]) {
		llm, _ := item.(map[string]any)
		if llm == nil {
			continue
		}
		if strings.TrimSpace(str(llm["name"])) != name {
			continue
		}
		return BridgeTarget{
			BaseURL:  orDefault(strings.TrimSpace(str(llm["baseUrl"])), baseURL),
			APIKey:   strings.TrimSpace(str(llm["apiKey"])),
			Protocol: strings.TrimSpace(str(llm["protocol"])),
		}, true
	}
	return BridgeTarget{}, false
}

// storeBridgeTargets 由 usr 配置对象重建桥接目标快照（`reconcileLLMProviders` 调用；同批不读第二次库）。
func (s *Server) storeBridgeTargets(cfg map[string]any) {
	targets := map[string]BridgeTarget{}
	for _, item := range asAnySlice(cfg["llms"]) {
		llm, _ := item.(map[string]any)
		if llm == nil {
			continue
		}
		name := strings.TrimSpace(str(llm["name"]))
		if name == "" {
			continue
		}
		if _, dup := targets[name]; dup {
			continue // 重名 → 保留首条（与逐条查找口径一致）
		}
		targets[name] = BridgeTarget{
			BaseURL:  orDefault(strings.TrimSpace(str(llm["baseUrl"])), s.opts.LLMBase),
			APIKey:   strings.TrimSpace(str(llm["apiKey"])),
			Protocol: strings.TrimSpace(str(llm["protocol"])),
		}
	}
	s.bridgeMu.Lock()
	s.bridgeTargets = targets
	s.bridgeMu.Unlock()
}

// cachedBridgeTarget 读快照（未建快照 / 未命中 → ok=false，调用方回落直读）。
func (s *Server) cachedBridgeTarget(name string) (BridgeTarget, bool) {
	s.bridgeMu.RLock()
	defer s.bridgeMu.RUnlock()
	t, ok := s.bridgeTargets[name]
	return t, ok
}
