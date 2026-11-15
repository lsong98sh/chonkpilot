// plugin_caps_session.go —— 会话读能力（SESSION-QUERY-1，2026-11-15）。
//
// 把 `plugindata.Store` 的跨用户会话只读查询暴露为插件桥方法：
//   - plugin/v1/session.browse  枚举会话（可多目标用户）
//   - plugin/v1/session.read    单会话元数据 / 分页历史（可跨用户）
//   - plugin/v1/session.search  消息正文检索（可跨用户）
//
// 口径（2J-会话插件）：
//   - **self / top 由注入上下文决定**：插件把 `_meta.chonkpilot.session` / `.top_session`
//     原样带入（LLM 不可见、不可伪造），本层只用于结果锚点标记，不用于授权；
//   - **targets 的"自己"部分永远由宿主补**：targets 只表达"额外要看的别人"；
//   - 跨用户模式 usr → prj → private（`plugins.session.cross-user`），private 下显式他人 →
//     `permission_denied`（明确拒绝，不静默收窄）；
//   - 不覆盖框架已映射的 `session.list/get/history`（codegraph 正在用，形状不能变）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-llm/plugindata"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// sessionCrossUserKey 是跨用户模式配置键（usr 优先、prj 作默认；缺省 private）。
const sessionCrossUserKey = "plugins.session.cross-user"

// SetPluginData 注入插件数据能力实现（宿主在 plugindata.New 之后调用；nil = 会话查询不可用）。
func (s *Server) SetPluginData(st *plugindata.Store) {
	s.pluginData = st
}

// registerSessionCaps 注册会话能力（只读查询 + 建/续跑）由 SetPluginRegistry 在末尾调用。
func (s *Server) registerSessionCaps(reg *pluginbridge.Registry) {
	reg.Register("plugin/v1/session.browse", s.sessionBrowseCap)
	reg.Register("plugin/v1/session.read", s.sessionReadCap)
	reg.Register("plugin/v1/session.search", s.sessionSearchCap)
	reg.Register("plugin/v1/session.status", s.sessionStatusCap)
	reg.Register("plugin/v1/session.run", s.sessionRunCap)
	reg.Register("plugin/v1/session.open", s.sessionOpenCap)
}

type sessionBrowseCapIn struct {
	InstanceID string   `json:"instance_id"`
	WorkDir    string   `json:"work_dir"`
	Targets    []string `json:"targets"`
	IncludeSub bool     `json:"include_sub"`
	Kind       string   `json:"kind"`
	Since      string   `json:"since"`
	Limit      int      `json:"limit"`
	Offset     int      `json:"offset"`
	Session    string   `json:"session"`
	TopSession string   `json:"top_session"`
}

type sessionReadCapIn struct {
	InstanceID     string   `json:"instance_id"`
	WorkDir        string   `json:"work_dir"`
	Targets        []string `json:"targets"`
	User           string   `json:"user"`
	SessionID      string   `json:"session_id"`
	DialogID       string   `json:"dialog_id"`
	View           string   `json:"view"`
	BeforeTurnID   string   `json:"before_turn_id"`
	TargetMessages int      `json:"target_messages"`
	TargetBytes    int      `json:"target_bytes"`
	Session        string   `json:"session"`
	TopSession     string   `json:"top_session"`
}

type sessionSearchCapIn struct {
	InstanceID   string   `json:"instance_id"`
	WorkDir      string   `json:"work_dir"`
	Targets      []string `json:"targets"`
	Query        string   `json:"query"`
	Roles        []string `json:"roles"`
	SessionID    string   `json:"session_id"`
	Since        string   `json:"since"`
	Limit        int      `json:"limit"`
	ContextRunes int      `json:"context_runes"`
	Session      string   `json:"session"`
	TopSession   string   `json:"top_session"`
	// Mode = keyword（缺省）| semantic | hybrid（SESSION-SEMANTIC-1）。
	Mode string `json:"mode"`
}

type sessionStatusCapIn struct {
	InstanceID string   `json:"instance_id"`
	WorkDir    string   `json:"work_dir"`
	Targets    []string `json:"targets"`
	SessionID  string   `json:"session_id"`
	TurnID     string   `json:"turn_id"`
	WaitSec    int      `json:"wait_sec"`
	Session    string   `json:"session"`
	TopSession string   `json:"top_session"`
}

func (s *Server) sessionBrowseCap(ctx context.Context, sess *pluginbridge.Session, args json.RawMessage) (any, error) {
	if s.pluginData == nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeMethodNotFound, Msg: "会话查询能力未装配"}
	}
	var in sessionBrowseCapIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.browse: 参数解析失败"}
	}
	workDir := s.sessionCapWorkDir(in.InstanceID, in.WorkDir, sess)
	if workDir == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.browse: work_dir 必填"}
	}
	mode := s.sessionCrossUserMode(in.InstanceID)
	targets := s.sessionCapTargets(mode, in.Targets)
	return s.pluginData.BrowseSessions(ctx, plugindata.SessionBrowseRequest{
		PluginID:    sessionCapPluginID(sess),
		WorkDir:     workDir,
		Targets:     targets,
		Mode:        mode,
		SelfSession: strings.TrimSpace(in.Session),
		TopSession:  strings.TrimSpace(in.TopSession),
		IncludeSub:  in.IncludeSub,
		Kind:        strings.TrimSpace(in.Kind),
		Since:       strings.TrimSpace(in.Since),
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
}

// sessionStatusCap 查会话状态（持久化：轮次状态 + 最终 assistant 文本），可等待终态。
//
// 与 `task.result` 的差异：不依赖调用轮次仍存活（父轮结束后仍可查），见 2J §7.1。
func (s *Server) sessionStatusCap(ctx context.Context, sess *pluginbridge.Session, args json.RawMessage) (any, error) {
	if s.pluginData == nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeMethodNotFound, Msg: "会话查询能力未装配"}
	}
	var in sessionStatusCapIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.status: 参数解析失败"}
	}
	workDir := s.sessionCapWorkDir(in.InstanceID, in.WorkDir, sess)
	if workDir == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.status: work_dir 必填"}
	}
	sessionID, serr := resolveSessionRef(in.SessionID, in.Session, in.TopSession)
	if serr != nil {
		return nil, serr
	}
	if sessionID == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.status: session_id 缺失且无注入上下文"}
	}
	wait := in.WaitSec
	if wait < 0 {
		wait = 0
	}
	if wait > 600 {
		wait = 600
	}
	mode := s.sessionCrossUserMode(in.InstanceID)
	return s.pluginData.SessionStatus(ctx, plugindata.SessionStatusRequest{
		PluginID:    sessionCapPluginID(sess),
		WorkDir:     workDir,
		Targets:     in.Targets,
		Mode:        mode,
		SessionID:   sessionID,
		TurnID:      strings.TrimSpace(in.TurnID),
		WaitSec:     wait,
		SelfSession: strings.TrimSpace(in.Session),
		TopSession:  strings.TrimSpace(in.TopSession),
	})
}

func (s *Server) sessionReadCap(ctx context.Context, sess *pluginbridge.Session, args json.RawMessage) (any, error) {
	if s.pluginData == nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeMethodNotFound, Msg: "会话查询能力未装配"}
	}
	var in sessionReadCapIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.read: 参数解析失败"}
	}
	workDir := s.sessionCapWorkDir(in.InstanceID, in.WorkDir, sess)
	if workDir == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.read: work_dir 必填"}
	}
	targets := in.Targets
	if len(targets) == 0 && strings.TrimSpace(in.User) != "" {
		targets = []string{strings.TrimSpace(in.User)}
	}
	mode := s.sessionCrossUserMode(in.InstanceID)
	sessionID, serr := resolveSessionRef(in.SessionID, in.Session, in.TopSession)
	if serr != nil {
		return nil, serr
	}
	return s.pluginData.ReadSession(ctx, plugindata.SessionReadRequest{
		PluginID:       sessionCapPluginID(sess),
		WorkDir:        workDir,
		Targets:        targets,
		Mode:           mode,
		SessionID:      sessionID,
		DialogID:       strings.TrimSpace(in.DialogID),
		View:           strings.TrimSpace(in.View),
		BeforeTurnID:   strings.TrimSpace(in.BeforeTurnID),
		TargetMessages: in.TargetMessages,
		TargetBytes:    in.TargetBytes,
		SelfSession:    strings.TrimSpace(in.Session),
		TopSession:     strings.TrimSpace(in.TopSession),
	})
}

func (s *Server) sessionSearchCap(ctx context.Context, sess *pluginbridge.Session, args json.RawMessage) (any, error) {
	if s.pluginData == nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeMethodNotFound, Msg: "会话查询能力未装配"}
	}
	var in sessionSearchCapIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.search: 参数解析失败"}
	}
	workDir := s.sessionCapWorkDir(in.InstanceID, in.WorkDir, sess)
	if workDir == "" {
		return nil, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session.search: work_dir 必填"}
	}
	mode := s.sessionCrossUserMode(in.InstanceID)
	targets := s.sessionCapTargets(mode, in.Targets)
	sidFilter, serr := resolveSessionFilter(in.SessionID, in.Session, in.TopSession)
	if serr != nil {
		return nil, serr
	}
	searchMode := normalizeSessionSearchMode(in.Mode)
	var sem *plugindata.SemanticOptions
	if searchMode != "keyword" {
		sem = s.sessionSemanticOptions(in.InstanceID)
	}
	return s.pluginData.SearchSessions(ctx, plugindata.SessionSearchRequest{
		PluginID:     sessionCapPluginID(sess),
		WorkDir:      workDir,
		Targets:      targets,
		Mode:         mode,
		Query:        in.Query,
		Roles:        in.Roles,
		SessionID:    sidFilter,
		Since:        strings.TrimSpace(in.Since),
		Limit:        in.Limit,
		ContextRunes: in.ContextRunes,
		SelfSession:  strings.TrimSpace(in.Session),
		TopSession:   strings.TrimSpace(in.TopSession),
		SearchMode:   searchMode,
		Semantic:     sem,
	})
}

// sessionCapWorkDir 解析工作目录：显式参数 → 插件会话 work_dir → 实例登记。
func (s *Server) sessionCapWorkDir(instanceID, workDir string, sess *pluginbridge.Session) string {
	if wd := strings.TrimSpace(workDir); wd != "" {
		return wd
	}
	if sess != nil {
		if wd := strings.TrimSpace(sess.WorkDir()); wd != "" {
			return wd
		}
	}
	if instanceID != "" {
		if s.im != nil {
			if info, ok := s.im.Lookup(instanceID); ok {
				return info.WorkDir
			}
		}
	}
	return ""
}

func sessionCapPluginID(sess *pluginbridge.Session) string {
	if sess == nil {
		return ""
	}
	return strings.TrimSpace(sess.PluginID())
}

// sessionCapTargets 只在 public 模式补"全项目"缺省；private/llm 缺省 = 自己（由 Store 收口）。
func (s *Server) sessionCapTargets(mode string, targets []string) []string {
	if len(targets) == 0 && mode == "public" {
		return []string{"*"}
	}
	return targets
}

// sessionCrossUserMode 读跨用户模式：usr 视图优先，其次 prj 配置，最终 private。
//
// 非法值按 private 处理（安全侧收敛），不回落更宽松的下一层。
func (s *Server) sessionCrossUserMode(instanceID string) string {
	if s.cfg == nil {
		return "private"
	}
	// 注意：必须用 UserConfigGet（合并有效值），不能用 UserConfigView——后者是设置页/导出视图，
	// 会**剔除** `plugins.*`（插件配置只经插件桥按调用方身份读取）。
	if res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	}); err == nil {
		if raw, ok := res.Config[sessionCrossUserKey]; ok {
			if m := normalizeCrossUserMode(fmt.Sprint(raw)); m != "" {
				return m
			}
			return "private"
		}
	}
	res, err := s.cfg.ConfigKVList(facade.ConfigKVListRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err == nil {
		if raw, ok := res.List[sessionCrossUserKey]; ok {
			if m := normalizeCrossUserMode(raw); m != "" {
				return m
			}
			return "private"
		}
	}
	return "private"
}

func normalizeCrossUserMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "private":
		return "private"
	case "llm":
		return "llm"
	case "public":
		return "public"
	}
	return ""
}

// ── 语义检索配置（SESSION-SEMANTIC-1）─────────────────────────

const (
	sessionSemanticEnabledKey  = "plugins.session.semantic.enabled"
	sessionSemanticBaseURLKey  = "plugins.session.semantic.base-url"
	sessionSemanticAPIKeyKey   = "plugins.session.semantic.api-key"
	sessionSemanticModelKey    = "plugins.session.semantic.model"
	sessionSemanticMaxEmbedKey = "plugins.session.semantic.max-embed-per-call"
	sessionSemanticMinScoreKey = "plugins.session.semantic.min-score"
	sessionSemanticMaxScanKey  = "plugins.session.semantic.max-scan"
	// 阶段 2：全量索引 / HNSW。
	sessionSemanticAutoIndexKey = "plugins.session.semantic.auto-index"
	sessionSemanticHNSWKey      = "plugins.session.semantic.hnsw-threshold"
)

func normalizeSessionSearchMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "semantic":
		return "semantic"
	case "hybrid":
		return "hybrid"
	default:
		return "keyword"
	}
}

// sessionSemanticOptions 读语义检索配置（usr → prj → 缺省）。未启用 → nil。
func (s *Server) sessionSemanticOptions(instanceID string) *plugindata.SemanticOptions {
	if !sessionConfigTruthy(s.sessionConfigString(instanceID, sessionSemanticEnabledKey)) {
		return nil
	}
	return &plugindata.SemanticOptions{
		BaseURL:         s.sessionConfigString(instanceID, sessionSemanticBaseURLKey),
		APIKey:          s.sessionConfigString(instanceID, sessionSemanticAPIKeyKey),
		Model:           s.sessionConfigString(instanceID, sessionSemanticModelKey),
		MaxEmbedPerCall: sessionConfigInt(s.sessionConfigString(instanceID, sessionSemanticMaxEmbedKey), 64),
		MinScore:        sessionConfigFloat(s.sessionConfigString(instanceID, sessionSemanticMinScoreKey), 0.2),
		MaxScan:         sessionConfigInt(s.sessionConfigString(instanceID, sessionSemanticMaxScanKey), 2000),
		AutoIndex:       sessionConfigBoolDefault(s.sessionConfigString(instanceID, sessionSemanticAutoIndexKey), true),
		HNSWThreshold:   sessionConfigInt(s.sessionConfigString(instanceID, sessionSemanticHNSWKey), 512),
	}
}

// sessionConfigBoolDefault 解析布尔配置；键缺失（空串）→ 取缺省。
func sessionConfigBoolDefault(v string, def bool) bool {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return sessionConfigTruthy(v)
}

// sessionConfigString 按 usr 视图 → prj 配置读一个字符串键（空 = 未配置）。
func (s *Server) sessionConfigString(instanceID, key string) string {
	if s.cfg == nil {
		return ""
	}
	// UserConfigGet 才是含 `plugins.*` 的有效值（UserConfigView 会剔除插件配置）。
	if res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	}); err == nil {
		if raw, ok := res.Config[key]; ok {
			return strings.TrimSpace(fmt.Sprint(raw))
		}
	}
	if res, err := s.cfg.ConfigKVList(facade.ConfigKVListRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	}); err == nil {
		if raw, ok := res.List[key]; ok {
			return strings.TrimSpace(raw)
		}
	}
	return ""
}

func sessionConfigTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func sessionConfigInt(v string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return n
	}
	return def
}

func sessionConfigFloat(v string, def float64) float64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
		return f
	}
	return def
}

// resolveSessionRef 解析"本会话"引用（2J §2）：
//   - 省略 / "@self" → 注入的 session；缺失回退 top_session；
//   - "@top"         → 注入的 top_session；缺失回退 session；
//   - 其它 @ 前缀    → args_invalid（不猜）；
//   - 普通 id        → 原样（目标语义，仍走归属 / 跨用户校验）。
//
// 关键：self 只来自注入上下文，**永不**取工具参数——参数只能表达"我要操作谁"。
func resolveSessionRef(raw, self, top string) (string, error) {
	raw = strings.TrimSpace(raw)
	self = strings.TrimSpace(self)
	top = strings.TrimSpace(top)
	switch raw {
	case "", "@self":
		if self != "" {
			return self, nil
		}
		return top, nil
	case "@top":
		if top != "" {
			return top, nil
		}
		return self, nil
	}
	if strings.HasPrefix(raw, "@") {
		return "", &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "未知会话引用: " + raw}
	}
	return raw, nil
}

// resolveSessionFilter 是检索的限定解析：空 = 全部会话（不回落 self）；@self/@top 同 resolveSessionRef。
func resolveSessionFilter(raw, self, top string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return resolveSessionRef(raw, self, top)
}
