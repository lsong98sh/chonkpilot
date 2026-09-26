package dsl

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ─── 消费方注入的 I/O 抽象（真实实现：llm_run/desktop/browser；测试：内存 Mock）───

// FileInfo 文件元信息。
type FileInfo struct {
	Path   string // 路径
	Size   int64  // bytes
	Lines  int64  // 行数
	Blocks int64  // 段落数（近似）
}

// FileHandle 文件句柄（读写一体；缺省文件在写时创建）。
type FileHandle interface {
	Path() string
	// Stat 返回元信息；文件不存在返回 errNotExist（由消费方定义，如 os.IsNotExist 风格）。
	Stat() (FileInfo, error)
	// Exists 文件是否存在。
	Exists() bool
	// ReadText 全文。
	ReadText() (string, error)
	// ReadLines 按行解析。
	ReadLines() ([]string, error)
	// ReadRange 返回第 n..m 行（0 基含端点，负数=倒数，越界报错）。
	ReadRange(n, m int) ([]string, error)
	// WriteAll 覆盖写。
	WriteAll(text string) error
	// Append 追加。
	Append(text string) error
	// ReplaceLines 替换 n..m 行（超出范围的部分静默忽略）。
	ReplaceLines(n, m int, lines []string) error
}

// FileSystem 按路径取文件句柄。
type FileSystem interface {
	Open(path string) FileHandle
}

// DBHandle 数据库句柄。
type DBHandle interface {
	Path() string
	// Exists 数据库文件存在。
	Exists() bool
	// Size 数据库文件大小。
	Size() int64
	// TableNames 表名列表。
	TableNames() ([]string, error)
	// Table 取表句柄（缺表在写时创建；读时按 Exists 判定）。
	Table(name string) TableHandle
	// Describe 结构性描述（用于插值）。
	Describe() string
}

// TableHandle 表句柄（记录 = JSON 值：object/标量）。
type TableHandle interface {
	Name() string
	// Exists 表存在且非空。
	Exists() bool
	// Count 记录数。
	Count() int64
	// Records 全部记录。
	Records() ([]any, error)
	// RecordsRange 第 n..m 条（0 基含端点，负数=倒数，越界报错）。
	RecordsRange(n, m int) ([]any, error)
	// Query 简单过滤（level=error&time>...；由消费方实现）。
	Query(expr string) ([]any, error)
	// WriteRecords 覆盖写。
	WriteRecords(recs []any) error
	// AppendRecord 追加一条。
	AppendRecord(rec any) error
	// ReplaceRange 替换 n..m 条（超界静默忽略）。
	ReplaceRange(n, m int, recs []any) error
	// Describe 结构性描述（用于插值）。
	Describe() string
}

// DBSystem 按路径取数据库句柄。
type DBSystem interface {
	OpenDB(path string) DBHandle
}

// ─── 引擎内部句柄值（出现在脚本变量/表达式里）───

// VFile 文件句柄值。
type VFile struct {
	FS   FileSystem
	Path string
	// tmp 解析缓存（同一求值内避免重复 stat）
}

// VDB 数据库句柄值。
type VDB struct {
	DB   DBSystem
	Path string
}

// VTable 表句柄值（db.tables.表 的求值结果）。
type VTable struct {
	DB   *VDB
	Name string
	Th   TableHandle
}

func (f *VFile) handle() FileHandle { return f.FS.Open(f.Path) }

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1fGB", float64(n)/(1024*1024*1024))
	}
}

func describeFile(path string, st FileInfo, ok bool) string {
	if !ok {
		return fmt.Sprintf("<file: %s, missing>", filepath.Base(path))
	}
	return fmt.Sprintf("<file: %s, size: %s, lines: %d>", filepath.Base(path), humanSize(st.Size), st.Lines)
}

// ─── 缺省注入（Options.Files / Options.DBs 未提供时报错，避免 nil 解引用）───

type nilFS struct{}

func (nilFS) Open(path string) FileHandle { return &errFH{path: path} }

type errFH struct{ path string }

func (f *errFH) Path() string { return f.path }
func (f *errFH) Exists() bool { return false }
func (f *errFH) Stat() (FileInfo, error) {
	return FileInfo{}, fmt.Errorf("文件系统未注入（Options.Files），句柄 %q 不可用", f.path)
}
func (f *errFH) ReadText() (string, error) {
	return "", fmt.Errorf("文件系统未注入（Options.Files），句柄 %q 不可用", f.path)
}
func (f *errFH) ReadLines() ([]string, error) { return nil, fmt.Errorf("文件系统未注入") }
func (f *errFH) ReadRange(int, int) ([]string, error) {
	return nil, fmt.Errorf("文件系统未注入")
}
func (f *errFH) WriteAll(string) error                 { return fmt.Errorf("文件系统未注入") }
func (f *errFH) Append(string) error                   { return fmt.Errorf("文件系统未注入") }
func (f *errFH) ReplaceLines(int, int, []string) error { return fmt.Errorf("文件系统未注入") }

type nilDB struct{}

func (nilDB) OpenDB(path string) DBHandle { return &errDBH{path: path} }

type errDBH struct{ path string }

func (d *errDBH) Path() string { return d.path }
func (d *errDBH) Exists() bool { return false }
func (d *errDBH) Size() int64  { return 0 }
func (d *errDBH) TableNames() ([]string, error) {
	return nil, fmt.Errorf("数据库未注入（Options.DBs），库 %q 不可用", d.path)
}
func (d *errDBH) Table(string) TableHandle { return &errTB{} }
func (d *errDBH) Describe() string {
	return fmt.Sprintf("<db: %s, unavailable>", d.path)
}

type errTB struct{}

func (t *errTB) Name() string                         { return "" }
func (t *errTB) Exists() bool                         { return false }
func (t *errTB) Count() int64                         { return 0 }
func (t *errTB) Records() ([]any, error)              { return nil, fmt.Errorf("数据库未注入") }
func (t *errTB) RecordsRange(int, int) ([]any, error) { return nil, fmt.Errorf("数据库未注入") }
func (t *errTB) Query(string) ([]any, error)          { return nil, fmt.Errorf("数据库未注入") }
func (t *errTB) WriteRecords([]any) error             { return fmt.Errorf("数据库未注入") }
func (t *errTB) AppendRecord(any) error               { return fmt.Errorf("数据库未注入") }
func (t *errTB) ReplaceRange(int, int, []any) error   { return fmt.Errorf("数据库未注入") }
func (t *errTB) Describe() string                     { return "<table: unavailable>" }

// describe 句柄插值只出描述串（不读内容）。
func describe(v any) string {
	switch t := v.(type) {
	case *VFile:
		f := t.handle()
		st, err := f.Stat()
		return describeFile(t.Path, st, err == nil)
	case *VDB:
		db := t.DB.OpenDB(t.Path)
		names, _ := db.TableNames()
		return fmt.Sprintf("<db: %s, tables: [%s]>", filepath.Base(t.Path), strings.Join(names, ", "))
	case *VTable:
		return t.Th.Describe()
	default:
		return ""
	}
}
