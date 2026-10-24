package pluginsdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Manifest 是插件 manifest（capability/plugins/<id>/plugin.json）的客户端只读视图。
type Manifest struct {
	Schema      string `json:"schema"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Product     bool   `json:"product"`

	Runtime struct {
		Mode      string            `json:"mode"`
		Exe       string            `json:"exe"`
		Args      []string          `json:"args"`
		Env       map[string]string `json:"env"`
		Autostart *bool             `json:"autostart"`
	} `json:"runtime"`

	MCP struct {
		Mode      string         `json:"mode"`
		Namespace string         `json:"namespace"`
		HotTools  []string       `json:"hot_tools"`
		Timeout   int            `json:"timeout_sec"`
		Category  string         `json:"category"`
		Origin    string         `json:"origin"`
		Tools     []ManifestTool `json:"tools"`
	} `json:"mcp"`

	Config struct {
		Scope  string        `json:"scope"`
		Fields []ConfigField `json:"fields"`
	} `json:"config"`

	UI struct {
		Views     []View    `json:"views"`
		Menus     []Menu    `json:"menus"`
		Statusbar []BarItem `json:"statusbar"`
		RPC       []string  `json:"rpc"`
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

// ManifestTool 是 tools 模式的工具声明。
type ManifestTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
	Hot         bool           `json:"hot"`
	Idempotent  bool           `json:"idempotent"`
}

// View 是插件视图声明。
type View struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Icon        string         `json:"icon"`
	Entry       string         `json:"entry"`
	Containers  []string       `json:"containers"`
	DefaultSize map[string]int `json:"default_size"`
}

// Menu 是插件菜单声明。
type Menu struct {
	Location string `json:"location"`
	Label    string `json:"label"`
	Icon     string `json:"icon"`
	View     string `json:"view"`
}

// BarItem 是状态栏/顶栏条目声明。
type BarItem struct {
	ID      string         `json:"id"`
	Zone    string         `json:"zone"`
	Icon    string         `json:"icon"`
	Tooltip string         `json:"tooltip"`
	Command map[string]any `json:"command"`
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
	Options  []any  `json:"options"`
}

// loadManifest 从 CHONKPILOT_PLUGIN_ROOT 读取 plugin.json；失败返回 nil（不阻塞插件启动）。
func loadManifest() *Manifest {
	root := strings.TrimSpace(os.Getenv(EnvRoot))
	if root == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		return nil
	}
	var m Manifest
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return &m
}
