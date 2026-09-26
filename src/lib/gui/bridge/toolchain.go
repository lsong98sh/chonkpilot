// 工具链探测（gui.toolchain.detect）：启动/打开路径配置页时探测 6 个工具链与 Chrome 的
// 可执行文件路径 + 版本，作为路径配置页「系统」页签的候选（61-消息一览 §1 gui.*）。
// 探测结果不落库（内存候选，12-数据层 系统默认 = 资源/常量）。
package bridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/winproc"
)

// toolchainCandidate 单个工具链的探测描述。
type toolchainCandidate struct {
	ID      string // 配置 key（javaPath/pythonPath/... 的前缀）
	Name    string // 展示名
	Bin     string // 可执行文件名（不含 .exe）
	AltBins []string
	Args    []string
	EnvDir  []string // 环境变量目录（直接拼 bin）
	Dirs    []string // 常见安装目录（支持 * 通配子目录）
	DirVer  bool     // 版本来自 exe 同目录的「版本号子目录」（Chrome：Application\<ver>\）
}

// toolchainCandidates 探测清单（chrome 走同一机制，供路径页系统页签展示）。
var toolchainCandidates = []toolchainCandidate{
	{ID: "java", Name: "Java", Bin: "java", Args: []string{"-version"},
		EnvDir: []string{"JAVA_HOME"},
		Dirs:   []string{`%ProgramFiles%\Java`, `%ProgramFiles%\Eclipse Adoptium`, `%ProgramFiles%\Microsoft`}},
	{ID: "python", Name: "Python", Bin: "python", AltBins: []string{"python3"}, Args: []string{"--version"},
		Dirs: []string{`%LOCALAPPDATA%\Programs\Python`, `%ProgramFiles%\Python*`}},
	{ID: "node", Name: "Node.js", Bin: "node", Args: []string{"--version"},
		Dirs: []string{`%ProgramFiles%\nodejs`}},
	{ID: "go", Name: "Go", Bin: "go", Args: []string{"version"},
		EnvDir: []string{"GOROOT"},
		Dirs:   []string{`%ProgramFiles%\Go`, `C:\Go`}},
	{ID: "rust", Name: "Rust", Bin: "rustc", Args: []string{"--version"},
		Dirs: []string{`%USERPROFILE%\.cargo`}},
	{ID: "c", Name: "C/C++", Bin: "gcc", AltBins: []string{"clang", "cl"}, Args: []string{"--version"},
		Dirs: []string{`%ProgramFiles%\mingw64`, `C:\msys64\mingw64`, `%ProgramFiles%\LLVM`}},
	// chrome：官方安装器必写注册表 App Paths；版本从 Application\<ver>\ 目录名取
	// （不执行 chrome.exe --version——Windows 上会转发给已运行实例并弹出窗口）。
	{ID: "chrome", Name: "Chrome", Bin: "chrome", DirVer: true,
		Dirs: []string{`%ProgramFiles%\Google\Chrome\Application`, `%ProgramFiles(x86)%\Google\Chrome\Application`, `%LOCALAPPDATA%\Google\Chrome\Application`}},
}

// callDetectToolchains 探测全部候选：{tools:[{id,name,path,version}]}（未找到 → path 空）。
func callDetectToolchains(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	tools := make([]map[string]any, 0, len(toolchainCandidates))
	for _, c := range toolchainCandidates {
		path := findToolchain(c)
		item := map[string]any{"id": c.ID, "name": c.Name, "path": path, "version": ""}
		if path != "" {
			item["version"] = toolchainVersion(c, path)
		}
		tools = append(tools, item)
	}
	return json.Marshal(map[string]any{"tools": tools})
}

// toolchainVersion 取版本：优先「版本号子目录」（DirVer），否则执行 `<path> <args>`
// 取首行输出；Args 为空时不执行进程（避免 chrome 一类 GUI 程序被误启动）。
func toolchainVersion(c toolchainCandidate, path string) string {
	if c.DirVer {
		return versionFromDir(path)
	}
	if len(c.Args) == 0 {
		return ""
	}
	return probeVersion(path, c.Args)
}

// findToolchain 依次按 PATH → 注册表 App Paths → 环境变量目录 → 常见安装目录查找可执行文件。
func findToolchain(c toolchainCandidate) string {
	bins := append([]string{c.Bin}, c.AltBins...)
	for _, bin := range bins {
		if p, err := exec.LookPath(bin); err == nil {
			return p
		}
	}
	for _, bin := range bins {
		exe := bin
		if !strings.HasSuffix(strings.ToLower(exe), ".exe") {
			exe += ".exe"
		}
		// 注册表 App Paths\<exe>：官方安装器登记，覆盖非标准安装目录
		if p := lookupAppPath(exe); p != "" {
			return p
		}
		for _, env := range c.EnvDir {
			base := os.Getenv(env)
			if base == "" {
				continue
			}
			for _, sub := range []string{"bin", ""} {
				p := filepath.Join(base, sub, exe)
				if fileExists(p) {
					return p
				}
			}
		}
		for _, dir := range c.Dirs {
			expanded := expandWinEnv(dir)
			if p := globTool(expanded, exe); p != "" {
				return p
			}
			// 通配一层版本子目录（如 ...\Java\jdk-21\bin\java.exe）
			matches, _ := filepath.Glob(filepath.Join(expanded, "*", "bin", exe))
			if len(matches) > 0 {
				return matches[0]
			}
		}
	}
	return ""
}

// expandWinEnv 展开候选目录里的环境变量：os.ExpandEnv 只认 $VAR/${VAR}，会把 Windows 的
// %ProgramFiles% / %LOCALAPPDATA% 原样保留（导致候选目录全都查不到）；这里同时支持 %VAR%。
func expandWinEnv(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '%' {
			if j := strings.IndexByte(s[i+1:], '%'); j > 0 {
				b.WriteString(os.Getenv(s[i+1 : i+1+j]))
				i += j + 2
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return os.ExpandEnv(b.String())
}

// globTool 在目录（及其 * 通配子目录）下查找可执行文件。
func globTool(dir, exe string) string {
	if p := filepath.Join(dir, exe); fileExists(p) {
		return p
	}
	for _, sub := range []string{"bin", filepath.Join("*", "bin")} {
		if matches, _ := filepath.Glob(filepath.Join(dir, sub, exe)); len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// versionFromDir 从可执行文件同目录的「版本号子目录」取版本。Chrome 布局为
// Application\chrome.exe + Application\<version>\，这是 Windows 上唯一可靠的版本来源：
// chrome.exe --version 会把请求转发给已运行实例（输出「正在现有的浏览器会话中打开」并弹窗）。
func versionFromDir(exePath string) string {
	entries, err := os.ReadDir(filepath.Dir(exePath))
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() || !isVersionLike(e.Name()) {
			continue
		}
		if best == "" || compareVersion(e.Name(), best) > 0 {
			best = e.Name()
		}
	}
	return best
}

// isVersionLike 判断目录名是否为「点分纯数字」版本号（如 152.0.7977.84）：至少两段，
// 每段非空且全为数字——据此排除 PlatformExperienceHelper/SetupMetrics 一类同名目录。
func isVersionLike(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}

// compareVersion 按点分数字逐段比较（缺段补 0），返回 a 相对 b 的正负。
func compareVersion(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		x, y := 0, 0
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

// probeVersion 执行 `<path> <args>` 取首行版本输出（2s 超时；隐藏控制台窗口）。
func probeVersion(path string, args []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = winproc.SysProcAttr()
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}
