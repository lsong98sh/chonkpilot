//go:build windows

package gui

// 对话窗口注册表（24 §4.3 / MW-4·MW-6 最小面）：window_id ↔ hwnd/session_id 一对一。
// **只登记对话窗口**；主窗口不入表（主窗口"当前活动会话"由主窗口前端自知，故宿主不追踪，
// 见 24 §4.3）。职责：open-chat 幂等（已开 → 激活）/ 上限（≤5）/ 列表 / 关闭广播。

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// chatCloseWait 是进程收尾时等待单个对话窗口自行关闭的上限（超时即放弃等待，进程随即退出）。
const chatCloseWait = 2 * time.Second

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
	seq       int
	main      *windowHost // 主窗口（广播 gui.window.closed / 定位新窗口用；不入 byID）
}

func newWindowRegistry() *windowRegistry {
	return &windowRegistry{byID: map[string]*windowHost{}, bySession: map[string]string{}}
}

// setMain 登记主窗口（Main 建完主窗口后调用一次）。
func (r *windowRegistry) setMain(h *windowHost) {
	r.mu.Lock()
	r.main = h
	r.mu.Unlock()
}

// open 打开/激活一个纯对话窗口（gui.window.open-chat，61 §1；幂等）：
//   - 该 session 已有对话窗口 → **激活**（ShowWindow(SW_RESTORE)+SetForegroundWindow），不新建；
//   - 未开且未达上限（maxChatWindows）→ 新建对话窗口并绑定该 session；
//   - session 为空 / 已达上限 / 建窗失败 → ok=false（不新增字段，靠 ok 判定）。
//
// 返回 (window_id, activated, ok)。
func (r *windowRegistry) open(env *hostEnv, sessionID string) (string, bool, bool) {
	if sessionID == "" {
		return "", false, false
	}
	r.mu.Lock()
	if id, ok := r.bySession[sessionID]; ok {
		h := r.byID[id]
		r.mu.Unlock()
		if h != nil {
			activateWindow(h.hwnd)
		}
		return id, true, true
	}
	if len(r.bySession) >= maxChatWindows {
		r.mu.Unlock()
		return "", false, false
	}
	r.seq++
	windowID := "w" + strconv.Itoa(r.seq)
	r.bySession[sessionID] = windowID // **先占位**：并发 open 也不会越过上限
	idx := len(r.bySession) - 1
	r.mu.Unlock()

	h, err := startWindow(env, windowSpec{
		role:      roleChat,
		windowID:  windowID,
		sessionID: sessionID,
		title:     chatWindowTitle(env.workDir),
		// URL 路由承载视图与绑定会话（24 §1.1 / §6.1）：宿主零视图判断，前端按 URL 分派。
		url:       appOrigin + "/?session-id=" + url.QueryEscape(sessionID) + "#chat",
		frameless: false, // 对话窗口 = 原生标题栏（最大化/最小化/resize 由系统提供）
		// --test-port 多窗口路由（24 §4.5）：本窗口登记为可选测试目标（window_id 定向）。
		onWired: func(h *windowHost) error {
			env.testSrv.RegisterChat(h)
			return nil
		},
	})
	if err != nil {
		r.mu.Lock()
		delete(r.bySession, sessionID)
		r.mu.Unlock()
		return "", false, false
	}
	r.mu.Lock()
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
	}
	r.mu.Unlock()
	r.broadcast(h.windowID, msgkeys.TopicGuiWindowClosed, map[string]any{
		msgkeys.GuiWindowClosedEventWindowId:  h.windowID,
		msgkeys.GuiWindowClosedEventSessionId: h.sessionID,
	})
}

// broadcast 把宿主下行事件发给除 exceptWindowID 外的所有窗口（各窗口自己的桥 → 自己的前端）。
func (r *windowRegistry) broadcast(exceptWindowID, typ string, payload map[string]any) {
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

// mainWindowTitle 返回主窗口固定标题（用户裁决 2026-09-26：始终 `chonkpilot-<工作目录名>`，
// **不随会话/摘要变化**；与 chatWindowTitle 同取 workDir 末级目录名）。
func mainWindowTitle(workDir string) string {
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
		windowID, activated, ok := h.env.windows.open(h.env, p.SessionID)
		return map[string]any{
			msgkeys.GuiWindowOpenChatResultOk:        ok,
			msgkeys.GuiWindowOpenChatResultWindowId:  windowID,
			msgkeys.GuiWindowOpenChatResultActivated: activated,
		}, nil
	case msgkeys.TopicGuiWindowList:
		return map[string]any{msgkeys.GuiWindowListResultWindows: h.env.windows.list()}, nil
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
