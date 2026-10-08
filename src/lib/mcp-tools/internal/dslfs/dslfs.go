// dslfs 提供 DSL 核心语句 `#"path"` 文件句柄（dsl.FileHandle）的共享实现：
// 路径强校验（R-11：绝对 / ~/ / !/；相对路径报错）与 agentbox 沙箱读写校验统一在此收口。
// filesys_run / browser_run / desktop_run 三域共用本实现，仅以 Profile 选择各自既有的行为档位
// （三域历史实现存在少量语义差异，逐字保真以避免回归）。
package dslfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// Profile 选择句柄的既有行为差异（三域历史实现不同，逐字保留）。
type Profile struct {
	StatBlocks     bool // Stat 是否计算 Blocks（filesys/desktop=true；browser=false）
	LenientRange   bool // 空文件 ReadRange 是否恒返回空（browser=true；filesys/desktop=false）
	StrictMkdirErr bool // WriteAll 的 MkdirAll 失败是否上抛（filesys/desktop=true；browser=false）
	AppendMkdir    bool // Append 前是否 MkdirAll（filesys/desktop=true；browser=false）
	LockWrites     bool // ReplaceLines 是否加写锁（filesys/desktop=true；browser=false）
}

// Default 是 filesys_run / desktop_run 档（历史行为最完备的一版）。
var Default = Profile{StatBlocks: true, StrictMkdirErr: true, AppendMkdir: true, LockWrites: true}

// Browser 是 browser_run 档（历史实现，语义略宽：空文件 ReadRange 恒返回空）。
var Browser = Profile{LenientRange: true}

// File 是 dsl.FileHandle 的共享实现（path + profile；写锁见 pathLocks）。
type File struct {
	path string
	prof Profile
}

// pathLocks 是**进程级**文件写锁表（key = 解析后的绝对路径）。DSL 每次 `#"path"` 引用都会经
// FileSystem.Open 新建句柄，句柄级互斥无法跨句柄生效（C-08）；改用按路径的进程级互斥，
// 保证并发 ReplaceLines（LockWrites 档）对同一文件串行化。
var pathLocks sync.Map // map[string]*sync.Mutex

// pathLock 取（或新建）某绝对路径的互斥锁。
func pathLock(abs string) *sync.Mutex {
	v, _ := pathLocks.LoadOrStore(abs, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// New 创建句柄（path 为 DSL 原始引用；解析与校验在各方法内延迟进行）。
func New(path string, prof Profile) *File { return &File{path: path, prof: prof} }

func (f *File) Path() string { return f.path }

// abs 解析句柄路径为绝对路径（R-11：绝对 / ~/ / !/；相对路径报错）并做 agentbox 沙箱校验
// （write=true 需落在可写目录内；未启用隔离 = 一律放行）。
func (f *File) abs(write bool) (string, error) {
	abs, msg := paths.ResolvePath(f.path, "")
	if msg != "" {
		return "", errors.New(msg)
	}
	if abs == "" {
		return "", errors.New(paths.InvalidPathMessage(f.path))
	}
	if err := agentbox.Check(abs, write); err != nil {
		return "", err
	}
	return abs, nil
}

// Exists 路径不合规（R-11）或被沙箱拒绝时按「不存在」处理：字面违规已由预校验拦截，此处兜底 {{}} 插值。
func (f *File) Exists() bool {
	abs, err := f.abs(false)
	if err != nil {
		return false
	}
	_, serr := os.Stat(abs)
	return serr == nil
}

func (f *File) Stat() (dsl.FileInfo, error) {
	abs, err := f.abs(false)
	if err != nil {
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
	if f.prof.StatBlocks && content != "" {
		blocks = strings.Count(content, "\n\n") + 1
	}
	return dsl.FileInfo{Path: f.path, Size: fi.Size(), Lines: int64(lines), Blocks: int64(blocks)}, nil
}

func (f *File) ReadText() (string, error) {
	abs, err := f.abs(false)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (f *File) ReadLines() ([]string, error) {
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

func (f *File) ReadRange(n, m int) ([]string, error) {
	ls, err := f.ReadLines()
	if err != nil {
		return nil, err
	}
	L := len(ls)
	if L == 0 {
		// browser 档：空文件恒返回空；filesys/desktop 档：仅「全量」请求返回空，否则报错。
		if f.prof.LenientRange {
			return []string{}, nil
		}
		if n == 0 && (m == 0 || m == -1) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("range 越界（空文件）")
	}
	n, m = normalizeRange(n, m, L)
	if n < 0 || n >= L || m < n || m >= L {
		if f.prof.LenientRange {
			return nil, fmt.Errorf("range 越界")
		}
		return nil, fmt.Errorf("range(%d,%d) 越界（共 %d 行）", n, m, L)
	}
	return ls[n : m+1], nil
}

func (f *File) WriteAll(text string) error {
	// 写入路径强校验（R-11，运行时解析：覆盖 {{}} 插值的重定向目标与 !/ 临时目录）+ 沙箱写校验
	abs, err := f.abs(true)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(abs); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil && f.prof.StrictMkdirErr {
			return err
		}
	}
	return os.WriteFile(abs, []byte(text), 0o644)
}

func (f *File) Append(text string) error {
	if text == "" {
		return nil
	}
	abs, err := f.abs(true)
	if err != nil {
		return err
	}
	if f.prof.AppendMkdir {
		if dir := filepath.Dir(abs); dir != "." {
			_ = os.MkdirAll(dir, 0o755)
		}
	}
	fd, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer fd.Close()
	_, err = fd.WriteString(text)
	return err
}

func (f *File) ReplaceLines(n, m int, lines []string) error {
	abs, err := f.abs(true)
	if err != nil {
		return err
	}
	if f.prof.LockWrites {
		// 进程级按路径写锁（跨句柄生效；见 pathLocks 注释）。
		mu := pathLock(abs)
		mu.Lock()
		defer mu.Unlock()
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
	n, m = normalizeRange(n, m, L)
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

// normalizeRange 归一化负索引（-1 = 末行），返回归一后的 (n, m)。
func normalizeRange(n, m, L int) (int, int) {
	if n < 0 {
		n += L
	}
	if m < 0 {
		m += L
	}
	return n, m
}
