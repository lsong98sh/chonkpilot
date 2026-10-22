package gui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-llm/pluginbridge"
)

// guiEventActions 是「纯前端事件」动作白名单（设计稿 §6 第 5 批：页面 / 抽屉 / 主题语言场景切换）。
// 这些 type 后端无处理者（前端本地 `mq.on` 订阅）→ gui.apply 必须经 EmitFrontend 注入目标窗口，
// 否则 PublishEvent 会因不在方法面白名单而静默丢弃。
var guiEventActions = map[string]bool{
	"theme-select":            true, // {themeId}
	"lang-select":             true, // {lang}
	"scenario-open":           true, // {}
	"preview-tab-open":        true, // {kind,title?}
	"preview-tab-close-all":   true, // {}
	"file-open":               true, // {path, temporary?}
	"session-select":          true, // {session_id}
	"lf-layout-open-drawer":   true, // {drawerId, viewKey?, instanceKey?}
	"lf-layout-close-drawer":  true, // {drawerId}
	"lf-layout-toggle-drawer": true, // {drawerId}
}

// classifyGUIAction 把插件面的 action 分类为 backend / event；未知动作明确报错（不静默）。
//   - backend：`gui.*`（宿主本地 guiDo，如 gui.window.status / gui.ui.save）与 `data-*`（数据门面）
//   - event：纯前端事件（guiEventActions）
func classifyGUIAction(action string) (string, error) {
	if strings.HasPrefix(action, "gui.") || strings.HasPrefix(action, "data-") {
		return "backend", nil
	}
	if guiEventActions[action] {
		return "event", nil
	}
	return "", fmt.Errorf("未知 GUI action %q（支持：gui.* 本地动作 / data-* 数据面 / 前端事件 %s）",
		action, guiEventActionNames())
}

// guiEventActionNames 返回前端事件动作清单（稳定序，错误消息 / 文档口径）。
func guiEventActionNames() string {
	names := make([]string, 0, len(guiEventActions))
	for n := range guiEventActions {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, " / ")
}

// pluginGUIController 把插件桥的 GUI 反向调用接到本进程窗口桥。
type pluginGUIController struct{ e *Env }

// NewPluginGUIController 构造 GUI 反向通道（供 pluginbridge.Options.GUI 注入）。
func NewPluginGUIController(e *Env) pluginbridge.GUIController {
	return &pluginGUIController{e: e}
}

// List 返回当前窗口实例 id。
func (c *pluginGUIController) List() []string {
	if c == nil || c.e == nil {
		return nil
	}
	return c.e.windows.instances()
}

// Apply 在目标窗口执行一条 GUI 命令（与前端 PublishEvent 同一白名单/校验）。
func (c *pluginGUIController) Apply(instanceID, action string, payload json.RawMessage) (any, error) {
	if c == nil || c.e == nil {
		return nil, fmt.Errorf("GUI 通道未装配")
	}
	h := c.e.windows.findByInstance(instanceID)
	if h == nil {
		return nil, fmt.Errorf("实例 %q 不存在或已关闭", instanceID)
	}
	body := string(payload)
	if body == "" {
		body = "{}"
	}
	mode, err := classifyGUIAction(action)
	if err != nil {
		return nil, err
	}
	// 窗口面（gui.window.status / open-chat / list / set-title）：需要本窗口 hwnd / 窗口注册表，
	// 与前端 /publish 的 appHandler 特判同一条链路（Bridge.PublishEvent 不处理这些动作）。
	if action == "gui.window.status" {
		res, errs := applyWindowCommand(h.hwnd, h.destroy, h.br, body)
		if len(errs) > 0 {
			return nil, errs[0]
		}
		return res, nil
	}
	if isWindowMessage(action) {
		res, errs := h.handler.handleWindowMessage(action, body)
		if len(errs) > 0 {
			return nil, errs[0]
		}
		return res, nil
	}
	if mode == "event" {
		// 纯前端事件：注入目标窗口（无返回值；送达即 ack）。
		h.br.EmitFrontend(action, body)
		return map[string]any{"ok": true, "delivered": "event"}, nil
	}
	res, errs := h.br.PublishEvent(action, body)
	if len(errs) > 0 {
		return nil, errs[0]
	}
	return res, nil
}

// InstanceIDs 返回本进程窗口实例 id（pluginbridge 的 ExistingInstanceIDs 注入）。
func (e *Env) InstanceIDs() []string {
	if e == nil {
		return nil
	}
	return e.windows.instances()
}
