// 上行面：`POST /publish`（{type,payload} → 白名单 → 注入服务端进程内总线 → 结果信封）。
//
// 分派规则与 GUI 桥（chonkpilot-gui/bridge/bridge.go PublishEvent）**对齐**：
//
//	llm-start                     → 拆两步 session-start + session-send（21-llm-server）
//	gui.*                         → 本包 browser 本地面（guiDo：native 明确不支持 / 等价面）
//	data-<domain>-<action>        → 总线 persist 数据面（注入 req_id + instance_id）
//	filesys.*（点分白名单）        → 总线直通（服务端托管的 filesys 应答；work_dir 由服务端绑定）
//	单字方法面（frontMethodSubjects）→ 相对域主题（注入 instance_id）
//	其余单字 type                  → 纯前端事件（UI 本地）→ 不注入总线、不报错（与桥一致）
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// allowedDottedPrefixes 点分相对主题白名单（上行可注入总线的 filesys 面；其余点分主题拒绝）。
// 2026-09-20：新增 `llm.test-connection`（批 3 · ⑮「LLM 测试连接」，只读探活）与
// `memory.flush`（批 3 · ⑱「立即沉淀」，手动触发一次记忆沉淀）；GUI 桥本就有「点分直通总线」
// 分支无需改，browser 入口需在此显式放行，否则返回 topic not allowed。
// 2026-10-06（P2-10）：新增前缀 `vfts.`（vfts 管理面：vfts.dict.get / vfts.dict.set /
// vfts.reindex —— 插件订阅、同主题 promise 写回 v.Result）。
var allowedDottedPrefixes = []string{"filesys.", "vfts.", msgkeys.TopicLlmTestConnection, msgkeys.TopicMemoryFlush}

// frontMethodSubjects 前端单字方法 type → 相对域主题（与桥 frontMethodSubjects 同表；
// 未列出的单字 type 视为纯前端事件，不注入总线）。
//
// 注：认证域 `login-register` / `login-in` / `login-out` **不在此表** —— 需入口承载令牌
// （`Set-Cookie` / 清 cookie）→ 走 `handleLogin` 专用分支（login.go，61 §4.6）。
var frontMethodSubjects = map[string]string{
	msgkeys.TopicLlmSend:        "session-send",
	msgkeys.TopicLlmCancel:      "session-cancel",
	msgkeys.TopicAskUserReply:   "session-ask-reply",
	msgkeys.TopicTaskStop:       msgkeys.TopicTaskStop,
	msgkeys.TopicTaskBackground: msgkeys.TopicTaskBackground,
	msgkeys.TopicToolRetry:      msgkeys.TopicToolRetry,
	msgkeys.TopicPromptOptimise: msgkeys.TopicPromptOptimise,
	// 实例消息（61 §4.1，阶段 2a）：instance-claim = 前端启动认领（请求-响应 → v.Result 作为
	// /publish 响应回发起者）；instance-heartbeat = **发布方改为前端 SPA**（客户端续期 30s，
	// `-tags split` 门控语义不变：单体形态前端不发布、不判超时）。
	msgkeys.TopicInstanceClaim:     msgkeys.TopicInstanceClaim,
	msgkeys.TopicInstanceHeartbeat: msgkeys.TopicInstanceHeartbeat,
	// 客户端能力面 type（schema `clientTopic`，由 genmsg 生成 msgkeys.MsgClientTopics* 常量引用）。
	msgkeys.MsgClientTopicsToolsList:     msgkeys.TopicMcpToolsList,
	msgkeys.MsgClientTopicsPromptsList:   msgkeys.TopicMcpPromptsList,
	msgkeys.MsgClientTopicsResourcesList: msgkeys.TopicMcpResourcesList,
	msgkeys.TopicMcpToolsWait:            msgkeys.TopicMcpToolsWait,
	// 场景向导方法面（2026-10-04）：探测 / 合成 / 生成 / 稍后（server 侧处理，写回 v.Result）。
	msgkeys.TopicAgentWizardProbe:    msgkeys.TopicAgentWizardProbe,
	msgkeys.TopicAgentWizardCompose:  msgkeys.TopicAgentWizardCompose,
	msgkeys.TopicAgentWizardGenerate: msgkeys.TopicAgentWizardGenerate,
	msgkeys.TopicAgentWizardSkip:     msgkeys.TopicAgentWizardSkip,
}

// capabilityListKeys 客户端能力面 topic → 结果数组键（按本实例作用域过滤用）。
var capabilityListKeys = map[string]string{
	msgkeys.MsgClientTopicsToolsList:     msgkeys.FieldTools,
	msgkeys.MsgClientTopicsPromptsList:   msgkeys.FieldPrompts,
	msgkeys.MsgClientTopicsResourcesList: msgkeys.FieldResources,
}

// browserUnsupported 是 browser 形态**明确禁用**的 native 能力（19 §3.3/§6：必须替换或禁用；
// 不得静默失败、不得假成功）。返回错误信封 `{ok:false,error}` + errors。
// 键 = gui.<action> 的 **action 后缀**（guiDo 入参，见 publishEvent `strings.TrimPrefix(typ, "gui.")`）；
// 故 `gui.file.save` 的键须为 **`file.save`**（历史误置全名 `gui.file.save` → 恒不命中，已订正）；
// `prompt-optimise` 的 action 后缀恰与契约主题同名 → 以 msgkeys 常量引用（值不变）。
// 说明：`dir.open-dialog` 的**消息面**保持"明确不支持"（61 §1 已定 result = `{path?}`，browser
// 无人机选择器可取 path；改 result = 改 payload，需用户确认）——**等价面**由入口新增的
// 非 MQ HTTP 路由 `GET /dirs` 提供（本 instance 作用域只读面，见 19 §8.9）。
var browserUnsupported = map[string]string{
	"dir.open-dialog":           "本地目录选择器（native）；等价面 = 入口 GET /dirs（列本 instance 允许目录）",
	"pick-executable":           "本地文件选择器（native）",
	"dir.open":                  "以新进程打开目录（native）",
	"console.open":              "系统控制台打开目录（native）",
	"reveal":                    "资源管理器定位（native）",
	"file.save":                 "系统「另存为」对话框（native）；browser 形态请用页面下载/剪贴板（见批 3 · ⑯）",
	"open-with":                 "系统「打开方式」（native）",
	"capture":                   "窗口截图（native）",
	"toolchain.detect":          "本机工具链探测（native）",
	"system.builtins":           "exe 同级只读内置项（native）",
	"window.status":             "原生窗口控制（browser 无原生窗口）",
	"upload":                    "附件上传（browser 形态未提供 /show 字节面）",
	msgkeys.TopicPromptOptimise: "提示词优化（browser 形态未接线 LLM 优化面）",
}

// handlePublish 上行入口：解析 {type,payload} → 分派 → 应答 `{ok,result,errors}`
// （HTTP 恒 200，与 GUI /publish 信封一致；错误收进 errors 由发送端自查）。
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{msgkeys.FieldOk: false, msgkeys.FieldErrors: []string{"POST required"}})
		return
	}
	var body struct {
		Type    string `json:"type"`
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{msgkeys.FieldOk: false, msgkeys.FieldErrors: []string{"bad body: " + err.Error()}})
		return
	}
	if strings.TrimSpace(body.Type) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{msgkeys.FieldOk: false, msgkeys.FieldErrors: []string{"type required"}})
		return
	}
	// 认证域（61 §4.6；阶段 2b-1）：入口承载令牌（Set-Cookie / 清 cookie），应答里的内部
	// token 字段在此取走并剥除 → **令牌不进前端 payload**（见 login.go）。
	if isLoginTopic(body.Type) {
		s.handleLogin(w, r, body.Type, body.Payload)
		return
	}
	result, errs := s.publishEvent(body.Type, body.Payload, cookieToken(r))
	emsg := make([]string, 0, len(errs))
	for _, e := range errs {
		emsg = append(emsg, e.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{msgkeys.FieldOk: len(emsg) == 0, "result": result, msgkeys.FieldErrors: emsg})
}

// publishEvent 按白名单分派一次上行请求（返回结果与错误）。
// token 是**入口从连接层取**的当前令牌（cookie；空 = 未登录）——由入口注入请求载荷，
// 服务端**不采信**前端 payload 里的身份字段（61 §4.6）。
func (s *Server) publishEvent(typ, payloadJSON, token string) (any, []error) {
	switch {
	case typ == msgkeys.TopicLlmStart:
		s.splitLLMStart(payloadJSON)
		return nil, nil
	case strings.HasPrefix(typ, "gui."):
		return s.guiDo(strings.TrimPrefix(typ, "gui."), []byte(payloadJSON))
	case strings.HasPrefix(typ, "data-"):
		// 阶段 4 第二/三/四批（41 G-34 / G-35 / G-36）：门面已覆盖的域（config 类 · session 域 ·
		// tasktree / knowledge / filelist / scenario / memory）**优先走 data 门面**（服务端进程内
		// 直调，不经 MQ；与 GUI 桥 dataViaFacade 同构，见 facade_session.go / facade_domains.go）；
		// 未命中/未注入门面 → 回落总线 persist 路径（两条路径应答载荷逐字一致）。
		return s.dataCall(typ, payloadJSON)
	case strings.Contains(typ, "."):
		if !dottedAllowed(typ) {
			return nil, []error{fmt.Errorf("topic not allowed: %s（browser 形态上行白名单外）", typ)}
		}
		// filesys.*：work_dir 由服务端绑定（覆盖请求载荷），浏览器端不得自报任意目录。
		if strings.HasPrefix(typ, "filesys.") {
			bound, err := s.bindFilesys(payloadJSON)
			if err != nil {
				return failReply("filesys: %v", err)
			}
			return s.publishV(typ, bound)
		}
		return s.publishV(typ, s.injectInstance(payloadJSON, token))
	}
	if subject, ok := frontMethodSubjects[typ]; ok {
		result, errs := s.publishV(subject, s.injectInstance(payloadJSON, token))
		// 客户端能力面（tools/prompts/resources-list）返回全量条目（含所有 instance），
		// 按本实例作用域过滤后再回前端（与桥 filterCapabilityScope 同判据）。
		s.filterCapabilityScope(typ, result)
		return result, errs
	}
	// 白名单外的单字 type = 纯前端事件（UI 本地；GUI 桥同样静默忽略）→ 不注入总线。
	return nil, nil
}

// dottedAllowed 点分主题是否在白名单内。
func dottedAllowed(subject string) bool {
	for _, p := range allowedDottedPrefixes {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	return false
}

// bindFilesys 把 filesys.* 请求的 work_dir **强制绑定为本 instance 的服务端 work_dir**
// （浏览器端自报的任意 work_dir 一律被覆盖 → 不能借 payload 越权读写其它目录）。
// 只做入口侧绑定/校验，**不改 filesys 内部语义**（filesys 仍以 work_dir 为管理单位、
// withinWorkDir 为第二道；越界 path 由其自身拒绝返回 forbidden）。
// 同时补 instance_id（watch 声明者归属需要；已存在不覆盖）。
func (s *Server) bindFilesys(payloadJSON string) (string, error) {
	if strings.TrimSpace(s.workDir) == "" {
		return "", fmt.Errorf("服务端 work_dir 未绑定（--work-dir 缺失）")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	m[msgkeys.FieldWorkDir] = s.workDir
	if _, ok := m[msgkeys.FieldInstanceId]; !ok {
		m[msgkeys.FieldInstanceId] = s.instanceID
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// splitLLMStart 把前端 llm-start 拆为 server 协议两步（与桥 splitLLMStart 同语义）：
//
//	session-start {req_id, instance_id, session, turn, llm, think, effort, scenario_id, continue}
//	session-send  {instance_id, session, turn, type:"text-user", content}
func (s *Server) splitLLMStart(payloadJSON string) {
	var p struct {
		Session    string `json:"session"`
		SessionID  string `json:"session_id"`
		Turn       string `json:"turn"`
		TurnID     string `json:"turn_id"`
		LLM        string `json:"llm"`
		Think      string `json:"think"`
		Effort     string `json:"effort"`
		ScenarioID string `json:"scenario_id"`
		Continue   bool   `json:"continue"`
		Q          string `json:"q"`
		Content    string `json:"content"`
	}
	_ = json.Unmarshal([]byte(payloadJSON), &p)
	session := p.Session
	if session == "" {
		session = p.SessionID
	}
	turn := p.Turn
	if turn == "" {
		turn = p.TurnID
	}
	if session == "" {
		log.Printf("[server] httpapi llm-start: session required (payload=%s)", payloadJSON)
		return
	}
	if turn == "" && !p.Continue {
		turn = "t-" + strings.ReplaceAll(newUUID(), "-", "")[:12]
	}
	content := p.Q
	if content == "" {
		content = p.Content
	}
	// 主题 session-start / session-send 为 server 域相对主题（**非 61 topic**，保留字面量）；
	// 载荷键走 msgkeys（req_id 为内部路由键，非契约字段，保留字面量）。
	s.publish("session-start", map[string]any{
		"req_id":                newUUID(),
		msgkeys.FieldInstanceId: s.instanceID,
		msgkeys.FieldSession:    session,
		msgkeys.FieldTurn:       turn,
		msgkeys.FieldLlm:        p.LLM,
		msgkeys.FieldThink:      p.Think,
		msgkeys.FieldEffort:     p.Effort,
		msgkeys.FieldScenarioId: p.ScenarioID,
		msgkeys.FieldContinue:   p.Continue,
	})
	s.publish("session-send", map[string]any{
		msgkeys.FieldInstanceId: s.instanceID,
		msgkeys.FieldSession:    session,
		msgkeys.FieldTurn:       turn,
		msgkeys.FieldType:       "text-user",
		msgkeys.FieldContent:    content,
	})
}

// filterCapabilityScope 按本实例作用域过滤能力面返回（scope 空 = 全局；保留当前实例条目）。
func (s *Server) filterCapabilityScope(typ string, result any) {
	key, ok := capabilityListKeys[typ]
	if !ok {
		return
	}
	m, ok := result.(map[string]any)
	if !ok {
		return
	}
	arr, ok := m[key].([]any)
	if !ok {
		return
	}
	filtered := make([]any, 0, len(arr))
	for _, it := range arr {
		entry, isMap := it.(map[string]any)
		if !isMap {
			filtered = append(filtered, it)
			continue
		}
		scope, _ := entry[msgkeys.FieldScope].(string)
		if scope == "" || scope == s.instanceID {
			filtered = append(filtered, it)
		}
	}
	m[key] = filtered
}

// ── data-* 数据面客户端（请求-响应；与桥 dataViaPersist 同语义）──

// dataCall 发一条 data-<domain>-<action> 请求：**门面已覆盖的域走 data 门面**（同进程直调，
// 见 facade_session.go / facade_domains.go），其余域与未注入门面时经总线（dataViaPersist，
// 行为同改前）。两条路径的应答载荷键名/形态逐字一致（同一份 `facade/wire` 翻译）。
func (s *Server) dataCall(subject, payloadJSON string) (any, []error) {
	if res, errs, ok := s.dataViaFacade(subject, payloadJSON); ok {
		return res, errs
	}
	return s.dataViaPersist(subject, []byte(payloadJSON))
}

// dataReqTimeout data-* 请求超时（persist 同进程应答为微秒级；给分离形态留裕量）。
const dataReqTimeout = 3 * time.Second

// dataViaPersist 发 data-<domain>-<op> 并等应答（请求注入 req_id + instance_id；
// 应答同主题、按 req_id 匹配；超时/失败 → {ok:false,error} 信封 + errs）。
func (s *Server) dataViaPersist(subject string, payload []byte) (result any, errs []error) {
	var req map[string]any
	if err := json.Unmarshal(payload, &req); err != nil || req == nil {
		req = map[string]any{}
	}
	reqID := newUUID()
	req["req_id"] = reqID
	if _, ok := req[msgkeys.FieldInstanceId]; !ok {
		req[msgkeys.FieldInstanceId] = s.instanceID
	}
	raw, _ := json.Marshal(req)

	type reply struct {
		result map[string]any
		err    error
	}
	done := make(chan reply, 1)
	sub, err := s.bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != reqID || m.OK == nil {
			return nil
		}
		if *m.OK {
			select {
			case done <- reply{result: m.Result}:
			default:
			}
			return nil
		}
		msg := m.Error
		if msg == "" {
			msg = subject + " failed"
		}
		select {
		case done <- reply{err: errors.New(msg)}:
		default:
		}
		return nil
	})
	if err != nil {
		e := fmt.Errorf("%s: subscribe: %w", subject, err)
		return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: e.Error()}, []error{e}
	}
	defer func() { _ = sub.Unsubscribe() }()

	// 防环：请求与应答同主题（persist 直发），标记自发布跳过 SSE 回投。
	s.markPublished(subject)
	_ = s.bus.Emit(context.Background(), subject, raw)
	select {
	case r := <-done:
		if r.err != nil {
			return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: r.err.Error()}, []error{r.err}
		}
		return r.result, nil
	case <-time.After(dataReqTimeout):
		e := fmt.Errorf("%s via persist timeout", subject)
		return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: e.Error()}, []error{e}
	}
}

// ── gui.* browser 本地面 ──

// guiDo 处理 gui.<action>：native 明确不支持；只读/UI 状态面在服务端进程内实现
// （工作目录 = 启动参数 --work-dir）。
func (s *Server) guiDo(action string, payload []byte) (any, []error) {
	if reason, bad := browserUnsupported[action]; bad {
		return unsupportedReply(action, reason)
	}
	switch action {
	case "init-data":
		m, err := s.initData()
		if err != nil {
			return failReply("init-data: %v", err)
		}
		return m, nil
	case "ui.save":
		return s.guiSaveUI(payload)
	case "recent.list":
		return map[string]any{"dirs": s.recentDirsList()}, nil
	case "recent.remove":
		return s.guiRemoveRecentDir(payload)
	case "vcs.info":
		return s.vcsInfo(), nil
	case "search":
		return map[string]any{"results": s.searchFiles(payload)}, nil
	default:
		return unsupportedReply(action, "browser 形态未实现该 gui 动作")
	}
}

// unsupportedReply 生成"明确不支持"回复（不得静默失败 / 不得假成功）。
func unsupportedReply(action, reason string) (any, []error) {
	e := fmt.Errorf("browser 形态不支持 gui.%s：%s（native 能力已禁用，见 19 §3.3/§6）", action, reason)
	return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: e.Error()}, []error{e}
}

// failReply 生成失败回复（附 error 进 errors）。
func failReply(format string, args ...any) (any, []error) {
	e := fmt.Errorf(format, args...)
	return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: e.Error()}, []error{e}
}

// initData 返回启动初始化数据（对齐 GUI gui.init-data 的字段形态）：
// {treeData, expandedKeys, selectedKey, workDir, filetreeWidth, layout, ui, openedFile, openedFiles}。
// 布局/UI/已开文件经总线 persist 读取；treeData 直接读 --work-dir 磁盘（只读）。
func (s *Server) initData() (map[string]any, error) {
	cfg := s.prjConfigList()

	layout := prefixedConfig(cfg, "layout.")
	if len(layout) == 0 {
		layout = legacyBlob(cfg, "layout")
	}
	ui := map[string]any{}
	if uc, err := s.readUserConfig(); err == nil {
		for _, k := range []string{"theme", "locale"} {
			if v, ok := uc[k]; ok {
				ui[k] = v
			}
		}
	}

	// 打开文件恢复：落库为 workdir 相对逻辑路径（G-24）→ 展开为绝对（兼容旧绝对路径）。
	openedFiles := []string{}
	if raw := sval(cfg["opened-files"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &openedFiles)
	}
	for i, p := range openedFiles {
		openedFiles[i] = paths.FromLogical(s.workDir, p)
	}
	kept := openedFiles[:0]
	for _, p := range openedFiles {
		if strings.HasPrefix(p, "db://") {
			kept = append(kept, p)
			continue
		}
		if _, err := os.Stat(p); err == nil {
			kept = append(kept, p)
		}
	}
	openedFiles = kept
	openedFile := ""
	if len(openedFiles) > 0 {
		openedFile = openedFiles[0]
	}

	expanded := []string{}
	if raw := sval(cfg["filetree-expanded-key"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &expanded)
	}
	selected := ""
	if rel := sval(cfg["filetree-selected-path"]); rel != "" {
		selected = filepath.Join(s.workDir, rel)
	}
	absExpanded := make([]string, 0, len(expanded))
	expandedSet := make(map[string]bool, len(expanded))
	for _, rel := range expanded {
		if rel == "" {
			continue
		}
		abs := filepath.ToSlash(filepath.Join(s.workDir, rel))
		absExpanded = append(absExpanded, abs)
		expandedSet[abs] = true
	}

	return map[string]any{
		msgkeys.GuiInitDataResultTreeData:      readDirNodesExpanded(s.workDir, expandedSet),
		msgkeys.GuiInitDataResultExpandedKeys:  absExpanded,
		msgkeys.GuiInitDataResultSelectedKey:   selected,
		msgkeys.GuiInitDataResultWorkDir:       s.workDir,
		msgkeys.GuiInitDataResultFiletreeWidth: 260,
		msgkeys.GuiInitDataResultLayout:        layout,
		msgkeys.GuiInitDataResultUi:            ui,
		msgkeys.GuiInitDataResultOpenedFile:    openedFile,
		msgkeys.GuiInitDataResultOpenedFiles:   openedFiles,
	}, nil
}

// guiSaveUI 处理 gui.ui.save（按 payload 中存在的字段逐项保存；与 GUI 同语义）：
// layout.* / window.* → prj config；filetree-* → prj config（相对路径）；opened-files → prj config；
// ui{theme,locale} → usr config（用户级偏好）。
func (s *Server) guiSaveUI(payload []byte) (any, []error) {
	var p map[string]json.RawMessage
	if err := json.Unmarshal(payload, &p); err != nil {
		return failReply("ui.save: %v", err)
	}
	var errList []error
	saveMap := func(prefix string, raw json.RawMessage) {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		for k, v := range m {
			if err := s.prjConfigSave(prefix+k, cfgScalar(v)); err != nil {
				errList = append(errList, err)
			}
		}
	}
	if raw, ok := p[msgkeys.GuiUiSavePayloadLayout]; ok && len(raw) > 0 && string(raw) != "null" {
		saveMap("layout.", raw)
	}
	if raw, ok := p[msgkeys.GuiUiSavePayloadWindow]; ok && len(raw) > 0 && string(raw) != "null" {
		saveMap("window.", raw)
	}
	if raw, ok := p[msgkeys.GuiUiSavePayloadFiletree]; ok && len(raw) > 0 {
		var m struct {
			ExpandedDirs []string `json:"expanded_dirs"`
			SelectedPath string   `json:"selected_path"`
		}
		if json.Unmarshal(raw, &m) == nil {
			if m.ExpandedDirs != nil {
				rel := make([]string, 0, len(m.ExpandedDirs))
				for _, d := range m.ExpandedDirs {
					if d == "" {
						continue
					}
					if r, err := filepath.Rel(s.workDir, d); err == nil {
						rel = append(rel, r)
					}
				}
				if relJSON, err := json.Marshal(rel); err == nil {
					if err := s.prjConfigSave("filetree-expanded-key", string(relJSON)); err != nil {
						errList = append(errList, err)
					}
				}
			}
			if m.SelectedPath != "" {
				if r, err := filepath.Rel(s.workDir, m.SelectedPath); err == nil {
					if err := s.prjConfigSave("filetree-selected-path", r); err != nil {
						errList = append(errList, err)
					}
				}
			}
		}
	}
	if raw, ok := p[msgkeys.GuiUiSavePayloadOpenedFiles]; ok && len(raw) > 0 {
		var files []string
		if json.Unmarshal(raw, &files) == nil {
			if files == nil {
				files = []string{}
			}
			// 落库为 workdir 相对逻辑路径（G-24）：换挂载布局 / 迁移后仍可还原。
			logical := make([]string, 0, len(files))
			for _, f := range files {
				logical = append(logical, paths.ToLogical(s.workDir, f))
			}
			if b, err := json.Marshal(logical); err == nil {
				if err := s.prjConfigSave("opened-files", string(b)); err != nil {
					errList = append(errList, err)
				}
			}
		}
	}
	if raw, ok := p[msgkeys.GuiUiSavePayloadUi]; ok && len(raw) > 0 {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			data := map[string]any{}
			for _, k := range []string{"theme", "locale"} {
				if v, ok := m[k]; ok {
					data[k] = v
				}
			}
			if len(data) > 0 {
				if b, err := json.Marshal(map[string]any{msgkeys.FieldData: data}); err == nil {
					if _, errs := s.dataCall(msgkeys.TopicDataUserConfigSave, string(b)); len(errs) > 0 {
						errList = append(errList, errs...)
					}
				}
			}
		}
	}
	if len(errList) > 0 {
		return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: errList[0].Error()}, errList
	}
	return map[string]any{msgkeys.FieldOk: true}, nil
}

// recentDirsList 读 usr config 自由键 recent_dirs（JSON 数组字符串）→ 最近目录快照；
// browser 形态不记录新目录（目录选择器已禁用）→ 通常为空表。
func (s *Server) recentDirsList() []string {
	uc, err := s.readUserConfig()
	if err != nil {
		return []string{}
	}
	raw, _ := uc["recent_dirs"].(string)
	if raw == "" {
		return []string{}
	}
	var dirs []string
	if json.Unmarshal([]byte(raw), &dirs) != nil {
		return []string{}
	}
	return dirs
}

// guiRemoveRecentDir 处理 gui.recent.remove（browser 形态）：从 usr config 自由键 recent_dirs
// 中移除一条记录（payload {path}）→ {ok}。**仅删记录** —— 不删除对应项目目录 / 数据资产；
// 记录不存在 → 幂等成功（不写库）。
func (s *Server) guiRemoveRecentDir(payload []byte) (any, []error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return failReply("recent.remove: %v", err)
	}
	if p.Path == "" {
		return failReply("recent.remove: path required")
	}
	dirs := s.recentDirsList()
	kept := make([]string, 0, len(dirs))
	removed := false
	for _, d := range dirs {
		if d == p.Path {
			removed = true
			continue
		}
		kept = append(kept, d)
	}
	if !removed {
		return map[string]any{msgkeys.FieldOk: true}, nil // 无该记录 → 幂等成功
	}
	raw, _ := json.Marshal(kept)
	body, _ := json.Marshal(map[string]any{msgkeys.FieldData: map[string]any{"recent_dirs": string(raw)}})
	if _, errs := s.dataCall(msgkeys.TopicDataUserConfigSave, string(body)); len(errs) > 0 {
		return failReply("recent.remove: %v", errs[0])
	}
	return map[string]any{msgkeys.FieldOk: true}, nil
}

// maxDirChoices / maxDirDepth 目录选择等价面的规模上限（防大目录拖垮响应/页面）。
const (
	maxDirChoices = 200
	maxDirDepth   = 3
)

// allowedDirs 列出该 instance **允许的目录**（work_dir 及其子目录；深度/规模受限，
// 跳过隐藏目录与 skipDirs 重型目录）。作为 native 目录选择器（gui.dir.open-dialog）的
// **服务端等价面**由非 MQ 的 `GET /dirs` 暴露（61 §1 消息面 result 不变）：浏览器端只能在
// 该清单内选择；work_dir 由服务端绑定，客户端不可改。
func (s *Server) allowedDirs() []map[string]any {
	out := make([]map[string]any, 0, 16)
	if strings.TrimSpace(s.workDir) == "" {
		return out
	}
	root := filepath.Clean(s.workDir)
	out = append(out, map[string]any{"path": filepath.ToSlash(root), "name": filepath.Base(root), "depth": 0})
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth >= maxDirDepth || len(out) >= maxDirChoices {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if len(out) >= maxDirChoices {
				return
			}
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || skipDirs[e.Name()] {
				continue
			}
			p := filepath.Join(dir, e.Name())
			out = append(out, map[string]any{"path": filepath.ToSlash(p), "name": e.Name(), "depth": depth + 1})
			walk(p, depth+1)
		}
	}
	walk(root, 0)
	return out
}

// vcsInfo 探测工作目录 VCS 类型 + git 可执行性 → {git, svn, gitInstalled}（与 GUI 同语义）。
func (s *Server) vcsInfo() map[string]any {
	git := false
	if _, err := os.Stat(filepath.Join(s.workDir, ".git")); err == nil {
		git = true
	}
	svn := false
	if _, err := os.Stat(filepath.Join(s.workDir, ".svn")); err == nil {
		svn = true
	}
	gitInstalled := false
	if _, err := exec.LookPath("git"); err == nil {
		gitInstalled = true
	}
	return map[string]any{
		msgkeys.GuiVcsInfoResultGit:          git,
		msgkeys.GuiVcsInfoResultSvn:          svn,
		msgkeys.GuiVcsInfoResultGitInstalled: gitInstalled,
	}
}

// ── 检索（browser 形态：仅文件名/路径源 file；vfts/codegraph 索引源未接线）──

// skipDirs 检索跳过的重型目录（同 GUI local.go 默认集）。
var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".svn": true, "dist": true,
	"build": true, "vendor": true, ".chonkpilot": true, "out": true,
	"target": true, ".venv": true, "__pycache__": true,
}

// searchMaxResults 检索结果上限（同 GUI）。
const searchMaxResults = 20

// searchFiles 项目内检索（payload {query}）→ [{path,name,matchType,source:"file",snippet:""}]。
// **覆盖面差异（如实标注）**：browser 形态只做**文件名/路径**匹配（索引源未接线）。
func (s *Server) searchFiles(payload []byte) []map[string]any {
	var p struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal(payload, &p)
	query := strings.TrimSpace(p.Query)
	if query == "" {
		return []map[string]any{}
	}
	lower := strings.ToLower(query)
	results := make([]map[string]any, 0, searchMaxResults)
	_ = filepath.Walk(s.workDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if skipDirs[fi.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(s.workDir, path)
		if !strings.Contains(strings.ToLower(filepath.ToSlash(rel)), lower) {
			return nil
		}
		mt := "path"
		if strings.Contains(strings.ToLower(fi.Name()), lower) {
			mt = "filename"
		}
		results = append(results, map[string]any{
			"path":      path,
			"name":      fi.Name(),
			"matchType": mt,
			"source":    "file",
			"snippet":   "",
		})
		if len(results) >= searchMaxResults {
			return filepath.SkipAll
		}
		return nil
	})
	sort.SliceStable(results, func(i, j int) bool {
		return searchRank(results[i]) < searchRank(results[j])
	})
	return results
}

// searchRank 结果优先级（文件名命中 0 优先，其后路径命中）。
func searchRank(it map[string]any) int {
	if mt, _ := it["matchType"].(string); mt == "filename" {
		return 0
	}
	return 1
}

// ── 配置读（经 data 门面；与桥 data.go 同语义）──

// prjConfigSave 保存 prj config 键（save 载荷 {data:{key,value}}）。
func (s *Server) prjConfigSave(key, value string) error {
	raw, _ := json.Marshal(map[string]any{msgkeys.FieldData: map[string]any{msgkeys.FieldKey: key, "value": value}})
	_, errs := s.dataCall(msgkeys.TopicDataPrjConfigSave, string(raw))
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// prjConfigList 一把取 prj config 表平铺 map（失败 → 空 map）。
func (s *Server) prjConfigList() map[string]any {
	res, errs := s.dataCall(msgkeys.TopicDataPrjConfigList, `{}`)
	if len(errs) > 0 {
		return map[string]any{}
	}
	m, ok := res.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	if list, ok := m[msgkeys.FieldList].(map[string]any); ok {
		return list
	}
	return map[string]any{}
}

// readUserConfig 读用户配置（data-user-config-load；无记录回落默认）。
func (s *Server) readUserConfig() (map[string]any, error) {
	res, errs := s.dataCall(msgkeys.TopicDataUserConfigLoad, `{}`)
	if len(errs) > 0 {
		return map[string]any{}, errs[0]
	}
	m, ok := res.(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	if cfg, ok := m[msgkeys.FieldData].(map[string]any); ok {
		return cfg, nil
	}
	return map[string]any{}, nil
}

// sval 值转字符串（string 原样；其余 fmt 兜底；nil → ""）。
func sval(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// cfgScalar 配置值转存储字符串（字符串原样；数字/bool 取字面量；其余 JSON 序列化）。
func cfgScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}

// prefixedConfig 取指定前缀条目并剥前缀（v6 细 key 还原为对象）。
func prefixedConfig(cfg map[string]any, prefix string) map[string]any {
	out := map[string]any{}
	for k, v := range cfg {
		if strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = sval(v)
		}
	}
	return out
}

// legacyBlob 解析 v6 之前的整块 JSON 键（细 key 缺失时回落；坏值 → 空 map）。
func legacyBlob(cfg map[string]any, key string) map[string]any {
	out := map[string]any{}
	if raw := sval(cfg[key]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// ── 文件树（只读磁盘）──

// readDirNodes 读目录一层节点（隐藏条目跳过；os.ReadDir 已按名称字母序）。
func readDirNodes(dir string) []map[string]any {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, map[string]any{
			"name":   e.Name(),
			"label":  e.Name(),
			"path":   filepath.ToSlash(filepath.Join(dir, e.Name())),
			"is_dir": e.IsDir(),
		})
	}
	return out
}

// readDirNodesExpanded 为命中 expandedSet 的目录递归预载 children（与 GUI 同语义）。
func readDirNodesExpanded(dir string, expandedSet map[string]bool) []map[string]any {
	nodes := readDirNodes(dir)
	for _, n := range nodes {
		isDir, _ := n["is_dir"].(bool)
		if !isDir {
			continue
		}
		p, _ := n["path"].(string)
		if p != "" && expandedSet[p] {
			n["children"] = readDirNodesExpanded(filepath.FromSlash(p), expandedSet)
		}
	}
	return nodes
}
