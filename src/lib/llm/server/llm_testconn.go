// llm.test-connection 方法面：用「给定配置」**真实探活一次** LLM（只读探测，用户视角缺陷批 3 ⑮）。
//
// 背景（2026-09-20）：设置 → LLM 原先只有「取消 / 保存」，用户配完 provider 无法确认是否连通，
// 只能发一条消息试错。
//
// 契约（61-消息一览 §1「点分相对主题直通总线服务」）：
//
//	chonk.llm.test-connection{instance_id?, baseUrl, model, apiKey?, protocol?}
//	  → 同步应答 {ok, latency_ms, model_echo?, error{kind, message}}
//
// 唯一原则 = **只读探测**：不改当前生效配置（不写 usr llms、不动运行中 provider）、
// 不落库（不写会话/轮次/消息/历史表）——用**一次性 provider**（唯一名登记 → 探测后注销，
// 不进 usr 配置、不影响运行中 provider）发一次最小请求。
//
// 最小请求选型（成本最低优先）：复用**主路径同一条出网链路**（router `Call`：同一 Authorization
// 头 / 协议分派 / 超时 / 错误分类 → 「探的即真实调用路径」，不另造一份请求构造以免与主路径漂移）；
// 载荷 = 单条 user 消息 "hi" + max_tokens=1（Responses 协议自动映射 max_output_tokens）+
// 不下发工具；**首个非错误事件即判定连通**（HTTP 200 + 鉴权通过）→ 立即取消（不再等整段生成、
// 不再消费后续 token）。因此延迟 = 首包延迟（latency_ms）。
//
// 安全（硬要求）：应答与日志**不得出现 API Key 明文**——错误详情即便被上游回显（如
// `Incorrect API key provided: sk-…`）也一律经 maskLLMSecrets 脱敏。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-router"
)

const (
	// SubjectLLMTestConnection 探活方法面的相对主题。**点分形态**：GUI 桥与服务端上行入口按
	// 「点分相对主题直通总线服务」原样注入总线（61-消息一览 §1），无需登记单字方法白名单
	// （frontMethodSubjects 未收录的单字 type 会被静默丢弃）。
	SubjectLLMTestConnection = msgkeys.TopicLlmTestConnection

	// llmTestPrompt 探活请求的唯一输入（越短越省）。
	llmTestPrompt = "hi"
	// llmTestMaxTokens 探活请求的最大生成长度（1 = 成本最低）。
	llmTestMaxTokens = 1
	// llmTestKindInvalid 入参不合法（baseUrl/model 缺失）：非 LLM 侧错误分类，供前端区分文案。
	llmTestKindInvalid ErrKind = "invalid"
)

// llmTestTimeout 探活超时上限（合理上限可配常量；单测可临时替换）：既是外层 ctx 上限，
// 也作为首字节 / 流间隔超时下发 → 任何路径都不会长时间挂起。
var llmTestTimeout = 15 * time.Second

// llmTestConnReq 探活请求载荷（字段名与 usr llms 记录 / 前端表单同形：baseUrl/model/apiKey/protocol）。
type llmTestConnReq struct {
	InstanceID string `json:"instance_id,omitempty"`
	BaseURL    string `json:"baseUrl"`
	Model      string `json:"model"`
	APIKey     string `json:"apiKey,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

// onLLMTestConnection 受理探活方法面：校验入参 → 用临时配置真实请求一次 → 同步写回结果。
// 恒返回 nil（结果走 v.Result 信封），失败不抛总线错误——前端据 result.ok / error.kind 分流。
func (s *Server) onLLMTestConnection(ctx context.Context, _ string, v *mq.Value) error {
	req, err := parseLLMTestConnReq(v.Payload)
	if err != nil {
		v.Result = llmTestConnFail(llmTestKindInvalid, err.Error(), "")
		return nil
	}
	v.Result = s.probeLLMConnection(ctx, req)
	return nil
}

// parseLLMTestConnReq 解析并校验探活载荷：baseUrl / model 必填（缺 → invalid）。
func parseLLMTestConnReq(payload []byte) (*llmTestConnReq, error) {
	var req llmTestConnReq
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, errors.New("payload 不是合法 JSON 对象")
	}
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.Model = strings.TrimSpace(req.Model)
	req.APIKey = strings.TrimSpace(req.APIKey)
	if req.BaseURL == "" || req.Model == "" {
		return nil, errors.New("baseUrl/model 不能为空")
	}
	return &req, nil
}

// probeLLMConnection 用给定配置发一次最小请求（经 router，LR-11 起与主路径同一条出网链路）：
//   - 成功（首个非错误事件）→ {ok:true, latency_ms, model_echo?}，随后 cancel 收手；
//   - 失败 → {ok:false, error:{kind, message}}，kind = 既有错误分类
//     （network / timeout / rate_limit / server / auth / protocol）+ invalid（入参）。
func (s *Server) probeLLMConnection(ctx context.Context, req *llmTestConnReq) map[string]any {
	// SSRF 防护（硬要求）：探活目标 baseUrl 全由请求方提供，须先校验再出网（见 validateLLMTarget）。
	if err := s.validateLLMTarget(req.BaseURL); err != nil {
		return llmTestConnFail(llmTestKindInvalid, err.Error(), req.APIKey)
	}
	ctx, cancel := context.WithTimeout(ctx, llmTestTimeout)
	defer cancel()

	// 一次性 provider：**唯一名登记 → defer 注销**，不入 usr 配置、不影响运行中 provider
	// （对账集合不含本名，`Reconcile` 亦会回收；此处显式注销保证零残留）。
	proto := NormalizeLLMProtocol(req.Protocol)
	spec := router.Spec{
		Name:         "__probe__" + newID(),
		Protocol:     proto,
		BaseURL:      req.BaseURL,
		APIKey:       req.APIKey,
		DefaultModel: req.Model,
	}
	if err := s.rt.Register(spec); err != nil {
		return llmTestConnResultFromErr(err, req.APIKey)
	}
	defer func() { _ = s.rt.Unregister(spec.Name) }()

	maxTokens := llmTestMaxTokens
	start := time.Now()
	ch, err := s.chat(ctx, spec, []ChatMsg{{Role: "user", Content: llmTestPrompt}}, nil, ChatOptions{
		MaxTokens:       &maxTokens,
		ResponseTimeout: llmTestTimeout,
		StreamTimeout:   llmTestTimeout,
	})
	if err != nil {
		return llmTestConnResultFromErr(err, req.APIKey)
	}
	for {
		select {
		case <-ctx.Done():
			// 首包未到即超时（Do 的首字节超时通常先触发；此处兜底流内无事件）
			return llmTestConnFail(ErrTimeout, "探活超时（"+llmTestTimeout.String()+"）", req.APIKey)
		case ev, ok := <-ch:
			if !ok {
				// 流已结束但一个事件都没给出 → 无有效响应（不判成功）
				return llmTestConnFail(ErrProtocol, "探活无响应（流已结束，未返回任何事件）", req.APIKey)
			}
			if ev.Err != nil {
				return llmTestConnResultFromErr(ev.Err, req.APIKey)
			}
			latency := time.Since(start)
			cancel() // 拿到首包即收手（不等整段生成、不再消费 token）
			out := map[string]any{"ok": true, "latency_ms": latency.Milliseconds()}
			if ev.Model != "" {
				out["model_echo"] = ev.Model
			}
			logf("[chonkpilot-server] llm.test-connection ok=true protocol=%s model=%q latency=%dms model_echo=%q\n",
				proto, req.Model, latency.Milliseconds(), ev.Model)
			return out
		}
	}
}

// validateLLMTarget 校验探活目标 baseUrl（**SSRF 防护**）：仅允许 `http`/`https`；在**要求认证的
// 形态**（browser/gui，`RequireAuth()=true`）下进一步拒绝 loopback / 私网 / link-local /
// 组播 / 未指定 及云元数据（`169.254.169.254`）等**内网目标**（含域名解析后的地址）——避免远端
// 客户端把服务端当作内网 SSRF 跳板（结合入口鉴权 §4.6：未认证不可达本方法面）。
//
// desktop 形态（本机单用户、无网络暴露面，`RequireAuth()=false`）**只校验协议**：用户常把
// provider 指向本机（如 Ollama `http://127.0.0.1:11434`），拒绝内网会破坏 desktop 零摩擦。
func (s *Server) validateLLMTarget(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return errors.New("baseUrl 无法解析")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("baseUrl 仅支持 http/https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("baseUrl 缺少主机名")
	}
	if !s.RequireAuth() {
		return nil // desktop：本机单用户，不限制内网目标
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedTargetIP(ip) {
			return errors.New("baseUrl 指向本机/内网/元数据地址，已拒绝（SSRF 防护）")
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return errors.New("baseUrl 主机无法解析")
	}
	for _, ip := range ips {
		if isBlockedTargetIP(ip) {
			return errors.New("baseUrl 解析到本机/内网/元数据地址，已拒绝（SSRF 防护）")
		}
	}
	return nil
}

// isBlockedTargetIP 判定 IP 是否为**内网/保留**目标（SSRF 拒绝集）：loopback / 私网 /
// link-local（含 IPv6）/ 组播 / 未指定 / 云元数据 `169.254.169.254`。
func isBlockedTargetIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// 云元数据端点（属 link-local，上面已覆盖；显式列出以明意图）。
	return ip.Equal(net.ParseIP("169.254.169.254"))
}

// llmTestConnResultFromErr 把探活中的错误转结果信封：*LLMError 用其分类，其余按协议错误。
func llmTestConnResultFromErr(err error, apiKey string) map[string]any {
	var le *LLMError
	if errors.As(err, &le) {
		return llmTestConnFail(le.Kind, le.Message, apiKey)
	}
	return llmTestConnFail(ErrProtocol, err.Error(), apiKey)
}

// llmTestConnFail 组装失败结果信封：message 与主路径同形（`[kind] <msg>`，前端复用
// errorMessage 分类映射出人话文案），并**强制脱敏**（上游回显的 Key 不得外泄）。
func llmTestConnFail(kind ErrKind, msg, apiKey string) map[string]any {
	if kind == "" {
		kind = ErrProtocol
	}
	logf("[chonkpilot-server] llm.test-connection ok=false kind=%s message=%s\n",
		kind, maskLLMSecrets(msg, apiKey))
	return map[string]any{
		"ok":         false,
		"latency_ms": 0,
		"error": map[string]any{
			"kind":    string(kind),
			"message": maskLLMSecrets(llmErr(kind, msg).Error(), apiKey),
		},
	}
}

// llmSecretRe 常见密钥形态（`sk-…` / `Bearer <token>`）：上游把 Key 回显进错误体时的兜底脱敏。
var llmSecretRe = regexp.MustCompile(`(?i)(sk-|Bearer\s+)[A-Za-z0-9_\-\.]{6,}`)

// maskLLMSecrets 脱敏文本里的密钥：先替换本次请求的 apiKey 原文（长度 ≥4 才整串替换，
// 避免误伤正文里的短串），再按通用密钥形态兜底。
func maskLLMSecrets(s, apiKey string) string {
	if len(apiKey) >= 4 {
		s = strings.ReplaceAll(s, apiKey, "sk-***")
	}
	return llmSecretRe.ReplaceAllString(s, "$1***")
}
