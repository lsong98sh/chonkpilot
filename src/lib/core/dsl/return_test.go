package dsl

import (
	"strings"
	"sync"
	"testing"
)

// retFS 是**原始字节**语义的最小 FS（与 dsl_test.go 的行式 memFS 不同：
// Append 不做任何自动换行），用于精确断言 $RETURN 文件态的流式追加字节数。
type retFS struct {
	mu    sync.Mutex
	files map[string]string
}

func (r *retFS) Open(path string) FileHandle { return &retFH{fs: r, path: path} }

type retFH struct {
	fs   *retFS
	path string
}

func (f *retFH) Path() string { return f.path }

func (f *retFH) get() (string, bool) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	c, ok := f.fs.files[f.path]
	return c, ok
}

func (f *retFH) Exists() bool { _, ok := f.get(); return ok }

func (f *retFH) Stat() (FileInfo, error) {
	c, ok := f.get()
	if !ok {
		return FileInfo{}, errNotFound
	}
	return FileInfo{Path: f.path, Size: int64(len(c))}, nil
}

func (f *retFH) ReadText() (string, error) {
	c, ok := f.get()
	if !ok {
		return "", errNotFound
	}
	return c, nil
}

func (f *retFH) ReadLines() ([]string, error) {
	c, err := f.ReadText()
	if err != nil {
		return nil, err
	}
	return strings.Split(c, "\n"), nil
}

func (f *retFH) ReadRange(n, m int) ([]string, error) { return nil, errNotFound }

func (f *retFH) WriteAll(text string) error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	f.fs.files[f.path] = text
	return nil
}

func (f *retFH) Append(text string) error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	f.fs.files[f.path] = f.fs.files[f.path] + text // 原始字节追加（无自动换行）
	return nil
}

func (f *retFH) ReplaceLines(n, m int, lines []string) error { return errNotFound }

var errNotFound = errStr("not exist")

type errStr string

func (e errStr) Error() string { return string(e) }

// ─── $RETURN 结果通道（决策 42 §2 (249)）───

// TestReturnAccumulateInline：多次 `SET 值 => $RETURN` 累计追加（非覆盖），
// 段间以换行分隔；未超阈值 → inline 态（Text），Overflow=false。
func TestReturnAccumulateInline(t *testing.T) {
	fs := &memFS{files: map[string]string{}}
	eng := NewEngine(Options{Files: fs, ReturnFile: "ret.md"})
	ast, err := Parse("SET \"a\" => $RETURN\nSET 42 => $RETURN\nSET [1,2] => $RETURN\n", nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("execute: %v", err)
	}
	rv := eng.Return()
	if !rv.Used {
		t.Fatalf("Used 应为 true: %+v", rv)
	}
	if rv.Overflow {
		t.Fatalf("未超阈值不应转文件: %+v", rv)
	}
	if want := "a\n42\n[1,2]"; rv.Text != want {
		t.Fatalf("Text = %q, want %q", rv.Text, want)
	}
	if rv.File != "" {
		t.Fatalf("inline 态 File 应为空, got %q", rv.File)
	}
}

// TestReturnActionOutput：动作输出经 `=> $RETURN` 写入（与 SET 同一通道）。
func TestReturnActionOutput(t *testing.T) {
	fs := &memFS{files: map[string]string{}}
	act := Action{Name: "OUT", Run: func(s *Scope, args string) (string, error) { return "hello", nil }}
	eng := NewEngine(Options{Files: fs, Actions: []Action{act}})
	ast, err := Parse("OUT => $RETURN\n", []Action{act})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := eng.Return().Text; got != "hello" {
		t.Fatalf("动作输出未写入 $RETURN: %q", got)
	}
}

// TestReturnOverflowToFile：累计超 ReturnInlineLimit → 转文件流式追加；
// 返回 file 态（File 有值、Text 为空），落盘内容 = 全部段拼接。
func TestReturnOverflowToFile(t *testing.T) {
	fs := &retFS{files: map[string]string{}}
	chunk := strings.Repeat("x", 1000)
	var b strings.Builder
	const n = 70 // 70 * 1000 + 69 分隔符 = 70069 > 65536
	for i := 0; i < n; i++ {
		b.WriteString("SET \"" + chunk + "\" => $RETURN\n")
	}
	eng := NewEngine(Options{Files: fs, ReturnFile: "ret.md"})
	ast, err := Parse(b.String(), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("execute: %v", err)
	}
	rv := eng.Return()
	if !rv.Used || !rv.Overflow {
		t.Fatalf("应已转文件: %+v", rv)
	}
	if rv.File != "ret.md" {
		t.Fatalf("File = %q, want ret.md", rv.File)
	}
	if rv.Text != "" {
		t.Fatalf("file 态 Text 应为空, got %d bytes", len(rv.Text))
	}
	want := n*1000 + (n - 1)
	if rv.Size != want {
		t.Fatalf("Size = %d, want %d", rv.Size, want)
	}
	fs.mu.Lock()
	got := fs.files["ret.md"]
	fs.mu.Unlock()
	if len(got) != want {
		t.Fatalf("落盘长度 = %d, want %d", len(got), want)
	}
	if !strings.HasPrefix(got, chunk) {
		t.Fatal("落盘内容应以首段开头")
	}
	if want := returnSegSep + chunk; !strings.HasSuffix(got, want) {
		t.Fatal("落盘内容应以末段结尾")
	}
}

// TestReturnNoFileStaysInline：无 ReturnFile（空串）时超阈值仍留内存（仅 inline）。
func TestReturnNoFileStaysInline(t *testing.T) {
	fs := &memFS{files: map[string]string{}}
	var b strings.Builder
	chunk := strings.Repeat("y", 1000)
	for i := 0; i < 70; i++ {
		b.WriteString("SET \"" + chunk + "\" => $RETURN\n")
	}
	eng := NewEngine(Options{Files: fs}) // 无 ReturnFile
	ast, _ := Parse(b.String(), nil)
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("execute: %v", err)
	}
	rv := eng.Return()
	if rv.Overflow || rv.File != "" {
		t.Fatalf("无落盘目标不应转文件: %+v", rv)
	}
	if rv.Size <= ReturnInlineLimit {
		t.Fatalf("Size 应超阈值, got %d", rv.Size)
	}
}

// TestReturnUnused：脚本未用 `=> $RETURN` → Used=false（宿主回落既有汇总）。
func TestReturnUnused(t *testing.T) {
	fs := &memFS{files: map[string]string{}}
	eng := NewEngine(Options{Files: fs})
	ast, _ := Parse("SET 1 => a\n", nil)
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if eng.Return().Used {
		t.Fatalf("未使用 $RETURN 时 Used 应为 false")
	}
}

// TestReturnReadRejected：`$RETURN` 只写不可读——插值与取值两处均报错。
func TestReturnReadRejected(t *testing.T) {
	fs := &memFS{files: map[string]string{}}

	// ① 插值 {{$RETURN}}
	eng := NewEngine(Options{Files: fs})
	ast, _ := Parse("SET \"x{{$RETURN}}\" => a\n", nil)
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(eng.Result().Errors) == 0 {
		t.Fatal("插值读取 $RETURN 应报错")
	}

	// ② 作为值（读取侧）
	eng2 := NewEngine(Options{Files: fs})
	ast2, _ := Parse("SET $RETURN => x\n", nil)
	if err := eng2.Execute(ast2); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(eng2.Result().Errors) == 0 {
		t.Fatal("$RETURN 作为读取值应报错")
	}
}

// TestReturnRejectedAsBindingTarget：$RETURN 不可作 LOOP 绑定 / PUSH 目标（保留标识符）。
func TestReturnRejectedAsBindingTarget(t *testing.T) {
	fs := &memFS{files: map[string]string{}}
	cases := []string{
		"LOOP $RETURN=#\"in.json\"\nEND\n", // LOOP 绑定变量
		"PUSH 1 => $RETURN\n",              // PUSH 目标
		"TYPEOF 1 => $RETURN\n",            // TYPEOF 目标
	}
	for _, sc := range cases {
		eng := NewEngine(Options{Files: fs})
		ast, err := Parse(sc, nil)
		if err != nil {
			t.Fatalf("parse %q: %v", sc, err)
		}
		if err := eng.Execute(ast); err != nil {
			t.Fatalf("execute %q: %v", sc, err)
		}
		if len(eng.Result().Errors) == 0 {
			t.Fatalf("%q 应报错（$RETURN 为保留标识符）", sc)
		}
	}
}
