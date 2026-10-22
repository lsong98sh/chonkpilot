//go:build windows

package gui

// 对话窗口注册表（24 §4.3 / MW-4·MW-6 最小面）：window_id ↔ hwnd/session_id 一对一。
// **只登记对话窗口**；主窗口不入表（主窗口"当前活动会话"由主窗口前端自知，故宿主不追踪，
// 见 24 §4.3）。职责：open-chat 幂等（已开 → 激活）/ 上限（≤5）/ 列表 / 关闭广播。
//
// 🆕 **窗口额度分两类**（群聊方案 §9 D28/D30 · N13/N17；SUBCHAT-3 阶段 G G3）：**群聊窗口
// （`sessions.kind == group_chat`）单独计上限 5**，与普通对话窗口（≤5，24 §8 U-5）**互不联动**。
// 目标会话是不是群聊由**宿主侧**判定（`open-chat` 载荷不变 → 不能由前端传）：建窗前经既有
// **只读**消息 `data-session-get` 取 `kind`（`Bridge.SessionKind`）；**取不到 → 按普通会话处理**
// → 普通聊天窗口的既有额度与行为**逐字不变**。

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// chatCloseWait 是进程收尾时等待单个对话窗口自行关闭的上限（超时即放弃等待，进程随即退出）。
const chatCloseWait = 2 * time.Second

// groupChatKind 是群聊会话类型标识（`sessions.kind`，群聊方案 D25；与 data 层常量同值）。
const groupChatKind = "group_chat"

// windowRef 是 gui.window.list 的窗口条目（61 §1：{window_id, session_id}）。
type windowRef struct {
	WindowID  string `json:"window_id"`
	SessionID string `json:"session_id"`
}

// windowRegistry 是进程级对话窗口注册表（mw 多窗口）。
type windowRegistry struct {
	mu        sync.Mutex
	byID      map[string]*windowHost // window_id → 窗口
	bySession map[string]string      // session_id → window_id（占用判定 / 幂等激活）
	// groupSessions = session_id → 是否群聊窗口（额度分类计数依据；与 bySession 同生命周期，
	// 含**占位中**（建窗未完成）的会话 → 并发 open 也不会越过各自类别的上限）。
	groupSessions map[string]bool
	seq           int
	main          *windowHost // 主窗口（广播 gui.window.closed / 定位新窗口用；不入 byID）
}

func newWindowRegistry() *windowRegistry {
	return &windowRegistry{
		byID:          map[string]*windowHost{},
		bySession:     map[string]string{},
		groupSessions: map[string]bool{},
	}
}

// limitFor 返回该类别窗口的数量上限（群聊独立额度，群聊方案 §9 D28/D30；普通窗口同 24 §8 U-5）。
func limitFor(group bool) int {
	if group {
		return maxGroupChatWindows
	}
	return maxChatWindows
}

// countLocked 统计该类别**已占位**的对话窗口数（含建窗进行中；调用方须持有 r.mu）。
// 口径与改前一致（改前 = `len(r.bySession)`）；无群聊窗口时普通类别计数 == `len(r.bySession)`
// → 普通聊天窗口的既有额度与行为不变。
func (r *windowRegistry) countLocked(group bool) int {
	n := 0
	for sid := range r.bySession {
		if r.groupSessions[sid] == group {
			n++
		}
	}
	return n
}

// atLimitLocked 判定该类别是否已达上限（调用方须持有 r.mu）。
func (r *windowRegistry) atLimitLocked(group bool) bool {
	return r.countLocked(group) >= limitFor(group)
}

// setMain 登记主窗口（Main 建完主窗口后调用一次）。
func (r *windowRegistry) setMain(h *windowHost) {
	r.mu.Lock()
	r.main = h
	r.mu.Unlock()
}

// findByInstance 按实例 id 查窗口（空 id = 主窗口；找不到 = nil）。
func (r *windowRegistry) findByInstance(instanceID string) *windowHost {
	r.mu.Lock()
	defer r.mu.Unlock()
	if instanceID == "" {
		return r.main
	}
	if r.main != nil && r.main.instID == instanceID {
		return r.main
	}
	for _, h := range r.byID {
		if h.instID == instanceID {
			return h
		}
	}
	return nil
}

// instances 返回当前全部窗口实例 id（去重；主窗口在前）。
func (r *windowRegistry) instances() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]struct{}, len(r.byID)+1)
	out := make([]string, 0, len(r.byID)+1)
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if r.main != nil {
		add(r.main.instID)
	}
	for _, h := range r.byID {
		add(h.instID)
	}
	return out
}

// bringMainToTop 恢复并前置主窗口（flash 远程薄壳：`instance-goto-top` 控制帧处理，
// 由 UDS 读循环 goroutine 调用 → 跨线程，ShowWindow/SetForegroundWindow 幂等安全）。
// 主窗口尚未创建 → 空操作。
func (r *windowRegistry) bringMainToTop() {
	r.mu.Lock()
	h := r.main
	r.mu.Unlock()
	if h != nil {
		activateWindow(h.hwnd)
	}
}

// destroyMain 关闭主窗口（flash 远程薄壳：UDS 连接断开 = 实例退出 → 进程退出）。
// destroy 投 WM_CLOSE 给主窗口线程，跨线程安全；主窗口未创建 → 空操作。
func (r *windowRegistry) destroyMain() {
	r.mu.Lock()
	h := r.main
	r.mu.Unlock()
	if h != nil {
		h.destroy()
	}
}

// open 打开/激活一个纯对话窗口（gui.window.open-chat，61 §1；幂等）：
//   - 该 session 已有对话窗口 → **激活**（ShowWindow(SW_RESTORE)+SetForegroundWindow），不新建；
//   - 未开且未达上限（maxChatWindows）→ 新建对话窗口并绑定该 session；
//   - session 为空 / 已达上限 / 建窗失败 → ok=false（不新增字段，靠 ok 判定）。
//
// parentInstance = **调用来源窗口的实例 id**（"谁开的指谁"，逐层链；空 = 不明来源）。
// 新窗口实例据此把 `instance-register.parent_instance` 上报（61 §4.1 ④ · 单聊 §12.4 B4 /
// 群聊 §14.4 N18–N20），server 实例视图据此支持跨窗口定向投递。
//
// 返回 (window_id, activated, ok)。
//
// 同步建窗说明（D-16，**已评估保留**）：本函数在调用窗口的 WebView2 UI 线程上同步执行
// startWindow（内部虽在独占线程建窗，但会阻塞调用线程至建窗完成，稳态约 0.7~1.4s）。
// 之所以不改为「异步建窗 + 完成后回推」，是因为 61 §1 gui.window.open-chat 为**同步
// 请求-响应**契约（返回 {ok, window_id, activated}），且**不存在**异步完成事件；
// 改异步即需新增消息面（新增 msg/payload，须经用户确认——测试准则明令禁止擅改），
// 同时会使 --test-port 多窗口路由（window_id 定向）失去确定性，回归风险高。
// 故保持同步语义并在此明确声明取舍（若后续引入异步完成事件，可据此改造）。
func (r *windowRegistry) open(env *hostEnv, proj *projectCtx, br *bridge.Bridge, sessionID, parentInstance string) (string, bool, bool) {
	if sessionID == "" {
		return "", false, false
	}
	// 项目级字段（PRODSIM-1）：proj 优先；nil → 沿用 hostEnv 单项目字段（flash / --no-server）。
	workDir := env.workDir
	if proj != nil {
		workDir = proj.workDir
	}
	// 目标会话是不是群聊（`kind=group_chat`）：经既有只读 `data-session-get` 取（`open-chat` 载荷
	// 不变）；取不到 → 按普通会话处理（既有行为不变）。群聊窗口走**独立额度**（<maxGroupChatWindows）。
	group := br != nil && br.SessionKind(sessionID) == groupChatKind
	r.mu.Lock()
	if id, ok := r.bySession[sessionID]; ok {
		h := r.byID[id]
		r.mu.Unlock()
		if h != nil {
			activateWindow(h.hwnd)
		}
		return id, true, true
	}
	if r.atLimitLocked(group) {
		r.mu.Unlock()
		return "", false, false
	}
	r.seq++
	windowID := "w" + strconv.Itoa(r.seq)
	r.bySession[sessionID] = windowID // **先占位**：并发 open 也不会越过上限
	r.groupSessions[sessionID] = group
	idx := len(r.bySession) - 1
	r.mu.Unlock()

	h, err := startWindow(env, windowSpec{
		role:      roleChat,
		proj:      proj,
		windowID:  windowID,
		sessionID: sessionID,
		title:     chatWindowTitle(workDir),
		// owner 暂不启用（PRODSIM-1）：owned window 不出现在任务栏；从属关系待真机验收。
		// 父子链（"谁开的指谁"，逐层链；61 §4.1 ④）：本窗口实例的直接父 = 调用来源窗口实例。
		parentInstance: parentInstance,
		// URL 路由承载视图与绑定会话（24 §1.1 / §6.1）：宿主零视图判断，前端按 URL 分派。
		url:       appOrigin + "/?session-id=" + url.QueryEscape(sessionID) + "#chat",
		frameless: false, // 对话窗口 = 原生标题栏（最大化/最小化/resize 由系统提供）
		// --test-port 多窗口路由（24 §4.5）：本窗口登记为可选测试目标（window_id 定向）。
		onWired: func(h *windowHost) error {
			if s := env.testServer(); s != nil {
				s.RegisterChat(h)
				// 测试模式注入 console 捕获（导航前；与主窗口 onWired 同款，D-24）——
				// 缺失时 /console?window_id=<chat> 恒为空。
				s.InjectConsoleCapture(h.chromium)
			}
			return nil
		},
	})
	if err != nil {
		r.mu.Lock()
		delete(r.bySession, sessionID)
		delete(r.groupSessions, sessionID)
		r.mu.Unlock()
		return "", false, false
	}
	r.mu.Lock()
	// 竞态防护（D-09）：建窗耗时内该窗口可能已自行关闭（finish→remove 先跑），此时
	// remove 已把 bySession[sessionID] 清掉（或不再指向本 windowID）。若不核对即写 byID，
	// 会残留「幽灵」条目（byID 有、窗口已亡）→ 列表/广播指向已销毁窗口。故以 bySession
	// 一致性为准，被移除则不写入并回报失败。
	if r.bySession[sessionID] != windowID {
		r.mu.Unlock()
		// 幽灵分支：本窗口未被登记（会话占位已被 remove 清掉，或被并发 open 改指另一窗口）。
		// 此时窗口既未入 byID、也无任何引用 → 必须**主动销毁回滚**，否则其 WebView2 线程/profile
		// 泄漏，且同 session 可被重复开窗（状态不一致）。不在此处改 bySession —— 它可能已指向
		// 合法的新窗口，清掉会误伤。
		h.destroy()
		return windowID, false, false
	}
	r.byID[windowID] = h
	main := r.main
	r.mu.Unlock()
	placeChatWindow(main, h, idx)
	return windowID, false, true
}

// list 返回已开**对话窗口**（gui.window.list；只含 chat，61 §1）。
func (r *windowRegistry) list() []windowRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]windowRef, 0, len(r.byID))
	for id, h := range r.byID {
		if h != nil {
			out = append(out, windowRef{WindowID: id, SessionID: h.sessionID})
		}
	}
	return out
}

// remove 对话窗口关闭 → 双向移除（解除 session 占用）+ 广播 gui.window.closed
// （61 §1：宿主 → 前端；用于解除前端「已打开」置灰）。
func (r *windowRegistry) remove(h *windowHost) {
	r.mu.Lock()
	delete(r.byID, h.windowID)
	if r.bySession[h.sessionID] == h.windowID {
		delete(r.bySession, h.sessionID)
		delete(r.groupSessions, h.sessionID)
	}
	r.mu.Unlock()
	r.broadcast(h.windowID, msgkeys.TopicGuiWindowClosed, map[string]any{
		msgkeys.GuiWindowClosedEventWindowId:  h.windowID,
		msgkeys.GuiWindowClosedEventSessionId: h.sessionID,
	})
}

// broadcast 把宿主下行事件发给除 exceptWindowID 外的所有窗口（各窗口自己的桥 → 自己的前端）。
// 非 bus 派发的**跨层直调入口** → 加 panic 兜底：单次 panic 不得崩掉宿主进程（各窗口 Emit 失败
// 只应影响该窗口）。
func (r *windowRegistry) broadcast(exceptWindowID, typ string, payload map[string]any) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("windowRegistry.broadcast panic", "type", typ, "err", rec)
		}
	}()
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.mu.Lock()
	hosts := make([]*windowHost, 0, len(r.byID)+1)
	if r.main != nil {
		hosts = append(hosts, r.main)
	}
	for _, h := range r.byID {
		if h != nil {
			hosts = append(hosts, h)
		}
	}
	r.mu.Unlock()
	for _, h := range hosts {
		if h.windowID == exceptWindowID {
			continue
		}
		h.br.EmitFrontend(typ, string(raw))
	}
}

// shutdown 关闭全部对话窗口（主窗口关闭 = 退出本进程，24 §4.4）：逐个投 WM_CLOSE 并等待
// 其自行收尾（各自发 instance-exit + gui.window.closed），超时即放弃等待（进程随即退出）。
func (r *windowRegistry) shutdown() {
	r.mu.Lock()
	hosts := make([]*windowHost, 0, len(r.byID))
	for _, h := range r.byID {
		if h != nil {
			hosts = append(hosts, h)
		}
	}
	r.mu.Unlock()
	for _, h := range hosts {
		h.destroy()
	}
	for _, h := range hosts {
		select {
		case <-h.done:
		case <-time.After(chatCloseWait):
			slog.Warn("chat window close timeout", "window_id", h.windowID)
		}
	}
}

// MainWindowTitle 返回主窗口固定标题（用户裁决 2026-09-26：始终 `chonkpilot-<工作目录名>`，
// **不随会话/摘要变化**；与 chatWindowTitle 同取 workDir 末级目录名）。
func MainWindowTitle(workDir string) string {
	return "chonkpilot-" + filepath.Base(workDir)
}

// chatWindowTitle 返回对话窗口缺省标题（24 §6.3 C7：无摘要 = 「新会话 + <workdir 目录名>」；
// 摘要就绪后由前端经 gui.window.set-title 更新）。
func chatWindowTitle(workDir string) string {
	return "新会话 " + filepath.Base(workDir)
}

// isWindowMessage 判定是否为「窗口面」消息：由 appHandler 宿主侧处理（与 gui.window.status
// 同族，见 61 §1 / 24 §4.2），不进入桥。
func isWindowMessage(typ string) bool {
	switch typ {
	case msgkeys.TopicGuiWindowOpenChat, msgkeys.TopicGuiWindowList, msgkeys.TopicGuiWindowSetTitle:
		return true
	}
	return false
}

// handleWindowMessage 处理窗口面消息（MW-6）：open-chat / list / set-title。
// **执行对象 = 调用来源窗口**（本 appHandler 所属窗口，与 gui.window.status 同口径）。
func (h *appHandler) handleWindowMessage(typ, payloadJSON string) (any, []error) {
	switch typ {
	case msgkeys.TopicGuiWindowOpenChat:
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal([]byte(payloadJSON), &p)
		// 新窗口的直接父实例 = **本 handler 所属窗口的实例**（"谁开的指谁"，逐层链）。
		// 本窗口的桥一并传入：建窗前经既有只读 `data-session-get` 判目标会话是否群聊
		// （群聊窗口**独立额度**，群聊 §9 D28/D30；普通窗口额度与行为不变）。
		windowID, activated, ok := h.windows().open(h.env, h.proj, h.br, p.SessionID, h.br.InstanceID())
		return map[string]any{
			msgkeys.GuiWindowOpenChatResultOk:        ok,
			msgkeys.GuiWindowOpenChatResultWindowId:  windowID,
			msgkeys.GuiWindowOpenChatResultActivated: activated,
		}, nil
	case msgkeys.TopicGuiWindowList:
		return map[string]any{msgkeys.GuiWindowListResultWindows: h.windows().list()}, nil
	case msgkeys.TopicGuiWindowSetTitle:
		// 仅独立对话窗口可改标题（主窗口 handler 未接线 setTitle → ok=false，标题不变，61 §1）。
		if h.setTitle == nil {
			return map[string]any{msgkeys.GuiWindowSetTitleResultOk: false}, nil
		}
		var p struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal([]byte(payloadJSON), &p)
		h.setTitle(p.Title)
		return map[string]any{msgkeys.GuiWindowSetTitleResultOk: true}, nil
	}
	return nil, nil
}
