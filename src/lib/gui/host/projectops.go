//go:build windows

package host

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	gui "github.com/lsong98sh/chonkpilot-framework/host/gui"
	"github.com/chonkpilot/chonkpilot-workspace"
)

// hostProjectOps 是 PM（`workspace.Manager`）**动作端口**的 GUI 装配实现（P5 步骤 2 / C15）。
//
// 边界（§7.4）：PM 不持有窗口句柄 —— 本类型只把 PM 的薄门面调用转给**既有**宿主动作：
//   - 窗口类 = 框架 [gui.Env] 导出的薄动作（`OpenProject / HideProject / CloseProject /
//     OpenChatWindow`，与托盘 / `gui.window.open-chat` / 插件桥 `workdir.ensure` 同一实现）；
//   - 会话类 = 项目主窗口的桥，走**既有** `data-session-ensure-session`（零新增消息面）。
//
// 不新增第二套开窗 / 建会话逻辑。
type hostProjectOps struct {
	env projectWindowOps
	// open = 已打开则激活、未打开则建窗（hidden = 隐藏打开，C13/C17）。
	open func(dir string, hidden bool) error
	// instanceID 按 work_dir 反查主窗口实例（打开后尽力而为；未登记 → 空串）。
	instanceID func(workDir string) (string, bool)
	// newID 生成会话 id（测试可注入确定性实现；nil = 随机）。
	newID func() string
}

// projectWindowOps 是 hostProjectOps 依赖的**窗口薄动作面**（`*gui.Env` 实现它；
// 抽接口只为可测 —— 生产装配仍直接传 `env`）。
type projectWindowOps interface {
	HideProject(workDir string) bool
	CloseProject(workDir string) bool
	OpenChatWindow(workDir, sessionID, dialogID, parentInstance string) (string, bool, bool)
	ProjectBridge(workDir string) gui.Bridge
}

// OpenProject 打开（或激活）项目主窗口。
func (p hostProjectOps) OpenProject(workDir string, hidden bool) (string, bool, error) {
	if err := p.open(workDir, hidden); err != nil {
		return "", false, err
	}
	if p.instanceID == nil {
		return "", false, nil
	}
	id, _ := p.instanceID(workDir)
	return id, false, nil
}

// CreateWindow 在项目下建窗口：
//   - main → 复用 OpenProject（visible=false 即隐藏打开）；
//   - chat → 复用 `gui.window.open-chat` 同一实现（需 params["session_id"]；
//     可选 params["dialog_id"]，G-105：带则窗口直连该 dialog，免前端反查）。
func (p hostProjectOps) CreateWindow(workDir, kind string, visible bool, params map[string]any) (string, error) {
	switch kind {
	case workspace.WindowMain:
		return p.CreateWindowID(workDir, visible)
	case workspace.WindowChat:
		sid, _ := params["session_id"].(string)
		if sid == "" {
			return "", errors.New("workspace: 对话窗口需要 params[session_id]")
		}
		did, _ := params["dialog_id"].(string)
		parent, _ := params["parent_instance"].(string)
		wid, _, ok := p.env.OpenChatWindow(workDir, sid, did, parent)
		if !ok {
			return "", fmt.Errorf("workspace: 打开对话窗口失败（work_dir=%s session=%s）", workDir, sid)
		}
		return wid, nil
	default:
		return "", fmt.Errorf("workspace: 未知窗口种类 %q", kind)
	}
}

// CreateWindowID 是 main 窗口分支（打开/激活后回带主实例 id）。
func (p hostProjectOps) CreateWindowID(workDir string, visible bool) (string, error) {
	id, _, err := p.OpenProject(workDir, !visible)
	return id, err
}

// Hide 隐藏项目主窗口（不关闭实例）。
func (p hostProjectOps) Hide(workDir string) error {
	if !p.env.HideProject(workDir) {
		return fmt.Errorf("workspace: 项目未打开（%s）", workDir)
	}
	return nil
}

// Close 关闭项目主窗口（连带关闭本项目全部对话窗口 → 实例退出 / workdir-closed）。
func (p hostProjectOps) Close(workDir string) error {
	if !p.env.CloseProject(workDir) {
		return fmt.Errorf("workspace: 项目未打开（%s）", workDir)
	}
	return nil
}

// CreateDialog 在项目下新建对话：建一个新**根会话**，由数据层自举出 dialog 并回带 dialog_id。
// 可选 params：title / kind / origin / parent_session_id。
func (p hostProjectOps) CreateDialog(workDir string, params map[string]any) (string, error) {
	br := p.env.ProjectBridge(workDir)
	if br == nil {
		return "", fmt.Errorf("workspace: 项目未打开（%s）", workDir)
	}
	sid := p.sessionID(params)
	if err := p.ensureSession(br, sid, "", params); err != nil {
		return "", err
	}
	did := br.SessionDialogID(sid)
	if did == "" {
		return "", errors.New("workspace: 对话未建立（data-session-ensure-session 未回带 dialog_id）")
	}
	return did, nil
}

// CreateSession 在对话下新建会话。params 必须含 `work_dir`（定位项目桥），
// 可选 kind / title / origin / parent_session_id。
func (p hostProjectOps) CreateSession(dialogID string, params map[string]any) (string, error) {
	workDir, _ := params["work_dir"].(string)
	if workDir == "" {
		return "", errors.New("workspace: 建会话需要 params[work_dir]")
	}
	br := p.env.ProjectBridge(workDir)
	if br == nil {
		return "", fmt.Errorf("workspace: 项目未打开（%s）", workDir)
	}
	sid := p.sessionID(params)
	if err := p.ensureSession(br, sid, dialogID, params); err != nil {
		return "", err
	}
	return sid, nil
}

// ensureSession 走既有消息面 `data-session-ensure-session`（幂等；dialog_id 空 = 根会话自举）。
func (p hostProjectOps) ensureSession(br gui.Bridge, sessionID, dialogID string, params map[string]any) error {
	b, err := ensureSessionPayload(sessionID, dialogID, params)
	if err != nil {
		return err
	}
	if _, errs := br.PublishEvent("data-session-ensure-session", string(b)); len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// ensureSessionPayload 组装 `data-session-ensure-session` 载荷（既有消息面：session_id /
// dialog_id / parent_session_id / kind / title / origin；空值不落键）。
func ensureSessionPayload(sessionID, dialogID string, params map[string]any) ([]byte, error) {
	payload := map[string]any{"session_id": sessionID}
	if dialogID != "" {
		payload["dialog_id"] = dialogID
	}
	for _, k := range []string{"parent_session_id", "kind", "title", "origin"} {
		if v, ok := params[k]; ok && v != nil && v != "" {
			payload[k] = v
		}
	}
	return json.Marshal(payload)
}

// sessionID 取调用方指定的 session_id，否则生成 `s-<16hex>`。
func (p hostProjectOps) sessionID(params map[string]any) string {
	if sid, _ := params["session_id"].(string); sid != "" {
		return sid
	}
	if p.newID != nil {
		return p.newID()
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "s-" + fmt.Sprint(len(params)) + "-fallback"
	}
	return "s-" + hex.EncodeToString(b[:])
}
