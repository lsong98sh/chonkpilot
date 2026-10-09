// MCP 配置目录读写（12-数据层 · 25-MCP与场景分层模型）：MCP server 定义 = **capability 根下的
// `mcps/` 子目录**（`<级别根>/capability/mcps/`；四级同构 app / user / project / prjusr）
// 下的**一个 JSON 文件**（文件名 = server 名 + `.json`，一个 server 一个文件）。
//
// 四级根与场景根**同构**（见 McpRoots / McpSystemRoot / McpUserRoot / McpProjectRoot /
// McpPrjUsrRoot）；字段沿用既有 MCP server 配置项（transport/url/runtime/args/env/hot_tools/
// timeout/isolate/sandbox 等，见 64-配置项一览 §3 与 gateway ServerEntry）。
//
// 同名跨级语义（**与场景不同**）：场景 id 全局唯一（无覆盖）；MCP server **同名跨级 = 最具体级
// 优先、整条覆盖**（prjusr > project > user > app），合并视图由 internal/mcp 组装。
//
// internal 门禁：本文件不导出给模块外。
package capfs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// McpsRoot 某级 capability 根下的 MCP 配置根 = <capRoot>/mcps。
func McpsRoot(capRoot string) string { return filepath.Join(capRoot, DirMcps) }

// McpSystemRoot 系统级 MCP 配置根 = <appDir>/mcps（appDir = 系统级 capability 根；
// appDir 空 → <exeDir>/capability/mcps）。
func McpSystemRoot(appDir string) (string, error) {
	cap, err := SystemRoot(appDir)
	if err != nil {
		return "", err
	}
	return McpsRoot(cap), nil
}

// McpUserRoot 用户级 MCP 配置根 = ~/.chonkpilot/capability/mcps（usrPath 注入时随其所在目录）。
func McpUserRoot(usrPath string) string { return McpsRoot(UserRoot(usrPath)) }

// McpProjectRoot 项目级 MCP 配置根 = <workdir>/.chonkpilot/capability/mcps。
func McpProjectRoot(workDir string) string { return McpsRoot(ProjectRoot(workDir)) }

// McpPrjUsrRoot 项目私有级 MCP 配置根 = <prjusr 数据根>/capability/mcps。
func McpPrjUsrRoot(projectID string) string { return McpsRoot(PrjUsrRoot(projectID)) }

// McpRoots 返回四级 MCP 配置根（系统 → 用户 → 项目 → 项目私有；与 scenario 的 ScenarioRoots 同构）。
// prjUsrCapRoot 为**项目私有 capability 根**（空串则不含该级，例如实例未登记）；workDir 空则不含项目级。
func McpRoots(appDir, usrPath, workDir, prjUsrCapRoot string) []Level {
	out := []Level{}
	if sys, err := McpSystemRoot(appDir); err == nil {
		out = append(out, Level{Kind: KindApp, Root: sys})
	}
	out = append(out, Level{Kind: KindUser, Root: McpUserRoot(usrPath)})
	if workDir != "" {
		out = append(out, Level{Kind: KindProject, Root: McpProjectRoot(workDir)})
	}
	if prjUsrCapRoot != "" {
		out = append(out, Level{Kind: KindPrjUsr, Root: McpsRoot(prjUsrCapRoot)})
	}
	return out
}

// mcpFileSuffix MCP 定义文件后缀。
const mcpFileSuffix = ".json"

// ValidMcpName 校验 server 名是否可作为文件名（与前端表单同一口径：字母开头，仅字母/数字/下划线）。
// 非法名**拒绝写入**（避免路径穿越 / 非法文件名）。
func ValidMcpName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// McpFileName 归一 server 名 → 文件名（<名>.json）。
func McpFileName(name string) string { return name + mcpFileSuffix }

// ListMcpNames 列出某级 MCP 根下 `<名>.json` 的 server 名（字母序；跳过非 .json / 隐藏项 / 非法名）。
func ListMcpNames(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, mcpFileSuffix) {
			continue
		}
		base := strings.TrimSuffix(name, mcpFileSuffix)
		if !ValidMcpName(base) {
			continue
		}
		out = append(out, base)
	}
	sort.Strings(out)
	return out
}

// ReadMcpFile 读某级 MCP 根下 `<名>.json` → 字段 map（不存在 / 非法 JSON / 非对象 → ok=false）。
// 返回的 map **不含** name/level（由调用方按上下文补）。
func ReadMcpFile(root, name string) (map[string]any, bool) {
	if !ValidMcpName(name) {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(root, McpFileName(name)))
	if err != nil {
		return nil, false
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

// WriteMcpFile 写某级 MCP 根下 `<名>.json`（覆盖；自动建目录）。name 非法 → 报错。
func WriteMcpFile(root, name string, obj map[string]any) error {
	if !ValidMcpName(name) {
		return os.ErrInvalid
	}
	target := filepath.Join(root, McpFileName(name))
	// 越界复验（A-38）：ValidMcpName 已挡 `..`/分隔符，此处再以 RealPathInside 复验实路径
	// （MCP 根内指向根外的 symlink 不得被 MkdirAll/WriteFile 跟随）。
	if !RealPathInside(root, target) {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	// 剔除上下文键（name/level 由文件名与所在级承载，不入文件正文）
	body := make(map[string]any, len(obj))
	for k, v := range obj {
		if k == "name" || k == "level" {
			continue
		}
		body[k] = v
	}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(raw, '\n'), 0o644)
}

// DeleteMcpFile 删某级 MCP 根下 `<名>.json`（不存在视为成功）。
func DeleteMcpFile(root, name string) error {
	if !ValidMcpName(name) {
		return nil
	}
	target := filepath.Join(root, McpFileName(name))
	// 越界复验（A-38）：根内指向根外的 symlink 不得被 os.Remove 跟随删除根外文件。
	if !RealPathInside(root, target) {
		return os.ErrInvalid
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// McpFileExists 判定某级 MCP 根下 `<名>.json` 是否存在。
func McpFileExists(root, name string) bool {
	if !ValidMcpName(name) {
		return false
	}
	st, err := os.Stat(filepath.Join(root, McpFileName(name)))
	return err == nil && !st.IsDir()
}
