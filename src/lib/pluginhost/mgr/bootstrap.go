package mgr

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BootEntry 是 plugin.ini 中的一行启动命令。
//
// 约定（D-71）：
//   - 文件固定为 <exeDir>/plugin.ini；
//   - 每行一个可执行命令行（exe [args...]），行序即启动顺序；
//   - 可写在 [bootstrap] 段内；文件里一旦出现任何段头，则只取 [bootstrap] 段；
//   - `#` / `;` 开头为注释，空行跳过；
//   - 相对路径由调用方按 exe 目录解析（本包不猜测工作目录）。
type BootEntry struct {
	// Command 为原始命令行（已去首尾空白）。
	Command string
	// Exe 为命令行首个 token。
	Exe string
	// Args 为其余 token。
	Args []string
	// Line 为在文件中的行号（从 1 开始）。
	Line int
}

// ErrEmptyCommand 表示命令行为空（不应发生，解析阶段已跳过）。
var ErrEmptyCommand = errors.New("plugin.ini: empty command")

// LoadBootstrap 读取 plugin.ini；文件不存在时返回 (nil, nil)。
func LoadBootstrap(path string) ([]BootEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return ParseBootstrap(f)
}

// ParseBootstrap 解析 plugin.ini 内容。
func ParseBootstrap(r io.Reader) ([]BootEntry, error) {
	var (
		entries []BootEntry
		// hasSection 表示文件里出现过段头；section 为当前段名（小写）。
		hasSection bool
		section    string
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, ";") {
			continue
		}
		if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
			hasSection = true
			section = strings.ToLower(strings.TrimSpace(raw[1 : len(raw)-1]))
			continue
		}
		// 有段头时只认 [bootstrap]。
		if hasSection && section != "bootstrap" {
			continue
		}
		fields, err := splitCommand(raw)
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			continue
		}
		entries = append(entries, BootEntry{
			Command: raw,
			Exe:     fields[0],
			Args:    fields[1:],
			Line:    line,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// splitCommand 按空白切分命令行，支持双引号包裹（不处理转义）。
func splitCommand(s string) ([]string, error) {
	var (
		fields  []string
		cur     strings.Builder
		inQuote bool
	)
	flush := func() {
		if cur.Len() > 0 {
			fields = append(fields, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	if inQuote {
		return nil, errors.New("plugin.ini: unbalanced quote")
	}
	flush()
	return fields, nil
}

// applyBootstrap 按 plugin.ini 决定「启动谁、按什么顺序」。
//
// 语义（D-71）：
//   - 清单存在且至少一行 → 清单是唯一权威：清单内插件启用并按行序启动，清单外一律禁用；
//   - 清单不存在 / 为空 → 不动，保持 enabled.json + manifest.autostart 的既有行为；
//   - 行内 exe 相对 plugin.ini 所在目录解析，作为该插件的 spawn 路径（覆盖 manifest.runtime.exe）；
//   - 清单里写了一个没有 manifest 的插件 → 记日志并跳过（宿主不扫目录猜 manifest）。
func (m *Manager) applyBootstrap() {
	path := strings.TrimSpace(m.opts.BootstrapFile)
	if path == "" {
		return
	}
	entries, err := LoadBootstrap(path)
	if err != nil {
		m.logf("plugin.ini: 解析失败 %s: %v", path, err)
		return
	}
	if len(entries) == 0 {
		return
	}
	baseDir := filepath.Dir(path)

	m.mu.Lock()
	defer m.mu.Unlock()

	byExe := make(map[string]string, len(m.plugins))
	for id, p := range m.plugins {
		if p == nil || p.Manifest == nil {
			continue
		}
		exe := strings.TrimSpace(p.Manifest.Runtime.Exe)
		if exe == "" {
			continue
		}
		byExe[strings.ToLower(filepath.Base(strings.ReplaceAll(exe, `\`, `/`)))] = id
	}

	listed := make(map[string]bool, len(entries))
	order := make([]string, 0, len(m.plugins))
	for _, e := range entries {
		exe := strings.ReplaceAll(e.Exe, `\`, `/`)
		if !filepath.IsAbs(exe) {
			exe = filepath.Join(baseDir, filepath.FromSlash(exe))
		}
		base := strings.ToLower(filepath.Base(exe))
		id := byExe[base]
		if id == "" {
			// 未匹配到 manifest：按产物命名约定推导 id（chonkpilot-plugin-<id>.exe）。
			id = strings.TrimSuffix(base, ".exe")
			id = strings.TrimPrefix(id, "chonkpilot-plugin-")
		}
		p := m.plugins[id]
		if p == nil {
			m.logf("plugin.ini: 未找到插件 %q（跳过；宿主不扫描目录）", e.Command)
			continue
		}
		if listed[id] {
			continue
		}
		listed[id] = true
		p.enabled = true
		p.exeOverride = exe
		order = append(order, id)
	}
	// 清单外一律禁用；保留在 order 尾部（UI 仍能看到，状态 = disabled）。
	for _, id := range m.order {
		if listed[id] {
			continue
		}
		if p := m.plugins[id]; p != nil {
			p.enabled = false
			p.exeOverride = ""
		}
		order = append(order, id)
	}
	m.order = order
}
