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
// 读法 = 全局 usr 配置（`UserConfigGet{}`，与 `reconcileLLMProviders` 同源；provider 名单属 usr
// 基线、无 instance 维度）；**不写、不缓存、不落日志**。**apiKey 不落任何日志**。
func (s *Server) ResolveBridgeTarget(name string) (BridgeTarget, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == bridgeDefaultName {
		// 系统默认 = exe flags 隐含默认（`exeDefaultSpec`：protocol=openai，无 apiKey）。
		return BridgeTarget{BaseURL: s.opts.LLMBase, Protocol: ProtocolOpenAI}, true
	}
	if s.cfg == nil {
		return BridgeTarget{}, false
	}
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{})
	if err != nil {
		logf("[chonkpilot-server] ResolveBridgeTarget: UserConfigGet failed: %v\n", err)
		return BridgeTarget{}, false
	}
	for _, item := range asAnySlice(res.Config["llms"]) {
		llm, _ := item.(map[string]any)
		if llm == nil {
			continue
		}
		if strings.TrimSpace(str(llm["name"])) != name {
			continue
		}
		return BridgeTarget{
			BaseURL:  orDefault(strings.TrimSpace(str(llm["baseUrl"])), s.opts.LLMBase),
			APIKey:   strings.TrimSpace(str(llm["apiKey"])),
			Protocol: strings.TrimSpace(str(llm["protocol"])),
		}, true
	}
	return BridgeTarget{}, false
}
