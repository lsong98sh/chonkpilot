// scriptfs.go — filesys_run DSL 核心句柄（`#"path"` 数据源读写）使用的校验型文件系统（R-11）。
//
// filesys_run 的动作动词（RPL/APD/PTC/INS/DEL/MOV/CPY）在 actions.go 内自行解析并校验
// 路径；本 FS 服务 DSL 核心语句的 `#"path"` 句柄——LOOP/SET 数据源、访问器
// （.content/.lines/.array/.object/.range）、以及 `=> #"file"` 重定向目标。
// 这些引用同样须绝对路径、`~/` 开头或 `!/` 开头（临时目录）：字面路径由 manager.go 在执行前
// 预校验拦截，{{}} 插值得到的路径由本 FS 在执行时解析并兜底校验（含 !/ → <temp>/chonkpilot/<instance>/）。
package fileops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// ScriptFS 是 filesys_run 的 dsl.FileSystem 实现（零状态）。
type ScriptFS struct{}

// Open 返回句柄（路径强校验在各句柄方法内完成）。
func (ScriptFS) Open(path string) dsl.FileHandle { return &scriptFile{path: path} }

type scriptFile struct {
	path string
	mu   sync.Mutex
}

// abs 解析句柄路径为绝对路径（R-11：绝对 / ~/ / !/；相对路径报错）。
func (f *scriptFile) abs() (string, error) {
	return ResolveLocalPath(f.path)
}

func (f *scriptFile) Path() string { return f.path }

// Exists 路径不合规或被沙箱拒绝时按「不存在」处理（字面违规已由预校验拦截，此处只兜底）。
func (f *scriptFile) Exists() bool {
	abs, err := f.abs()
	if err != nil {
		return false
	}
	if err := sandboxErr(abs, false); err != nil {
		return false
	}
	_, serr := os.Stat(abs)
	return serr == nil
}

func (f *scriptFile) Stat() (dsl.FileInfo, error) {
	abs, err := f.abs()
	if err != nil {
		return dsl.FileInfo{}, err
	}
	if err := sandboxErr(abs, false); err != nil {
		return dsl.FileInfo{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return dsl.FileInfo{}, err
	}
	content, _ := f.ReadText()
	lines := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") && content != "" {
		lines++
	}
	blocks := 0
	if content != "" {
		blocks = strings.Count(content, "\n\n") + 1
	}
	return dsl.FileInfo{Path: f.path, Size: fi.Size(), Lines: int64(lines), Blocks: int64(blocks)}, nil
}

func (f *scriptFile) ReadText() (string, error) {
	abs, err := f.abs()
	if err != nil {
		return "", err
	}
	// agentbox 沙箱（仅隔离开启时生效）：允许目录外一律拒绝读
	if err := sandboxErr(abs, false); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (f *scriptFile) ReadLines() ([]string, error) {
	c, err := f.ReadText()
	if err != nil {
		return nil, err
	}
	ls := strings.Split(c, "\n")
	if len(ls) > 0 && ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	return ls, nil
}

func (f *scriptFile) ReadRange(n, m int) ([]string, error) {
	ls, err := f.ReadLines()
	if err != nil {
		return nil, err
	}
	L := len(ls)
	if L == 0 {
		if n == 0 && (m == 0 || m == -1) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("range 越界（空文件）")
	}
	n, m = scriptNormalizeRange(n, m, L)
	if n < 0 || n >= L || m < n || m >= L {
		return nil, fmt.Errorf("range(%d,%d) 越界（共 %d 行）", n, m, L)
	}
	return ls[n : m+1], nil
}

func (f *scriptFile) WriteAll(text string) error {
	abs, err := f.abs()
	if err != nil {
		return err
	}
	// agentbox 沙箱（仅隔离开启时生效）：允许目录外一律拒绝写
	if err := sandboxErr(abs, true); err != nil {
		return err
	}
	if dir := filepath.Dir(abs); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(abs, []byte(text), 0o644)
}

func (f *scriptFile) Append(text string) error {
	if text == "" {
		return nil
	}
	abs, err := f.abs()
	if err != nil {
		return err
	}
	if err := sandboxErr(abs, true); err != nil {
		return err
	}
	if dir := filepath.Dir(abs); dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	fd, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer fd.Close()
	_, err = fd.WriteString(text)
	return err
}

func (f *scriptFile) ReplaceLines(n, m int, lines []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	abs, err := f.abs()
	if err != nil {
		return err
	}
	if err := sandboxErr(abs, true); err != nil {
		return err
	}
	cur, err := f.ReadText()
	if err != nil {
		return err
	}
	ls := strings.Split(cur, "\n")
	if len(ls) > 0 && ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	L := len(ls)
	n, m = scriptNormalizeRange(n, m, L)
	if n >= L || n < 0 {
		return nil
	}
	if m >= L {
		m = L - 1
	}
	head := append([]string{}, ls[:n]...)
	mid := append([]string{}, lines...)
	tail := []string{}
	if m+1 < L {
		tail = append([]string{}, ls[m+1:]...)
	}
	out := append(head, append(mid, tail...)...)
	return os.WriteFile(abs, []byte(strings.Join(out, "\n")), 0o644)
}

func scriptNormalizeRange(n, m, L int) (int, int) {
	if n < 0 {
		n += L
	}
	if m < 0 {
		m += L
	}
	return n, m
}
