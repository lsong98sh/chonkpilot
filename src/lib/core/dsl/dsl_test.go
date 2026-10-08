package dsl

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── 内存 Mock（测试用 I/O）───

type memFS struct {
	mu    sync.Mutex
	files map[string]string
}

func (m *memFS) Open(path string) FileHandle { return &memFH{fs: m, path: path} }

type memFH struct {
	fs   *memFS
	path string
}

func (f *memFH) Path() string { return f.path }

func (f *memFH) stat() (string, bool) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	c, ok := f.fs.files[f.path]
	return c, ok
}

func (f *memFH) Exists() bool { _, ok := f.stat(); return ok }

func (f *memFH) Stat() (FileInfo, error) {
	c, ok := f.stat()
	if !ok {
		return FileInfo{}, fmt.Errorf("not exist: %s", f.path)
	}
	lines := len(strings.Split(c, "\n"))
	if strings.HasSuffix(c, "\n") {
		lines--
	}
	if c == "" {
		lines = 0
	}
	blocks := 0
	if c != "" {
		blocks = strings.Count(c, "\n\n") + 1
	}
	return FileInfo{Path: f.path, Size: int64(len(c)), Lines: int64(lines), Blocks: int64(blocks)}, nil
}

func (f *memFH) ReadText() (string, error) {
	c, ok := f.stat()
	if !ok {
		return "", fmt.Errorf("file not exist: %s", f.path)
	}
	return c, nil
}

func (f *memFH) ReadLines() ([]string, error) {
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

func (f *memFH) ReadRange(n, m int) ([]string, error) {
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
	n, m = normalizeIdx(n, m, L)
	if n < 0 || n >= L || m < n || m >= L {
		return nil, fmt.Errorf("range(%d,%d) 越界（共 %d 行）", n, m, L)
	}
	return ls[n : m+1], nil
}

func (f *memFH) WriteAll(text string) error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	f.fs.files[f.path] = text
	return nil
}

func (f *memFH) Append(text string) error {
	if text == "" {
		return nil
	}
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	cur := f.fs.files[f.path]
	if cur == "" {
		f.fs.files[f.path] = text
		return nil
	}
	if !strings.HasSuffix(cur, "\n") {
		cur += "\n"
	}
	f.fs.files[f.path] = cur + text
	return nil
}

func (f *memFH) ReplaceLines(n, m int, lines []string) error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	cur, ok := f.fs.files[f.path]
	if !ok {
		return fmt.Errorf("file not exist: %s", f.path)
	}
	ls := strings.Split(cur, "\n")
	if len(ls) > 0 && ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	L := len(ls)
	if n < 0 {
		n += L
	}
	if m < 0 {
		m += L
	}
	if n >= L || n < 0 {
		return nil // 超界静默忽略
	}
	if m >= L {
		m = L - 1
	}
	head := append([]string{}, ls[:n]...)
	replace := append([]string{}, lines...)
	tail := []string{}
	if m+1 < L {
		tail = append([]string{}, ls[m+1:]...)
	}
	out := append(head, replace...)
	out = append(out, tail...)
	f.fs.files[f.path] = strings.Join(out, "\n")
	return nil
}

// memDB 数据库 Mock（每库 = 表名 → 记录列表；记录为 JSON 值）。
type memDB struct {
	mu  sync.Mutex
	dbs map[string]*memDBH
}

func (m *memDB) OpenDB(path string) DBHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.dbs[path]
	if !ok {
		h = &memDBH{path: path, tables: map[string]*memTB{}}
		m.dbs[path] = h
	}
	return h
}

type memDBH struct {
	path   string
	tables map[string]*memTB
}

func (d *memDBH) Path() string { return d.path }

func (d *memDBH) Exists() bool { return len(d.tables) > 0 }

func (d *memDBH) Size() int64 {
	var n int64
	for _, t := range d.tables {
		for _, r := range t.rows {
			if b, err := json.Marshal(r); err == nil {
				n += int64(len(b))
			}
		}
	}
	return n
}

func (d *memDBH) TableNames() ([]string, error) {
	var out []string
	for k := range d.tables {
		out = append(out, k)
	}
	return out, nil
}

func (d *memDBH) Table(name string) TableHandle {
	t, ok := d.tables[name]
	if !ok {
		t = &memTB{name: name}
		d.tables[name] = t
	}
	return t
}

func (d *memDBH) Describe() string {
	names, _ := d.TableNames()
	return fmt.Sprintf("<db: %s, tables: [%s]>", strings.TrimSuffix(d.path, ".db"), strings.Join(names, ", "))
}

type memTB struct {
	name string
	rows []any
}

func (t *memTB) Name() string { return t.name }
func (t *memTB) Exists() bool { return len(t.rows) > 0 }
func (t *memTB) Count() int64 { return int64(len(t.rows)) }
func (t *memTB) Records() ([]any, error) {
	return t.rows, nil
}
func (t *memTB) RecordsRange(n, m int) ([]any, error) {
	L := len(t.rows)
	if L == 0 {
		if n == 0 && (m == 0 || m == -1) {
			return []any{}, nil
		}
		return nil, fmt.Errorf("range 越界（空表）")
	}
	n, m = normalizeIdx(n, m, L)
	if n < 0 || n >= L || m < n || m >= L {
		return nil, fmt.Errorf("range(%d,%d) 越界（共 %d 条）", n, m, L)
	}
	return t.rows[n : m+1], nil
}

func (t *memTB) Query(expr string) ([]any, error) {
	conds := strings.Split(expr, "&")
	var out []any
	for _, r := range t.rows {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		hit := true
		for _, c := range conds {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			op := "="
			var key, val string
			for _, cand := range []string{">=", "<=", ">", "<", "="} {
				if strings.Contains(c, cand) {
					parts := strings.SplitN(c, cand, 2)
					if len(parts) == 2 {
						key = strings.TrimSpace(parts[0])
						val = strings.TrimSpace(parts[1])
						op = cand
						break
					}
				}
			}
			fv, ok := m[key]
			if !ok {
				hit = false
				break
			}
			fvs := fmt.Sprintf("%v", fv)
			switch op {
			case "=":
				if fvs != val {
					hit = false
				}
			case ">":
				if !(fvs > val) {
					hit = false
				}
			case "<":
				if !(fvs < val) {
					hit = false
				}
			case ">=":
				if !(fvs >= val) {
					hit = false
				}
			case "<=":
				if !(fvs <= val) {
					hit = false
				}
			}
			if !hit {
				break
			}
		}
		if hit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (t *memTB) WriteRecords(recs []any) error { t.rows = recs; return nil }
func (t *memTB) AppendRecord(rec any) error    { t.rows = append(t.rows, rec); return nil }
func (t *memTB) ReplaceRange(n, m int, recs []any) error {
	L := len(t.rows)
	if n < 0 {
		n += L
	}
	if m < 0 {
		m += L
	}
	if n >= L || n < 0 {
		return nil
	}
	if m >= L {
		m = L - 1
	}
	head := append([]any{}, t.rows[:n]...)
	mid := append([]any{}, recs...)
	tail := []any{}
	if m+1 < L {
		tail = append([]any{}, t.rows[m+1:]...)
	}
	t.rows = append(head, append(mid, tail...)...)
	return nil
}
func (t *memTB) Describe() string {
	return fmt.Sprintf("<table: %s.%s, records: %d>", strings.TrimSuffix(t.name+".db", ".db"), t.name, len(t.rows))
}

// ─── 测试环境 ───

type testEnv struct {
	fs      *memFS
	db      *memDB
	actions []Action
	mu      sync.Mutex
	calls   []string
}

func (e *testEnv) llmAction() Action {
	return Action{
		Name: "LLM",
		Run: func(s *Scope, args string) (string, error) {
			parts, err := SplitArgs(args)
			if err != nil {
				return "", err
			}
			title, _ := s.Interp(parts[0])
			prompt, _ := s.Interp(parts[1])
			e.mu.Lock()
			e.calls = append(e.calls, title+"|"+prompt)
			e.mu.Unlock()
			return "output:" + title, nil
		},
	}
}

func newEnv(seed map[string]string, seedDB map[string]map[string][]any) *testEnv {
	fs := &memFS{files: map[string]string{}}
	for k, v := range seed {
		fs.files[k] = v
	}
	db := &memDB{dbs: map[string]*memDBH{}}
	for dp, tables := range seedDB {
		h := &memDBH{path: dp, tables: map[string]*memTB{}}
		for tn, rows := range tables {
			h.tables[tn] = &memTB{name: tn, rows: rows}
		}
		db.dbs[dp] = h
	}
	e := &testEnv{fs: fs, db: db}
	e.actions = []Action{e.llmAction(), {Name: "SLP", Run: func(s *Scope, args string) (string, error) {
		ms, err := strconv.Atoi(strings.TrimSpace(args))
		if err != nil {
			return "", err
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return "", nil
	}}}
	return e
}

func (e *testEnv) run(t *testing.T, script string) RunResult {
	t.Helper()
	res, err := Run(script, Options{Files: e.fs, DBs: e.db, Actions: e.actions})
	if err != nil {
		t.Fatalf("parse/run error: %v\nscript:\n%s", err, script)
	}
	return res
}

func (e *testEnv) file(p string) string {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	return e.fs.files[p]
}

// ─── 基础语法 ───

func TestLexMultiline(t *testing.T) {
	e := newEnv(nil, nil)
	res := e.run(t, `LLM "分析" "分析：<<<
第一行
第二行
>>> 然后继续"
`)
	want := "分析：\n第一行\n第二行\n然后继续"
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 1 || !strings.HasSuffix(e.calls[0], "|"+want) {
		t.Fatalf("多行文本错误: %v", e.calls)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
}

func TestParseUnknownVerb(t *testing.T) {
	_, err := Parse(`NOPE "x"`, []Action{{Name: "LLM", Run: func(*Scope, string) (string, error) { return "", nil }}})
	if err == nil {
		t.Fatal("未知动作应报错")
	}
}

func TestReadOnlyTargetRejected(t *testing.T) {
	_, err := Parse(`SET "x" => fh.content`, []Action{{Name: "LLM", Run: nil}})
	if err == nil {
		t.Fatal("只读访问器作 SET 目标应报错")
	}
}

// ─── SET 与句柄（11.6）───

func TestSetHandleWriteRead(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET #"out.md" => fh
SET "hello" => fh
SET fh.content => text
SET "world" => fh.eof
SET fh.content => all
`)
	if got := e.file("out.md"); got != "hello\nworld" {
		t.Fatalf("out.md = %q", got)
	}
}

func TestSetTable(t *testing.T) {
	dbSeed := map[string]map[string][]any{
		"state.db": {"logs": []any{map[string]any{"level": "info", "msg": "a"}}},
	}
	e := newEnv(nil, dbSeed)
	e.run(t, `SET @"state.db" => db
SET db.tables.logs.content => logs
SET {"ok": 1} => db.tables.logs.eof
SET db.tables.logs.rows => n
`)
	if n := e.db.OpenDB("state.db").Table("logs").Count(); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
}

// ─── IF 与逻辑组合（5.1 / 11.10）───

func TestIfLogic(t *testing.T) {
	e := newEnv(map[string]string{"data.json": `[{"done":true,"count":3,"status":"ok"},{"done":false,"count":0,"status":"error"},{"done":false,"count":5,"status":"fatal"}]`}, nil)
	e.run(t, `LOOP item=#"data.json".array
IF (item.done == false and item.count > 0) or item.status == "fatal"
   LLM "act" "handle {{item.name}}"
END
END
`)
}

func TestIfExist(t *testing.T) {
	e := newEnv(map[string]string{"cfg.json": "{}"}, nil)
	e.run(t, `IF exist "cfg.json"
   LLM "has" "存在配置文件"
END
IF not exist #"missing.log"
   LLM "none" "缺文件"
END
IF not exist @"nodb.db"
   LLM "nodb" "缺库"
END
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := strings.Join(e.calls, ";")
	if !strings.Contains(got, "存在配置文件") || !strings.Contains(got, "缺文件") || !strings.Contains(got, "缺库") {
		t.Fatalf("exist 判定错误: %v", e.calls)
	}
}

// ─── 断点续跑（9.1 / 12.3）───

func TestBreakpointResume(t *testing.T) {
	seed := map[string]string{"tasks.json": `[{"name":"a","done":false},{"name":"b","done":false},{"name":"c","done":true}]`}
	e := newEnv(seed, nil)
	for round := 0; round < 2; round++ {
		res := e.run(t, `LOOP item=#"tasks.json".array concurrency=3
IF item.done != true
   LLM "编码{{item.name}}" "实现 {{item.name}}" => #"out/{{item.name}}.py"
   SET item.done => true
END
END
`)
		if len(res.Errors) != 0 {
			t.Fatalf("round %d errors: %v", round, res.Errors)
		}
	}
	if e.file("out/a.py") == "" || e.file("out/b.py") == "" {
		t.Fatal("输出文件未生成")
	}
	var after []map[string]any
	if err := json.Unmarshal([]byte(e.file("tasks.json")), &after); err != nil {
		t.Fatal(err)
	}
	for _, it := range after {
		if it["done"] != true {
			t.Fatalf("写回失败: %v", after)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// 两轮只执行 a、b 各一次
	if len(e.calls) != 2 {
		t.Fatalf("calls = %d, want 2（第二轮断点续跑应跳过）", len(e.calls))
	}
}

// ─── BREAK / CONTINUE / EXIT（5.4 / 9.5 / 11.1）───

func TestBreakContinue(t *testing.T) {
	seed := map[string]string{"tasks.json": `[{"name":"a","done":true,"status":"ok"},{"name":"b","done":false,"status":"fatal"},{"name":"c","done":false,"status":"ok"}]`}
	e := newEnv(seed, nil)
	e.run(t, `LOOP item=#"tasks.json".array
IF item.done == true
   CONTINUE
END
LLM "do" "处理 {{item.name}}"
IF item.status == "fatal"
   BREAK
END
END
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 1 || !strings.Contains(e.calls[0], "处理 b") {
		t.Fatalf("BREAK/CONTINUE 错误: %v", e.calls)
	}
}

func TestExitStopsAll(t *testing.T) {
	seed := map[string]string{"tasks.json": `[{"name":"a","status":"fatal"},{"name":"b","status":"ok"}]`}
	e := newEnv(seed, nil)
	e.run(t, `LOOP item=#"tasks.json".array
LLM "check" "检查 {{item.name}}"
IF item.status == "fatal"
   EXIT
END
LLM "after" "处理 {{item.name}}"
END
LLM "tail" "最后的输出"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	var after []string
	for _, c := range e.calls {
		if strings.HasPrefix(c, "after|") || strings.HasPrefix(c, "tail|") {
			after = append(after, c)
		}
	}
	if len(after) != 0 {
		t.Fatalf("EXIT 未终止后续执行: %v", e.calls)
	}
}

// ─── PARALLEL（9.4 / 11.2）───

func TestParallelExitCrossGoroutine(t *testing.T) {
	e := newEnv(map[string]string{
		"a.json": `[{"fatal":true}]`,
		"b.json": `[{"n":1},{"n":2},{"n":3}]`,
	}, nil)
	e.run(t, `PARALLEL
LOOP item=#"a.json".array
   LLM "bra" "快"
   IF item.fatal == true
      EXIT
   END
END
LOOP it=#"b.json".array
   LLM "brb" "慢 {{it.n}}"
   SLP 40
END
END
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	var b2 []string
	for _, c := range e.calls {
		if strings.HasPrefix(c, "brb|") {
			b2 = append(b2, c)
		}
	}
	if len(b2) >= 3 {
		t.Fatalf("EXIT 未跨 goroutine 终止分支 b: %v", e.calls)
	}
	if !strings.Contains(strings.Join(e.calls, ";"), "快") {
		t.Fatalf("分支 a 未执行: %v", e.calls)
	}
}

// ─── 插值截断与句柄描述（2.2 / 6.3）───

func TestInterpTruncateAndHandle(t *testing.T) {
	e := newEnv(map[string]string{"big.log": "line1\nline2"}, nil)
	e.run(t, `SET #"big.log" => fh
SET "0123456789ABCDEF" => s
LLM "t" "头 {{s:5}} 尾"
LLM "d" "文件 {{fh}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	joined := strings.Join(e.calls, "\n")
	if !strings.Contains(joined, "头 01234…[截断] 尾") {
		t.Fatalf("截断错误: %v", e.calls)
	}
	if !strings.Contains(joined, "<file: big.log, size: 11B, lines: 2>") {
		t.Fatalf("句柄描述错误: %v", e.calls)
	}
}

// ─── .range 与越界（11.10）───

func TestLoopRangeCSV(t *testing.T) {
	seed := map[string]string{"data.csv": "name,age\na,1\nb,2"}
	e := newEnv(seed, nil)
	res := e.run(t, `LOOP row=#"data.csv".lines.range(1,-1)
LLM "row" "行 {{row}}"
END
`)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 2 {
		t.Fatalf("range(1,-1) 应跳过表头得到 2 行: %v", e.calls)
	}
}

func TestRangeOutOfBound(t *testing.T) {
	e := newEnv(map[string]string{"x.json": `[1,2]`}, nil)
	res := e.run(t, `LOOP item=#"x.json".array.range(5,9)
LLM "x" "不会到这"
END
`)
	if len(res.Errors) == 0 {
		t.Fatal("range 越界应记错")
	}
}

// ─── 错误不阻塞 ───

func TestActionErrorNotBlocking(t *testing.T) {
	actions := []Action{{Name: "LLM", Run: func(s *Scope, args string) (string, error) {
		parts, _ := SplitArgs(args)
		title, _ := s.Interp(parts[0])
		if title == "bad" {
			return "", fmt.Errorf("模拟失败")
		}
		return title, nil
	}}}
	e := newEnv(nil, nil)
	e.actions = actions
	res := e.run(t, `LLM "bad" "boom"
LLM "good" "ok"
`)
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v", res.Errors)
	}
	if len(res.Summary) != 1 || res.Summary[0] != "good" {
		t.Fatalf("summary = %v", res.Summary)
	}
}

// ─── 数据库循环与写回 ───

func TestTableLoopWriteback(t *testing.T) {
	dbSeed := map[string]map[string][]any{
		"state.db": {"users": []any{
			map[string]any{"name": "u1", "done": false},
			map[string]any{"name": "u2", "done": false},
		}},
	}
	e := newEnv(nil, dbSeed)
	e.run(t, `SET @"state.db" => db
LOOP user=db.tables.users
   LLM "proc" "处理 {{user.name}}"
   SET user.done => true
END
`)
	rows, _ := e.db.OpenDB("state.db").Table("users").Records()
	for _, r := range rows {
		m := r.(map[string]any)
		if m["done"] != true {
			t.Fatalf("表记录写回失败: %v", rows)
		}
	}
}

// ─── 变量捕获后循环（5.2）───

func TestLoopOverCapturedList(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["x","y"] => users
LOOP user=users
LLM "u" "处理 {{user}}"
END
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 2 {
		t.Fatalf("calls=%v", e.calls)
	}
}

// ─── 嵌套 LOOP 不同变量名（5.2）───

func TestNestedLoop(t *testing.T) {
	seed := map[string]string{
		"groups.json": `[{"name":"g1"},{"name":"g2"}]`,
	}
	e := newEnv(seed, nil)
	res := e.run(t, `LOOP group=#"groups.json".array
LOOP task=#"groups.json".array.range(0,0)
   LLM "t" "组 {{group.name}} 任务 {{task.name}}"
END
END
`)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 2 {
		t.Fatalf("嵌套循环应 2 次: %v", e.calls)
	}
}

// ─── 无参 LOOP（5.2）：无界循环、BREAK/EXIT 退出、迭代上限、单步失败终止 ───

// BREAK 退出本层循环（无迭代变量：循环状态由脚本自身的变量承载，赋值回写声明作用域）。
func TestUnboundedLoopBreak(t *testing.T) {
	e := newEnv(nil, nil)
	res := e.run(t, `SET "a" => state
LOOP
LLM "tick" "{{state}}"
IF state == "b"
   BREAK
END
SET "b" => state
END
LLM "after" "done"
`)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	got := strings.Join(e.calls, ",")
	if got != "tick|a,tick|b,after|done" {
		t.Fatalf("BREAK 应在第 2 轮退出: %v", got)
	}
}

// EXIT 终止整个脚本（跨层）。
func TestUnboundedLoopExit(t *testing.T) {
	e := newEnv(nil, nil)
	res := e.run(t, `LOOP
LLM "once" "x"
EXIT
END
LLM "never" "y"
`)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 1 || !strings.Contains(e.calls[0], "once") {
		t.Fatalf("EXIT 应终止整脚本: %v", e.calls)
	}
}

// 到达迭代上限 → 记错并终止（不静默停止）；上限可经 Options.MaxLoopIterations 配置。
func TestUnboundedLoopLimit(t *testing.T) {
	e := newEnv(nil, nil)
	res, err := Run(`LOOP
LLM "spin" "x"
END`, Options{Files: e.fs, DBs: e.db, Actions: e.actions, MaxLoopIterations: 3})
	if err != nil {
		t.Fatalf("parse/run error: %v", err)
	}
	if len(res.Summary) != 3 {
		t.Fatalf("应执行 3 轮（MaxLoopIterations=3）: %v", res.Summary)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Msg, "超过迭代上限 3") {
		t.Fatalf("应记录一次上限错误: %+v", res.Errors)
	}
}

// 单步失败 → 终止本层循环（步骤错误 + 终止说明共 2 条），不空转到上限；
// 循环后的顶层语句照常执行（StopOnError=false 的记错继续语义）。
func TestUnboundedLoopStopsOnStepFailure(t *testing.T) {
	e := newEnv(nil, nil)
	e.actions = append(e.actions, Action{Name: "BOOM", Run: func(s *Scope, args string) (string, error) {
		return "", errors.New("boom")
	}})
	res := e.run(t, `LOOP
BOOM
END
LLM "after" "ok"
`)
	if len(res.Errors) != 2 {
		t.Fatalf("应 2 条错误（步骤失败 + 终止说明）: %+v", res.Errors)
	}
	if !strings.Contains(res.Errors[0].Msg, "boom") {
		t.Fatalf("首条应为步骤错误: %+v", res.Errors[0])
	}
	if !strings.Contains(res.Errors[1].Msg, "终止本层循环") {
		t.Fatalf("次条应为终止说明: %+v", res.Errors[1])
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 1 || !strings.Contains(e.calls[0], "after") {
		t.Fatalf("循环后顶层语句应执行: %v", e.calls)
	}
}

// 无参 LOOP 不得携带 concurrency（否则会被误读为「变量名 concurrency = 数据源」）。
func TestUnboundedLoopRejectsConcurrency(t *testing.T) {
	_, err := ParseScript("LOOP concurrency=3\nEND\n")
	if err == nil || !strings.Contains(err.Error(), "不支持 concurrency") {
		t.Fatalf("应拒绝 concurrency: %v", err)
	}
}

// ─── Raw 动作 => 目标重定向（动作级输出统一写入，空输出也写）───

func TestRawActionRedirectOutput(t *testing.T) {
	fs := &memFS{files: map[string]string{}}
	var gotArgs []string
	actions := []Action{{
		Name: "WIN",
		Raw:  true,
		Run: func(s *Scope, args string) (string, error) {
			gotArgs = append(gotArgs, args)
			return "Desktop: Windows-10\nactive", nil
		},
	}}
	res, err := Run(`WIN list => #"wins.txt"`, Options{Files: fs, Actions: actions})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "list" {
		t.Fatalf("action args = %v, want [list]（不携带 => 目标）", gotArgs)
	}
	fs.mu.Lock()
	got := fs.files["wins.txt"]
	fs.mu.Unlock()
	if got != "Desktop: Windows-10\nactive" {
		t.Fatalf("wins.txt = %q", got)
	}
	if len(res.Summary) != 0 {
		t.Fatalf("重定向后输出不应进 Summary: %v", res.Summary)
	}
}

func TestRawActionRedirectEmptyOutput(t *testing.T) {
	fs := &memFS{files: map[string]string{}}
	var gotArgs []string
	actions := []Action{{
		Name: "KPR",
		Raw:  true,
		Run: func(s *Scope, args string) (string, error) {
			gotArgs = append(gotArgs, args)
			return "", nil
		},
	}}
	res, err := Run(`KPR ctrl+s => #"press.txt"`, Options{Files: fs, Actions: actions})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	fs.mu.Lock()
	got, exists := fs.files["press.txt"]
	fs.mu.Unlock()
	if !exists {
		t.Fatal("空输出也应重定向创建文件 press.txt")
	}
	if got != "" {
		t.Fatalf("press.txt = %q, want 空串", got)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "ctrl+s" {
		t.Fatalf("action args = %v, want [ctrl+s]", gotArgs)
	}
	if len(res.Summary) != 0 {
		t.Fatalf("空输出无重定向不应进 Summary: %v", res.Summary)
	}
}

func TestRawActionArgsQuotedArrowNotSplit(t *testing.T) {
	fs := &memFS{files: map[string]string{}}
	var gotArgs string
	actions := []Action{{
		Name: "TYP",
		Raw:  true,
		Run: func(s *Scope, args string) (string, error) {
			gotArgs = args
			return "", nil
		},
	}}
	if _, err := Run(`TYP "save => done"`, Options{Files: fs, Actions: actions}); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if gotArgs != `"save => done"` {
		t.Fatalf("引号内 => 不应被分离: %q", gotArgs)
	}
}

// ─── 增强草案测试：TYPEOF / ENTRY / SPLIT / JOIN / PUSH / 下标 ───

// TYPEOF 基本类型
func TestTypeofBasic(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `TYPEOF "hello" => t1
TYPEOF 42 => t2
TYPEOF true => t3
TYPEOF null => t4
TYPEOF [1,2,3] => t5
TYPEOF {"a":1} => t6
LLM "check" "{{t1}}|{{t2}}|{{t3}}|{{t4}}|{{t5}}|{{t6}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "string|number|bool|null|list|object") {
		t.Fatalf("TYPEOF 结果错误: %v", got)
	}
}

func TestTypeofUndefined(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `TYPEOF undefinedVar => t
LLM "chk" "{{t}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "null") {
		t.Fatalf("未定义变量应为 null: %v", got)
	}
}

// ENTRY 对象 → KV 数组
func TestEntryBasic(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET {"a":"1","b":"2"} => cfg
ENTRY cfg => entries
LLM "chk" "{{entries}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, `[{"key":"a","value":"1"},{"key":"b","value":"2"}]`) &&
		!strings.Contains(got, `[{"key":"b","value":"2"},{"key":"a","value":"1"}]`) {
		t.Fatalf("ENTRY 结果错误: %v", got)
	}
}

func TestEntryLOOP(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET {"x":"10","y":"20"} => cfg
ENTRY cfg => entries
LOOP kv = entries
   LLM "kv" "{{kv.key}}={{kv.value}}"
END
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 2 {
		t.Fatalf("ENTRY LOOP 应迭代 2 次: %v", e.calls)
	}
}

// SPLIT 字符串拆分
func TestSplitBasic(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SPLIT "a,b,c", "," => parts
LLM "chk" "{{parts}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, `["a","b","c"]`) {
		t.Fatalf("SPLIT 结果错误: %v", got)
	}
}

func TestSplitEmptyField(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SPLIT "a,,b", "," => parts
LLM "chk" "{{parts}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, `["a","","b"]`) {
		t.Fatalf("SPLIT 空段错误: %v", got)
	}
}

func TestSplitLOOP(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SPLIT "x,y,z", "," => cols
LOOP c = cols
   LLM "col" "{{c}}"
END
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) != 3 {
		t.Fatalf("SPLIT LOOP 应迭代 3 次: %v", e.calls)
	}
}

// JOIN 数组拼接
func TestJoinBasic(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["a","b","c"] => list
JOIN list, "," => text
LLM "chk" "{{text}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "a,b,c") {
		t.Fatalf("JOIN 结果错误: %v", got)
	}
}

func TestJoinNewline(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["a","b"] => rows
JOIN rows, "\n" => text
LLM "chk" "{{text}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "a\nb") {
		t.Fatalf("JOIN \\n 错误: %v", got)
	}
}

func TestJoinEmpty(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET [] => empty
JOIN empty, "," => text
LLM "chk" "{{text}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	// 空数组 JOIN 应为空字符串
	parts := strings.SplitN(got, "|", 2)
	if len(parts) < 2 || parts[1] != "" {
		t.Fatalf("JOIN 空数组错误: %v", got)
	}
}

func TestJoinWithSplit(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SPLIT "a,b,c", "," => parts
JOIN parts, "," => text
LLM "chk" "{{text}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "a,b,c") {
		t.Fatalf("SPLIT+JOIN 互逆错误: %v", got)
	}
}

// PUSH 追加
func TestPushBasic(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET [] => list
PUSH "a" => list
PUSH "b" => list
LLM "chk" "{{list}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, `["a","b"]`) {
		t.Fatalf("PUSH 结果错误: %v", got)
	}
}

func TestPushPushToFile(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET [] => rows
LOOP item = ["a","b","c"]
   PUSH item => rows
END
SET rows => #"out.json"
`)
	got := e.file("out.json")
	if got != `["a","b","c"]` {
		t.Fatalf("PUSH+SET 文件错误: %v", got)
	}
}

// 下标读
func TestSubscriptRead(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["a","b","c"] => list
SET list[0] => first
SET list[2] => last
LLM "chk" "{{first}}|{{last}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "a|c") {
		t.Fatalf("下标读错误: %v", got)
	}
}

func TestSubscriptNegative(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["x","y","z"] => list
SET list[-1] => last
LLM "chk" "{{last}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "z") {
		t.Fatalf("负数下标错误: %v", got)
	}
}

func TestSubscriptVariableIndex(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["a","b","c"] => list
SET 1 => idx
SET list[idx] => val
LLM "chk" "{{val}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "b") {
		t.Fatalf("变量下标错误: %v", got)
	}
}

// 下标写
func TestSubscriptWrite(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["a","b","c"] => list
SET "x" => list[1]
LLM "chk" "{{list}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, `["a","x","c"]`) {
		t.Fatalf("下标写错误: %v", got)
	}
}

func TestSubscriptWriteNegative(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["a","b","c"] => list
SET "z" => list[-1]
LLM "chk" "{{list}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, `["a","b","z"]`) {
		t.Fatalf("负数下标写错误: %v", got)
	}
}

// 下标 + JSON 数组字面量不冲突
func TestSubscriptVsJSONLiteral(t *testing.T) {
	e := newEnv(nil, nil)
	e.run(t, `SET ["a","b"] => list
SET [3,4] => arr
SET list[0] => first
SET arr[0] => first2
LLM "chk" "{{first}}|{{first2}}"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	got := e.calls[len(e.calls)-1]
	if !strings.Contains(got, "a|3") {
		t.Fatalf("下标与 JSON 字面量冲突: %v", got)
	}
}

// 下标须在同一行内闭合：跨行下标显式报错
// （旧实现跨行推进游标却沿用首行偏移：`[` 后首行偏移=9，次行更长 → 静默取 ln2[9:10]="9" 产出错误 token 流）。
func TestSubscriptCrossLineRejected(t *testing.T) {
	_, err := Parse("SET list[\n0123456789] => x\n", nil)
	if err == nil {
		t.Fatal("跨行下标应报错")
	}
}

// 大组合：SPLIT CSV → 下标改列 → JOIN 回行 → 收集
func TestSplitSubscriptJoinPushFull(t *testing.T) {
	e := newEnv(map[string]string{"data.csv": "a,1,x\nb,2,y\nc,3,z"}, nil)
	e.run(t, `SET [] => out
LOOP row = #"data.csv".lines
   LLM "debug" "{{row}}"
   SPLIT row, "," => cols
   LLM "debug2" "{{cols}}"
   SET "fixed" => cols[2]
   LLM "debug3" "{{cols}}"
   JOIN cols, "," => line
   LLM "debug4" "{{line}}"
   PUSH line => out
END
JOIN out, "\n" => result
SET result => #"result.csv"
`)
	e.mu.Lock()
	defer e.mu.Unlock()
	t.Logf("calls: %v", e.calls)
	got := e.file("result.csv")
	want := "a,1,fixed\nb,2,fixed\nc,3,fixed"
	if got != want {
		t.Fatalf("完整组合错误:\ngot:  %q\nwant: %q", got, want)
	}
}
