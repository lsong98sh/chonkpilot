// Package pluginmgr 是插件平台宿主侧：发现 / 校验 / 拉起 / 守护 manifest 插件，
// 并把插件的 MCP / UI / 状态接入宿主。
//
// 设计见 docs/plugin-platform/插件与MCP统一设计方案.md；
// 开发规范见 docs/plugin-platform/插件与MCP-Server开发指南.md。
package mgr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Manifest 是 plugin.json 的宿主侧视图。
type Manifest struct {
	Schema      string `json:"schema"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Publisher   string `json:"publisher"`
	Product     bool   `json:"product"`

	Runtime struct {
		Mode      string            `json:"mode"`
		Exe       string            `json:"exe"`
		Args      []string          `json:"args"`
		Env       map[string]string `json:"env"`
		Autostart *bool             `json:"autostart"`
		Restart   struct {
			BackoffMS    int `json:"backoff_ms"`
			MaxBackoffMS int `json:"max_backoff_ms"`
			MaxFails     int `json:"max_fails"`
		} `json:"restart"`
		Health struct {
			IntervalMS    int `json:"interval_ms"`
			TimeoutMS     int `json:"timeout_ms"`
			FailThreshold int `json:"fail_threshold"`
		} `json:"health"`
		Stop struct {
			GraceMS int `json:"grace_ms"`
		} `json:"stop"`
	} `json:"runtime"`

	Channel struct {
		Transport string `json:"transport"`
		Addr      string `json:"addr"`
	} `json:"channel"`

	MCP struct {
		Mode      string            `json:"mode"`
		Namespace string            `json:"namespace"`
		HotTools  []string          `json:"hot_tools"`
		Timeout   int               `json:"timeout_sec"`
		Category  string            `json:"category"`
		Origin    string            `json:"origin"`
		Tools     []json.RawMessage `json:"tools"`
	} `json:"mcp"`

	Config struct {
		Scope  string        `json:"scope"`
		Fields []ConfigField `json:"fields"`
	} `json:"config"`

	UI struct {
		Views     []View         `json:"views"`
		Menus     []Menu         `json:"menus"`
		Statusbar []BarItem      `json:"statusbar"`
		Actions   []ButtonAction `json:"actions"`
		RPC       []string       `json:"rpc"`
		// SelfServe = 插件自持 webui（PLUGIN-UI-1）：资源由插件经 `plugin/v1/ui.asset.get` 提供；
		// 前端路由已统一 `/plugins/<id>/…`（旧宿主直读磁盘前缀 `/plugin-ui/<id>/…` 已下线），
		// 本字段保留用于遥测 / 兼容（前端不再据此分流）。
		SelfServe bool `json:"selfServe"`
	} `json:"ui"`

	Permissions struct {
		Data    []string       `json:"data"`
		Hooks   []string       `json:"hooks"`
		LLM     map[string]any `json:"llm"`
		Session []string       `json:"session"`
		UI      map[string]any `json:"ui"`
		Network []string       `json:"network"`
	} `json:"permissions"`
}

// ConfigField 是配置字段声明。
type ConfigField struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Default  any    `json:"default"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	Min      *int   `json:"min"`
	Max      *int   `json:"max"`
	Label    string `json:"label"`
}

// View / Menu / BarItem 是 UI 声明。
type View struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Icon        string         `json:"icon"`
	Entry       string         `json:"entry"`
	Containers  []string       `json:"containers"`
	DefaultSize map[string]int `json:"default_size"`
}

type Menu struct {
	Location string `json:"location"`
	Label    string `json:"label"`
	Icon     string `json:"icon"`
	View     string `json:"view"`
}

type BarItem struct {
	ID      string         `json:"id"`
	Zone    string         `json:"zone"`
	Icon    string         `json:"icon"`
	Tooltip string         `json:"tooltip"`
	Command map[string]any `json:"command"`
}

// ButtonAction 是插件向宿主**注册的动作按钮**（插件维护视图里每张卡片上的按钮，
// 与布局栏按钮同形：名称 + 图标 + 命令 + 打开方式）。两种写法：
//
//  1. 便捷式：`view` + `container`（preview | tab | drawer | dialog）→ 按容器打开本插件已注册的
//     某个 `ui.views` 视图；缺省容器 = 该 view 的 `containers[0]`。
//  2. 命令式：`command`（与布局栏按钮同一命令对象，见 `layout-free/utils/commands.js`
//     `commandToMq`：open-dialog / open-tabs / open-drawer / toggle-drawer / publish-mq）
//     → 前端原样执行。
//
// `icon` 既可以是宿主图标名（`components/icon/icons.js` 的键），也可以是内联 `<svg>…</svg>`。
type ButtonAction struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Icon      string         `json:"icon"`
	Tooltip   string         `json:"tooltip"`
	Style     string         `json:"style"`     // icon | icon-text | text（缺省 icon-text）
	View      string         `json:"view"`      // 便捷式：目标 view id（须在 ui.views 注册）
	Container string         `json:"container"` // 便捷式：preview | tab | drawer | dialog
	Command   map[string]any `json:"command"`   // 命令式：布局命令对象（优先于 view）
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// loadManifest 读取并校验一个插件目录。
func loadManifest(root, dirID string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("plugin.json 解析失败: %w", err)
	}
	if m.Schema != "chonkpilot.plugin/v1" {
		return nil, fmt.Errorf("schema 必须是 chonkpilot.plugin/v1")
	}
	if !idPattern.MatchString(m.ID) {
		return nil, fmt.Errorf("id 非法（^[a-z][a-z0-9-]{1,31}$）")
	}
	if dirID != "" && m.ID != dirID {
		return nil, fmt.Errorf("id(%s) 必须等于目录名(%s)", m.ID, dirID)
	}
	if m.Runtime.Mode == "" {
		m.Runtime.Mode = "managed"
	}
	if m.Runtime.Mode != "managed" && m.Runtime.Mode != "external" {
		return nil, fmt.Errorf("runtime.mode 必须是 managed / external")
	}
	if m.Runtime.Mode == "managed" {
		if strings.TrimSpace(m.Runtime.Exe) == "" {
			return nil, fmt.Errorf("runtime.exe 必填")
		}
		if filepath.IsAbs(m.Runtime.Exe) || strings.Contains(m.Runtime.Exe, "..") {
			return nil, fmt.Errorf("runtime.exe 必须是插件目录内的相对路径")
		}
		// 不在此处 stat：exe 缺失由 Manager 拉起失败后置 status=error（UI 可见、可重试）。
	}
	if m.MCP.Mode == "" {
		m.MCP.Mode = "http"
	}
	switch m.MCP.Mode {
	case "http", "tools", "none":
	default:
		return nil, fmt.Errorf("mcp.mode 只允许 http / tools / none（不支持 %s）", m.MCP.Mode)
	}
	if m.MCP.Namespace == "" {
		m.MCP.Namespace = "-"
	}
	if m.MCP.Origin == "" {
		if m.Product {
			m.MCP.Origin = "builtin"
		} else {
			m.MCP.Origin = "user"
		}
	}
	if !m.Product && m.MCP.Origin == "builtin" {
		return nil, fmt.Errorf("product=false 时 origin 不能是 builtin")
	}
	for i := range m.UI.Views {
		v := &m.UI.Views[i]
		if v.ID == "" || v.Entry == "" {
			return nil, fmt.Errorf("ui.views[%d] 需要 id/entry", i)
		}
		if strings.Contains(v.Entry, "..") || !strings.HasPrefix(v.Entry, "webui/") {
			return nil, fmt.Errorf("ui.views[%d].entry 必须是 webui/ 下的相对路径", i)
		}
	}
	for _, f := range m.Config.Fields {
		if f.Key == "" {
			return nil, fmt.Errorf("config.fields 存在空 key")
		}
	}
	// 动作按钮（插件维护卡片）：id 必填且唯一；view 模式必须引用已注册视图；容器取值受限。
	// 两种模式二选一，都缺省 → 注册无效（前端无法执行）。
	viewIDs := map[string]bool{}
	for _, v := range m.UI.Views {
		viewIDs[v.ID] = true
	}
	actIDs := map[string]bool{}
	for i := range m.UI.Actions {
		a := &m.UI.Actions[i]
		if strings.TrimSpace(a.ID) == "" {
			return nil, fmt.Errorf("ui.actions[%d] 需要 id", i)
		}
		if actIDs[a.ID] {
			return nil, fmt.Errorf("ui.actions[%d].id 重复: %s", i, a.ID)
		}
		actIDs[a.ID] = true
		if strings.TrimSpace(a.Label) == "" {
			a.Label = a.ID
		}
		switch a.Style {
		case "":
			a.Style = "icon-text"
		case "icon", "icon-text", "text":
		default:
			return nil, fmt.Errorf("ui.actions[%d].style 只允许 icon / icon-text / text", i)
		}
		if a.View != "" && !viewIDs[a.View] {
			return nil, fmt.Errorf("ui.actions[%d].view(%s) 未在 ui.views 注册", i, a.View)
		}
		if a.View == "" && len(a.Command) == 0 {
			return nil, fmt.Errorf("ui.actions[%d] 需要 view 或 command", i)
		}
		switch a.Container {
		case "", "preview", "tab", "drawer", "dialog":
		default:
			return nil, fmt.Errorf("ui.actions[%d].container 只允许 preview / tab / drawer / dialog", i)
		}
	}
	return &m, nil
}

// configViewID 返回插件的**配置视图** id（约定 ui.views 中 id=config 的那一项）。
func (m *Manifest) configViewID() string {
	for _, v := range m.UI.Views {
		if v.ID == "config" {
			return v.ID
		}
	}
	return ""
}

// launchCommand 是插件维护卡片上展示的「启动命令」（managed = exe + args；external = 连接地址）。
func (m *Manifest) launchCommand() string {
	if m.Runtime.Mode == "external" {
		if addr := strings.TrimSpace(m.Channel.Addr); addr != "" {
			return "external · " + addr
		}
		return "external"
	}
	if len(m.Runtime.Args) == 0 {
		return m.Runtime.Exe
	}
	return m.Runtime.Exe + " " + strings.Join(m.Runtime.Args, " ")
}

// uiPayload 把 manifest 的 UI 段转成前端可直接消费的 JSON。
func (m *Manifest) uiPayload() json.RawMessage {
	if len(m.UI.Views) == 0 && len(m.UI.Menus) == 0 && len(m.UI.Statusbar) == 0 && len(m.UI.Actions) == 0 {
		return nil
	}
	b, _ := json.Marshal(map[string]any{
		"views":      m.UI.Views,
		"menus":      m.UI.Menus,
		"statusbar":  m.UI.Statusbar,
		"actions":    m.UI.Actions,
		"rpc":        m.UI.RPC,
		"self_serve": m.UI.SelfServe,
	})
	return b
}
