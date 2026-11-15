// sessionquery.go —— 会话跨用户只读查询（browse / read / search；SESSION-QUERY-1，2026-11-15）。
//
// 定位：`session.export`（export.go）只解决「已知 session_id、整库导出」；本文件补齐
// LLM 会话工具需要的三种读法：
//   - browse：按目标用户枚举会话（含子会话），带 self / top 锚点；
//   - read：单会话元数据 / 分页历史（before_turn_id + target_messages + target_bytes）；
//   - search：跨用户消息正文检索（Go 侧匹配 + snippet + 预算 + partial）。
//
// 铁律不变（C8）：本包运行在**宿主进程**，插件不得直接开库；本文件只经
// `github.com/chonkpilot/chonkpilot-data` 的公开读原语短开对话库。
//
// 跨用户授权（G-102）：
//   - 目标用户解析与模式判定都由本文件收口（`resolveTargets`）；
//   - `private` 下非当前用户 → `permission_denied`（**明确拒绝，不静默收窄成空结果**）；
//   - `llm` / `public` 放行，`_audit/audit.jsonl` 每次查询落一条汇总记录。
//
// 用户名安全：显式 target 必须是**单段目录名**（拒 `/` `\` `:` `..` 与控制字符），
// 防止 `users/../../...` 越出 users 根。该校验同时覆盖 `resolveUser`（export 一并受益）。
package plugindata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot-data"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

const (
	// sessionDBFile 是 prjusr 主库文件名（一树一库的目录索引库）。
	sessionDBFile = "chonkpilot.db"

	// maxCrossUserTargets 是一次查询最多展开的用户数（`targets:["*"]` 与显式列表同口径）。
	maxCrossUserTargets = 8

	// 扫描预算：超过即返回 partial=true（不无限扫全库）。
	maxBrowseSessions = 500
	maxSearchSessions = 200
	maxSearchMessages = 200_000

	defaultBrowseLimit = 50
	maxBrowseLimit     = 200
	defaultSearchLimit = 20
	maxSearchLimit     = 100

	// defaultContextRunes / maxContextRunes 是 snippet 的上下文窗口（rune）。
	defaultContextRunes = 60
	maxContextRunes     = 200

	// defaultHistoryMessages / defaultHistoryBytes 对齐数据面 `data-session-history` 缺省。
	defaultHistoryMessages = 50
	defaultHistoryBytes    = 200 << 10
	// maxReadMessageChars 是单条消息输出的正文上限（超出截断并标记，防止一次读爆 token）。
	maxReadMessageChars = 32 << 10
)

// errScanStopped 是内部哨兵：预算 / ctx 到点后中止 ForEach（不对外暴露）。
var errScanStopped = errors.New("plugindata: session scan stopped")

// ── 请求 / 结果 ────────────────────────────────────────────

// SessionBrowseRequest 按目标用户枚举会话。
type SessionBrowseRequest struct {
	PluginID    string
	WorkDir     string
	Targets     []string // 空 = 当前用户；["*"] = 全项目；可点名
	Mode        string   // private | llm | public（服务端按 usr→prj→private 解析后传入）
	SelfSession string   // 注入的 _meta.chonkpilot.session（只用于锚点标记）
	TopSession  string   // 注入的 _meta.chonkpilot.top_session
	IncludeSub  bool     // false = 仅顶层会话（parent_id == ""）
	Kind        string   // 可选：过滤 sessions.kind
	Since       string   // 可选：RFC3339；按 created_at 下界
	Limit       int
	Offset      int
}

// SessionMeta 是会话元数据（不含正文）。
type SessionMeta struct {
	User      string `json:"user"`
	SessionID string `json:"session_id"`
	DialogID  string `json:"dialog_id"`
	Title     string `json:"title,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Origin    string `json:"origin,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	TurnCount int    `json:"turn_count"`
	Self      bool   `json:"self,omitempty"`
	Top       bool   `json:"top,omitempty"`
	CrossUser bool   `json:"cross_user,omitempty"`
}

// SessionBrowseResult 是枚举结果（total = 过滤后总数；truncated = 预算截断）。
type SessionBrowseResult struct {
	Sessions     []SessionMeta `json:"sessions"`
	Total        int           `json:"total"`
	ScannedUsers int           `json:"scanned_users"`
	Truncated    bool          `json:"truncated"`
}

// SessionReadRequest 读单会话（元数据或分页历史）。
type SessionReadRequest struct {
	PluginID       string
	WorkDir        string
	Targets        []string // 至多 1 个有效用户；空 = 当前用户
	Mode           string
	SessionID      string
	DialogID       string // 可选快路径
	View           string // "meta" | "history"（Mode 已用于跨用户模式）
	BeforeTurnID   string
	TargetMessages int
	TargetBytes    int
	SelfSession    string
	TopSession     string
}

// SessionMessage 是历史输出的消息视图（裁剪字段，不输出 _key/tool_calls/_meta）。
type SessionMessage struct {
	MessageID   string `json:"message_id"`
	TurnID      string `json:"turn_id,omitempty"`
	Role        string `json:"role"`
	Kind        string `json:"kind,omitempty"`
	Content     string `json:"content"`
	CreatedAt   string `json:"created_at,omitempty"`
	SenderAgent string `json:"sender_agent,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// SessionTurn 是历史输出的轮次视图（messages 升序）。
type SessionTurn struct {
	TurnID    string           `json:"turn_id"`
	Status    string           `json:"status,omitempty"`
	Summary   string           `json:"summary,omitempty"`
	CreatedAt string           `json:"created_at,omitempty"`
	Messages  []SessionMessage `json:"messages"`
}

// SessionReadResult 是读取结果。
type SessionReadResult struct {
	User      string        `json:"user"`
	Session   SessionMeta   `json:"session"`
	Turns     []SessionTurn `json:"turns,omitempty"`
	HasMore   bool          `json:"has_more"`
	Truncated bool          `json:"truncated"`
}

// SessionSearchRequest 是跨会话正文检索。
type SessionSearchRequest struct {
	PluginID     string
	WorkDir      string
	Targets      []string
	Mode         string
	Query        string
	Roles        []string // 空 = user+assistant；["*"] = 全部
	SessionID    string   // 可选：限定会话
	Since        string   // 可选：RFC3339 下界
	Limit        int
	ContextRunes int
	SelfSession  string
	TopSession   string
	// SearchMode = keyword（默认）| semantic | hybrid（SESSION-SEMANTIC-1）。
	SearchMode string
	// Semantic 是语义检索后端配置；nil / 未启用 = semantic/hybrid 返回 semantic_disabled。
	Semantic *SemanticOptions
}

// SessionSearchHit 是一条消息级命中（必须带 snippet 与可跳转 id）。
type SessionSearchHit struct {
	User      string  `json:"user"`
	SessionID string  `json:"session_id"`
	DialogID  string  `json:"dialog_id"`
	Title     string  `json:"title,omitempty"`
	TurnID    string  `json:"turn_id,omitempty"`
	MessageID string  `json:"message_id"`
	Role      string  `json:"role"`
	Kind      string  `json:"kind,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	// SemanticScore / KeywordScore 仅在 semantic / hybrid 模式返回（便于调试与解释排序）。
	SemanticScore float64 `json:"semantic_score,omitempty"`
	KeywordScore  float64 `json:"keyword_score,omitempty"`
	Self          bool    `json:"self,omitempty"`
	Top           bool    `json:"top,omitempty"`
	CrossUser     bool    `json:"cross_user,omitempty"`
}

// SessionSearchResult 是检索结果。
type SessionSearchResult struct {
	Hits            []SessionSearchHit `json:"hits"`
	Total           int                `json:"total"`
	ScannedUsers    int                `json:"scanned_users"`
	ScannedSessions int                `json:"scanned_sessions"`
	ScannedMessages int                `json:"scanned_messages"`
	Truncated       bool               `json:"truncated"`
	Partial         bool               `json:"partial"`
	ElapsedMs       int64              `json:"elapsed_ms"`
	// SearchMode 回显实际模式（keyword / semantic / hybrid）。
	SearchMode string `json:"search_mode"`
	// Indexed = 本次补写的向量条数（semantic / hybrid）。
	Indexed int `json:"indexed,omitempty"`
	// Approx = 本次走了 HNSW 近似检索（结果可能漏掉少量低分命中）。
	Approx bool `json:"approx,omitempty"`
	// Index = 后台全量向量索引进度（仅启用 semantic 时返回）。
	Index *VectorIndexStatus `json:"index,omitempty"`
}

// ── 目标用户解析与授权 ──────────────────────────────────────

// normalizeSessionMode 归一化跨用户模式（非法值按最严格的 private 处理）。
func normalizeSessionMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "llm":
		return "llm"
	case "public":
		return "public"
	default:
		return "private"
	}
}

// validateUserName 校验目标用户是**单段目录名**：拒空 / 点段 / 路径分隔符 / 盘符冒号 / 控制字符。
func validateUserName(u string) error {
	u = strings.TrimSpace(u)
	if u == "" {
		return &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "user 不能为空"}
	}
	if u == "." || u == ".." {
		return &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "user 不允许点段: " + u}
	}
	if strings.ContainsAny(u, `/\:`) {
		return &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "user 必须是单段目录名: " + u}
	}
	if utf8.RuneCountInString(u) > 64 {
		return &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "user 超长（上限 64 字符）"}
	}
	for _, r := range u {
		if unicode.IsControl(r) {
			return &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "user 含控制字符"}
		}
	}
	return nil
}

// listUsers 只枚举 users 根下的目录名（不读内容、不写审计）；共享数据根形态 → unsupported。
func (s *Store) listUsers(workDir string) ([]string, error) {
	root, err := s.usersRoot(workDir)
	if err != nil {
		return nil, err
	}
	entries, rerr := os.ReadDir(root)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, nil
		}
		return nil, rerr
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// resolveTargets 收口「目标用户解析 + 跨用户模式 + G-102 授权 + 汇总审计」。
//
// 语义：
//   - targets 空 → 当前用户；
//   - targets = ["*"] → users 根下全部用户（public 缺省；llm 里显式写 * 也展开）；
//   - private 下出现非当前用户 → `permission_denied`（明确拒绝，不静默收窄）；
//   - public 下当前用户恒在列表首位（"优先自己"由调用方排序实现）；
//   - 最多 maxCrossUserTargets 个，超出截断。
//
// 审计：成功跨用户一条汇总（含 targets）；拒绝一条 denied。
func (s *Store) resolveTargets(action, pluginID, workDir, mode string, targets []string) ([]string, error) {
	cur := s.currentUser()
	mode = normalizeSessionMode(mode)

	expanded := make([]string, 0, len(targets))
	seen := make(map[string]bool, len(targets)+1)
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if t == "*" {
			users, err := s.listUsers(workDir)
			if err != nil {
				return nil, err
			}
			for _, u := range users {
				if !seen[u] {
					seen[u] = true
					expanded = append(expanded, u)
				}
			}
			continue
		}
		if err := validateUserName(t); err != nil {
			s.audit(auditRecord{
				Action: action, ActorUser: cur, Plugin: pluginID, WorkDir: workDir,
				TargetUser: t, Result: "denied", Reason: "invalid_target_user",
			})
			return nil, err
		}
		if !seen[t] {
			seen[t] = true
			expanded = append(expanded, t)
		}
	}

	out := make([]string, 0, len(expanded)+1)
	seenOut := make(map[string]bool, len(expanded)+1)
	add := func(u string) {
		if u == "" || seenOut[u] {
			return
		}
		seenOut[u] = true
		out = append(out, u)
	}

	if mode == "public" {
		add(cur) // 优先自己：恒在首位
	}
	for _, t := range expanded {
		if t == cur {
			add(t)
			continue
		}
		if mode == "private" {
			s.audit(auditRecord{
				Action: action, ActorUser: cur, Plugin: pluginID, WorkDir: workDir,
				TargetUser: t, Result: "denied", Reason: "cross_user_private",
			})
			return nil, &pluginbridge.Error{
				Code: pluginbridge.CodePermissionDenied,
				Msg:  "跨用户访问未开启（plugins.session.cross-user=private）",
			}
		}
		// 历史记录读取类（G-102）直接放行；其它动作给宿主追加授权口。
		allowed := isHistoryReadAction(action)
		if !allowed && s.opts.Authorize != nil {
			ok, err := s.opts.Authorize(action, pluginID, cur, workDir, t)
			if err != nil {
				s.audit(auditRecord{
					Action: action, ActorUser: cur, Plugin: pluginID, WorkDir: workDir,
					TargetUser: t, Result: "error", Reason: err.Error(),
				})
				return nil, &pluginbridge.Error{Code: pluginbridge.CodeInternal, Msg: err.Error()}
			}
			allowed = ok
		}
		if !allowed {
			s.audit(auditRecord{
				Action: action, ActorUser: cur, Plugin: pluginID, WorkDir: workDir,
				TargetUser: t, Result: "denied", Reason: "cross_user_not_history_read",
			})
			return nil, &pluginbridge.Error{
				Code: pluginbridge.CodeAuthRequired,
				Msg:  "跨用户访问仅放行历史记录读取（G-102，2026-11-03）",
			}
		}
		add(t)
	}
	if len(out) == 0 {
		add(cur)
	}
	if len(out) > maxCrossUserTargets {
		out = out[:maxCrossUserTargets]
	}
	if len(out) > 1 || out[0] != cur {
		s.audit(auditRecord{
			Action: action, ActorUser: cur, Plugin: pluginID, WorkDir: workDir,
			Result: "ok", Targets: out, Count: len(out),
		})
	}
	return out, nil
}

// targetUserDir 解析目标用户的 prjusr 目录。
//
// 共享数据根形态（DataDir 非空）没有 users/ 分支：当前用户读同一个根；**显式跨用户 → unsupported**
// （与 prj.users.list 同口径，不再静默忽略 user）。
func (s *Store) targetUserDir(workDir, user string) (string, error) {
	if s.opts.UsersRoot != nil {
		root, err := s.opts.UsersRoot(workDir)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, user), nil
	}
	if s.opts.DataDir != "" {
		if user != "" && user != s.currentUser() {
			return "", &pluginbridge.Error{
				Code: pluginbridge.CodeUnsupported,
				Msg:  "共享数据根形态（data_dir 非空）无 users/ 分支，不支持跨用户读取",
			}
		}
		return s.opts.DataDir, nil
	}
	root, err := s.usersRoot(workDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, user), nil
}

// ── 对话库枚举 ────────────────────────────────────────────

// dialogIDs 枚举 prjusr 根下合法对话库（按文件名排序；目录缺失 = 空）。
func dialogIDs(prjUsrDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(prjUsrDir, "dialogs"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		if id, ok := data.DialogIDFromFile(name); ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// openDialog 短开目标用户的对话库（调用方配对 release）。
func openDialog(prjUsrDir, dialogID string) (*data.DB, func(), error) {
	return data.OpenExistingDialog(filepath.Join(prjUsrDir, sessionDBFile), dialogID)
}

// sessionRows 读对话库的 sessions 表（跳过陈旧行）。
func sessionRows(db *data.DB, dialogID string) ([]data.Record, error) {
	out := []data.Record{}
	err := db.Table(tableSessions).ForEach(func(_ string, rec data.Record) error {
		if staleDialogRow(rec, dialogID) {
			return nil
		}
		out = append(out, rec)
		return nil
	})
	return out, err
}

// turnCounts 统计对话库内每会话轮次数（单事务遍历 turns）。
func turnCounts(db *data.DB) (map[string]int, error) {
	out := map[string]int{}
	err := db.Table(tableTurns).ForEach(func(_ string, rec data.Record) error {
		if sid, _ := rec["session_id"].(string); sid != "" {
			out[sid]++
		}
		return nil
	})
	return out, err
}

// metaOf 由 sessions 行组装 SessionMeta（self/top 锚点按注入值标记）。
func metaOf(user, dialogID string, rec data.Record, turnCount int, cur, self, top string) SessionMeta {
	sid := sval(rec, "session_id")
	if sid == "" {
		sid = sval(rec, "_key")
	}
	return SessionMeta{
		User:      user,
		SessionID: sid,
		DialogID:  dialogID,
		Title:     sval(rec, "title"),
		Kind:      sval(rec, "kind"),
		Origin:    sval(rec, "origin"),
		ParentID:  sval(rec, "parent_id"),
		CreatedAt: sval(rec, "created_at"),
		UpdatedAt: sval(rec, "updated_at"),
		TurnCount: turnCount,
		Self:      self != "" && sid == self,
		Top:       top != "" && sid == top,
		CrossUser: cur != "" && user != cur,
	}
}

func sval(rec data.Record, key string) string {
	v, _ := rec[key].(string)
	return v
}

// sinceFloor 归一化 since 为数据面时间格式，便于字典序比较（非法 → 零值 = 不过滤）。
func sinceFloor(since string) string {
	since = strings.TrimSpace(since)
	if since == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, since); err == nil {
		return t.UTC().Format(data.RFC3339FixedNano)
	}
	if t, err := time.Parse("2006-01-02", since); err == nil {
		return t.UTC().Format(data.RFC3339FixedNano)
	}
	return ""
}

// ── browse ────────────────────────────────────────────────

// BrowseSessions 枚举目标用户的会话（默认仅顶层；IncludeSub=true 含子会话）。
func (s *Store) BrowseSessions(ctx context.Context, req SessionBrowseRequest) (SessionBrowseResult, error) {
	users, err := s.resolveTargets("session.browse", req.PluginID, req.WorkDir, req.Mode, req.Targets)
	if err != nil {
		return SessionBrowseResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultBrowseLimit
	}
	if limit > maxBrowseLimit {
		limit = maxBrowseLimit
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	cur := s.currentUser()
	floor := sinceFloor(req.Since)

	all := []SessionMeta{}
	truncated := false
	for _, user := range users {
		if ctx.Err() != nil {
			truncated = true
			break
		}
		dir, derr := s.targetUserDir(req.WorkDir, user)
		if derr != nil {
			return SessionBrowseResult{}, derr
		}
		ids, derr := dialogIDs(dir)
		if derr != nil {
			return SessionBrowseResult{}, derr
		}
		for _, dialogID := range ids {
			db, release, oerr := openDialog(dir, dialogID)
			if oerr != nil {
				s.logf("plugindata: browse 跳过对话库 %s/%s: %v", user, dialogID, oerr)
				continue
			}
			recs, rerr := sessionRows(db, dialogID)
			counts, cerr := turnCounts(db)
			release()
			if rerr != nil {
				return SessionBrowseResult{}, rerr
			}
			if cerr != nil {
				return SessionBrowseResult{}, cerr
			}
			for _, rec := range recs {
				m := metaOf(user, dialogID, rec, counts[sval(rec, "session_id")], cur, req.SelfSession, req.TopSession)
				if !req.IncludeSub && m.ParentID != "" {
					continue
				}
				if req.Kind != "" && m.Kind != req.Kind {
					continue
				}
				if floor != "" && m.CreatedAt < floor {
					continue
				}
				all = append(all, m)
			}
			if len(all) >= maxBrowseSessions {
				truncated = true
				break
			}
		}
		if len(all) >= maxBrowseSessions {
			break
		}
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].UpdatedAt != all[j].UpdatedAt {
			return all[i].UpdatedAt > all[j].UpdatedAt
		}
		return all[i].SessionID < all[j].SessionID
	})
	total := len(all)
	if offset >= total {
		return SessionBrowseResult{Sessions: []SessionMeta{}, Total: total, ScannedUsers: len(users), Truncated: truncated}, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return SessionBrowseResult{
		Sessions:     all[offset:end],
		Total:        total,
		ScannedUsers: len(users),
		Truncated:    truncated,
	}, nil
}

func mval(rec data.Record, key string) string { return sval(rec, key) }

// ── read ──────────────────────────────────────────────────

// ReadSession 读单会话元数据或分页历史。
func (s *Store) ReadSession(ctx context.Context, req SessionReadRequest) (SessionReadResult, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return SessionReadResult{}, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "session_id 必填"}
	}
	users, err := s.resolveTargets("session.read", req.PluginID, req.WorkDir, req.Mode, req.Targets)
	if err != nil {
		return SessionReadResult{}, err
	}
	user, dialogID, db, release, err := s.openSessionForUsers(req.WorkDir, users, req.SessionID, req.DialogID)
	if err != nil {
		return SessionReadResult{}, err
	}
	defer release()

	var rec data.Record
	ok, gerr := db.Table(tableSessions).Get(req.SessionID, &rec)
	if gerr != nil {
		return SessionReadResult{}, gerr
	}
	if !ok || staleDialogRow(rec, dialogID) {
		return SessionReadResult{}, errSessionNotFound(req.SessionID)
	}
	counts, cerr := turnCounts(db)
	if cerr != nil {
		return SessionReadResult{}, cerr
	}
	meta := metaOf(user, dialogID, rec, counts[req.SessionID], s.currentUser(), req.SelfSession, req.TopSession)

	mode := strings.ToLower(strings.TrimSpace(req.View))
	if mode == "" || mode == "meta" {
		return SessionReadResult{User: user, Session: meta}, nil
	}
	if mode != "history" {
		return SessionReadResult{}, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "mode 只允许 meta | history"}
	}

	turns, err := readSessionHistory(ctx, db, req.SessionID, req.BeforeTurnID, req.TargetMessages, req.TargetBytes)
	if err != nil {
		return SessionReadResult{}, err
	}
	return SessionReadResult{
		User:      user,
		Session:   meta,
		Turns:     turns.Turns,
		HasMore:   turns.HasMore,
		Truncated: turns.Truncated,
	}, nil
}

type historyResult struct {
	Turns     []SessionTurn
	HasMore   bool
	Truncated bool
}

// readSessionHistory 按 (before_turn_id, target_messages, target_bytes) 分页读取。
// 语义对齐 `chonkpilot-agent/session/component/history.go`：从最新轮往回累计，达标停。
func readSessionHistory(ctx context.Context, db *data.DB, sessionID, beforeTurnID string, targetMessages, targetBytes int) (historyResult, error) {
	if targetMessages <= 0 {
		targetMessages = defaultHistoryMessages
	}
	if targetBytes <= 0 {
		targetBytes = defaultHistoryBytes
	}
	recs, _, err := db.Table(tableTurns).Query(data.Query{
		Where:   map[string]any{"session_id": sessionID},
		OrderBy: "created_at",
	})
	if err != nil {
		return historyResult{}, err
	}
	sort.SliceStable(recs, func(i, j int) bool {
		if sval(recs[i], "created_at") != sval(recs[j], "created_at") {
			return sval(recs[i], "created_at") < sval(recs[j], "created_at")
		}
		return sval(recs[i], "_key") < sval(recs[j], "_key")
	})
	if len(recs) == 0 {
		return historyResult{}, nil
	}

	end := len(recs)
	if beforeTurnID != "" {
		end = -1
		for i, rec := range recs {
			if sval(rec, "turn_id") == beforeTurnID || sval(rec, "_key") == beforeTurnID {
				end = i
				break
			}
		}
		if end < 0 {
			return historyResult{}, &pluginbridge.Error{
				Code: pluginbridge.CodeArgsInvalid, Msg: "before_turn_id 不在该会话中: " + beforeTurnID,
			}
		}
	}

	// 从最新往回累计到预算（消息条数 / 字节数）。
	var selected []data.Record
	msgCount, byteCount := 0, 0
	hasMore := false
	for i := end - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			hasMore = true
			break
		}
		rec := recs[i]
		msgs, _, merr := db.Table(tableMessages).Query(data.Query{
			Where:   map[string]any{"turn_id": sval(rec, "turn_id")},
			OrderBy: "created_at",
		})
		if merr != nil {
			return historyResult{}, merr
		}
		turnBytes := 0
		for _, m := range msgs {
			c := sval(m, "content")
			turnBytes += len(c)
		}
		if len(selected) > 0 && (msgCount+len(msgs) > targetMessages || byteCount+turnBytes > targetBytes) {
			hasMore = true
			break
		}
		selected = append(selected, rec)
		msgCount += len(msgs)
		byteCount += turnBytes
		if msgCount >= targetMessages || byteCount >= targetBytes {
			hasMore = i > 0
			break
		}
	}
	// 反转为升序。
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}

	out := make([]SessionTurn, 0, len(selected))
	truncated := false
	for _, rec := range selected {
		turnID := sval(rec, "turn_id")
		if turnID == "" {
			turnID = sval(rec, "_key")
		}
		msgs, _, merr := db.Table(tableMessages).Query(data.Query{
			Where:   map[string]any{"turn_id": turnID},
			OrderBy: "created_at",
		})
		if merr != nil {
			return historyResult{}, merr
		}
		sort.SliceStable(msgs, func(i, j int) bool {
			if sval(msgs[i], "created_at") != sval(msgs[j], "created_at") {
				return sval(msgs[i], "created_at") < sval(msgs[j], "created_at")
			}
			return sval(msgs[i], "_key") < sval(msgs[j], "_key")
		})
		view := make([]SessionMessage, 0, len(msgs))
		for _, m := range msgs {
			content, cut := truncateRunes(sval(m, "content"), maxReadMessageChars)
			truncated = truncated || cut
			view = append(view, SessionMessage{
				MessageID:   sval(m, "_key"),
				TurnID:      sval(m, "turn_id"),
				Role:        sval(m, "role"),
				Kind:        sval(m, "kind"),
				Content:     content,
				CreatedAt:   sval(m, "created_at"),
				SenderAgent: sval(m, "sender_agent"),
				Truncated:   cut,
			})
		}
		out = append(out, SessionTurn{
			TurnID:    turnID,
			Status:    sval(rec, "status"),
			Summary:   sval(rec, "summary"),
			CreatedAt: sval(rec, "created_at"),
			Messages:  view,
		})
	}
	return historyResult{Turns: out, HasMore: hasMore, Truncated: truncated}, nil
}

// ── search ────────────────────────────────────────────────

// SearchSessions 在目标用户的消息正文里检索（Go 侧匹配；预算内返回 partial）。
func (s *Store) SearchSessions(ctx context.Context, req SessionSearchRequest) (SessionSearchResult, error) {
	started := time.Now()
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return SessionSearchResult{}, &pluginbridge.Error{Code: pluginbridge.CodeArgsInvalid, Msg: "query 必填"}
	}
	users, err := s.resolveTargets("session.search", req.PluginID, req.WorkDir, req.Mode, req.Targets)
	if err != nil {
		return SessionSearchResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	ctxRunes := req.ContextRunes
	if ctxRunes <= 0 {
		ctxRunes = defaultContextRunes
	}
	if ctxRunes > maxContextRunes {
		ctxRunes = maxContextRunes
	}
	roles := normalizeRoles(req.Roles)
	floor := sinceFloor(req.Since)
	cur := s.currentUser()
	lowerQuery, tokens := queryTerms(query)

	// SESSION-SEMANTIC-1：semantic / hybrid 走独立实现（需读向量表 + embedding 网络调用，
	// 不能在消息扫描事务内做）。
	if searchMode := normalizeSearchMode(req.SearchMode); searchMode != "keyword" {
		res, serr := s.searchSemantic(ctx, req, users, searchMode, limit, ctxRunes, roles, floor)
		if serr != nil {
			return SessionSearchResult{}, serr
		}
		res.ScannedUsers = len(users)
		res.ElapsedMs = time.Since(started).Milliseconds()
		return res, nil
	}

	hits := []SessionSearchHit{}
	scannedSessions, scannedMessages := 0, 0
	partial := false

scanLoop:
	for _, user := range users {
		if ctx.Err() != nil {
			partial = true
			break
		}
		dir, derr := s.targetUserDir(req.WorkDir, user)
		if derr != nil {
			return SessionSearchResult{}, derr
		}
		ids, derr := dialogIDs(dir)
		if derr != nil {
			return SessionSearchResult{}, derr
		}
		for _, dialogID := range ids {
			db, release, oerr := openDialog(dir, dialogID)
			if oerr != nil {
				s.logf("plugindata: search 跳过对话库 %s/%s: %v", user, dialogID, oerr)
				continue
			}
			recs, rerr := sessionRows(db, dialogID)
			if rerr != nil {
				release()
				return SessionSearchResult{}, rerr
			}
			titles := make(map[string]string, len(recs))
			for _, rec := range recs {
				titles[sval(rec, "session_id")] = sval(rec, "title")
			}
			if req.SessionID != "" {
				scannedSessions++
			} else {
				scannedSessions += len(recs)
			}
			serr := db.Table(tableMessages).ForEach(func(key string, rec data.Record) error {
				if ctx.Err() != nil {
					return errScanStopped
				}
				if scannedMessages >= maxSearchMessages || len(hits) >= maxSearchLimit*4 {
					return errScanStopped
				}
				role := sval(rec, "role")
				if !roles[role] {
					return nil
				}
				sid := sval(rec, "session_id")
				if req.SessionID != "" && sid != req.SessionID {
					return nil
				}
				created := sval(rec, "created_at")
				if floor != "" && created != "" && created < floor {
					return nil
				}
				content := sval(rec, "content")
				if content == "" {
					return nil
				}
				scannedMessages++
				score, byteIdx := matchScore(content, lowerQuery, tokens)
				if score <= 0 {
					return nil
				}
				if user == cur {
					score *= 1.25 // public/多用户下"优先自己"
				}
				hits = append(hits, SessionSearchHit{
					User:      user,
					SessionID: sid,
					DialogID:  dialogID,
					Title:     titles[sid],
					TurnID:    sval(rec, "turn_id"),
					MessageID: key,
					Role:      role,
					Kind:      sval(rec, "kind"),
					CreatedAt: created,
					Snippet:   makeSnippet(content, byteIdx, ctxRunes),
					Score:     score,
					Self:      req.SelfSession != "" && sid == req.SelfSession,
					Top:       req.TopSession != "" && sid == req.TopSession,
					CrossUser: user != cur,
				})
				return nil
			})
			release()
			if serr != nil && !errors.Is(serr, errScanStopped) {
				return SessionSearchResult{}, serr
			}
			if errors.Is(serr, errScanStopped) {
				partial = true
				break scanLoop
			}
		}
		if scannedSessions >= maxSearchSessions {
			partial = true
			break
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].CreatedAt > hits[j].CreatedAt
	})
	total := len(hits)
	truncated := false
	if total > limit {
		hits = hits[:limit]
		truncated = true
	}
	return SessionSearchResult{
		Hits:            hits,
		Total:           total,
		ScannedUsers:    len(users),
		ScannedSessions: scannedSessions,
		ScannedMessages: scannedMessages,
		Truncated:       truncated,
		Partial:         partial,
		ElapsedMs:       time.Since(started).Milliseconds(),
		SearchMode:      "keyword",
	}, nil
}

// normalizeRoles 归一化角色过滤：空 = user+assistant；["*"] / ["all"] = 全部。
func normalizeRoles(in []string) map[string]bool {
	all := map[string]bool{"system": true, "user": true, "assistant": true, "tool": true}
	if len(in) == 0 {
		return map[string]bool{"user": true, "assistant": true}
	}
	out := map[string]bool{}
	for _, r := range in {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "*" || r == "all" {
			return all
		}
		if all[r] {
			out[r] = true
		}
	}
	if len(out) == 0 {
		return map[string]bool{"user": true, "assistant": true}
	}
	return out
}

// queryTerms 生成 (小写全文, 去重词元)：空格 / 常见中英标点分隔；全文短语同时参与匹配。
func queryTerms(query string) (string, []string) {
	lower := strings.ToLower(query)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', '，', '。', ';', '；', ':', '：', '、', '!', '！', '?', '？':
			return true
		}
		return false
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return lower, out
}

// matchScore 返回匹配分（全文短语权重 3、词元各权重 1）与首个命中字节偏移（无命中 = -1）。
func matchScore(content, lowerQuery string, tokens []string) (float64, int) {
	lower := strings.ToLower(content)
	first := -1
	score := 0.0
	if lowerQuery != "" {
		if n := strings.Count(lower, lowerQuery); n > 0 {
			score += float64(3 * n)
			if first < 0 {
				first = strings.Index(lower, lowerQuery)
			}
		}
	}
	for _, t := range tokens {
		if t == lowerQuery {
			continue
		}
		if n := strings.Count(lower, t); n > 0 {
			score += float64(n)
			if first < 0 {
				first = strings.Index(lower, t)
			}
		}
	}
	return score, first
}

// makeSnippet 取命中位置半径 ctx 的 rune 片段（越界加省略号）；byteIdx < 0 = 取开头。
func makeSnippet(content string, byteIdx, ctx int) string {
	if content == "" {
		return ""
	}
	runes := []rune(content)
	if byteIdx <= 0 {
		end := ctx
		if end > len(runes) {
			end = len(runes)
		}
		s := string(runes[:end])
		if end < len(runes) {
			s += "…"
		}
		return s
	}
	ri := utf8.RuneCountInString(content[:byteIdx])
	start := ri - ctx
	if start < 0 {
		start = 0
	}
	end := ri + ctx
	if end > len(runes) {
		end = len(runes)
	}
	s := string(runes[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(runes) {
		s += "…"
	}
	return s
}

// truncateRunes 按 rune 上限截断（超限返回 cut=true）。
func truncateRunes(s string, max int) (string, bool) {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s, false
	}
	runes := []rune(s)
	return string(runes[:max]) + "…", true
}
