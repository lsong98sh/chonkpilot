// runner_test.go — 进程内协议回环测试：把 runProtocol 当纯函数，用 io.Pipe 驱动 stdio。
// 覆盖：含 FILE_* 与 LLM 的脚本 → 1 条 llm_call → 注入 llm_result → 收到 result；$RETURN
// inline/file 两态；三域动词前缀消歧。**不启动 Chrome、不执行桌面操作**（脚本只含 FILE_*/LLM）。
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/browser"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/desktop"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
)

// harness 驱动一次 runProtocol（in/out 用管道模拟 gateway 的 stdin/stdout）。
type harness struct {
	t     *testing.T
	pw    *io.PipeWriter
	outR  *io.PipeReader
	lines *bufio.Scanner
	done  chan error
}

func startProtocol(t *testing.T) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runProtocol(inR, outW) }()
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	return &harness{t: t, pw: inW, outR: outR, lines: sc, done: done}
}

// writeIn 向下行（stdin）发一条消息。
func (h *harness) writeIn(v any) {
	h.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	b = append(b, '\n')
	ch := make(chan error, 1)
	go func() { _, werr := h.pw.Write(b); ch <- werr }()
	select {
	case werr := <-ch:
		if werr != nil {
			h.t.Fatalf("写 stdin 失败：%v", werr)
		}
	case <-time.After(5 * time.Second):
		h.t.Fatal("写 stdin 超时")
	}
}

// readLine 读一条上行（stdout）协议行并解码。
func (h *harness) readLine() map[string]any {
	h.t.Helper()
	type result struct {
		m   map[string]any
		err error
	}
	ch := make(chan result, 1)
	go func() {
		if !h.lines.Scan() {
			ch <- result{err: h.lines.Err()}
			return
		}
		var m map[string]any
		if err := json.Unmarshal(h.lines.Bytes(), &m); err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{m: m}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			h.t.Fatalf("读 stdout 失败：%v", r.err)
		}
		return r.m
	case <-time.After(5 * time.Second):
		h.t.Fatal("读 stdout 超时")
		return nil
	}
}

// close 收尾：关管道并等 runProtocol 返回。
func (h *harness) close() {
	h.pw.Close()
	h.outR.Close()
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatalf("runProtocol 返回错误：%v", err)
		}
	case <-time.After(5 * time.Second):
		h.t.Fatal("runProtocol 未在 5s 内返回")
	}
}

// readUntil 顺序读协议行，直到出现 t==want（返回该行）；途中读到的 tree/step/log 一并返回
// （供调用方断言），未命中则 Fatal。
func (h *harness) readUntil(want string) (map[string]any, []map[string]any) {
	h.t.Helper()
	var seen []map[string]any
	for i := 0; i < 50; i++ {
		m := h.readLine()
		if m["t"] == want {
			return m, seen
		}
		seen = append(seen, m)
	}
	h.t.Fatalf("未读到 %q 协议行", want)
	return nil, nil
}

// TestRunProtocolFileAndLLMInlineReturn：FILE_* + LLM + `=> $RETURN`（inline 态）全链路。
func TestRunProtocolFileAndLLMInlineReturn(t *testing.T) {
	dir := t.TempDir()
	work := filepath.ToSlash(dir)
	target := filepath.ToSlash(filepath.Join(dir, "a.txt"))
	retFile := filepath.ToSlash(filepath.Join(dir, "ret.md"))

	script := "FILE_INS #\"" + target + "\" \"hello world\"\n" +
		"FILE_RPL #\"" + target + "\" \"world\" \"there\"\n" +
		"LLM \"agent1\" \"go {{env.CHONKPILOT_WORKDIR}}\" \"生成一句问候\" => $RETURN\n"

	h := startProtocol(t)
	h.writeIn(map[string]any{
		"t": "run", "job": "job1", "script": script, "instance": "inst1",
		"work_dir": work, "data_dir": work, "return_file": retFile,
	})

	call, pre := h.readUntil("llm_call")
	if call["agent"] != "agent1" {
		t.Fatalf("agent = %v", call["agent"])
	}
	if call["prompt"] != "go "+work {
		t.Fatalf("prompt 插值 {{env.CHONKPILOT_WORKDIR}} 失败：%v", call["prompt"])
	}
	if call["purpose"] != "生成一句问候" {
		t.Fatalf("purpose = %v", call["purpose"])
	}
	if call["session"] != "job1-1" {
		t.Fatalf("session = %v, want job1-1", call["session"])
	}
	// 契约：LLM 步骤前必发 step{running}（无容器脚本不发 tree）。
	if len(pre) != 1 || pre[0]["t"] != "step" || pre[0]["status"] != "running" {
		t.Fatalf("llm_call 前应恰有一条 step{running}：%v", pre)
	}

	h.writeIn(map[string]any{"t": "llm_result", "call": call["call"], "text": "你好"})

	// llm_call 之后：step{done} → result。
	res, mid := h.readUntil("result")
	if len(mid) != 1 || mid[0]["t"] != "step" || mid[0]["status"] != "done" {
		t.Fatalf("result 前应恰有一条 step{done}：%v", mid)
	}
	if res["ok"] != true {
		t.Fatalf("ok = %v, err = %v", res["ok"], res["error"])
	}
	if res["text"] != "你好" || res["overflow"] != false || res["return_used"] != true {
		t.Fatalf("$RETURN inline 态不符：text=%v overflow=%v return_used=%v", res["text"], res["overflow"], res["return_used"])
	}

	if b, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(b) != "hello there" {
		t.Fatalf("FILE_* 未生效：%q err=%v", b, err)
	}
	h.close()
}

// TestRunProtocolTreeAndSteps：LOOP 容器 → 先发 tree（静态容器）+ 每次迭代 step{running/done}
// 且**不新增容器节点**（同 id 更新 loop_current），$RETURN 未用 → return_used=false。
func TestRunProtocolTreeAndSteps(t *testing.T) {
	dir := t.TempDir()
	work := filepath.ToSlash(dir)
	items := filepath.ToSlash(filepath.Join(dir, "items.json"))

	script := "FILE_INS #\"" + items + "\" \"[{\\\"n\\\":1},{\\\"n\\\":2}]\"\n" +
		"LOOP item=#\"" + items + "\".array\n" +
		"  LLM \"agent1\" \"处理 {{item.n}}\" \"处理一项\"\n" +
		"END\n"

	h := startProtocol(t)
	h.writeIn(map[string]any{
		"t": "run", "job": "job3", "script": script, "instance": "inst1",
		"work_dir": work, "data_dir": work, "return_file": filepath.ToSlash(filepath.Join(dir, "ret.md")),
	})

	// 首行必为 tree（LOOP 容器，loop_current 0）。
	tree0 := h.readLine()
	if tree0["t"] != "tree" {
		t.Fatalf("首行应为 tree，实际 %v", tree0)
	}
	nodes0, _ := tree0["nodes"].([]any)
	if len(nodes0) != 1 {
		t.Fatalf("tree 应含 1 个容器节点：%v", nodes0)
	}
	n0, _ := nodes0[0].(map[string]any)
	if n0["kind"] != "dsl_loop" || n0["label"] != "LOOP item" {
		t.Fatalf("容器节点字段不符：%v", n0)
	}
	containerID := n0["id"].(string)

	// 首步：tree(loop_current=1) → step{running} → llm_call。
	call1, pre1 := h.readUntil("llm_call")
	if len(pre1) != 2 || pre1[0]["t"] != "tree" {
		t.Fatalf("首步应先发 tree：%v", pre1)
	}
	step1 := pre1[1]
	if step1["t"] != "step" || step1["status"] != "running" || step1["statement"] == "" {
		t.Fatalf("step{running} 字段不符：%v", step1)
	}
	h.writeIn(map[string]any{"t": "llm_result", "call": call1["call"], "text": "a"})

	// 次步：step{done}(no=1) → tree(loop_current=2，仍**同 1 个节点**) → step{running}(no=2) → llm_call。
	call2, pre2 := h.readUntil("llm_call")
	if len(pre2) != 3 || pre2[0]["status"] != "done" {
		t.Fatalf("第 1 步应收尾 step{done}：%v", pre2)
	}
	tree2 := pre2[1]
	if tree2["t"] != "tree" {
		t.Fatalf("次步应先发 tree：%v", pre2)
	}
	nodes2, _ := tree2["nodes"].([]any)
	if len(nodes2) != 1 {
		t.Fatalf("容器不得随迭代新增：%v", nodes2)
	}
	n2, _ := nodes2[0].(map[string]any)
	if n2["id"] != containerID || n2["loop_current"] != float64(2) {
		t.Fatalf("容器 loop_current 应更新为 2 且 id 不变：%v", n2)
	}
	if pre2[2]["t"] != "step" || pre2[2]["status"] != "running" || pre2[2]["no"] != float64(2) {
		t.Fatalf("第 2 步 step{running} 不符：%v", pre2[2])
	}
	h.writeIn(map[string]any{"t": "llm_result", "call": call2["call"], "text": "b"})

	res, tail := h.readUntil("result")
	if len(tail) != 1 || tail[0]["t"] != "step" || tail[0]["status"] != "done" || tail[0]["no"] != float64(2) {
		t.Fatalf("第 2 步应收尾 step{done}：%v", tail)
	}
	if res["return_used"] != false {
		t.Fatalf("未用 $RETURN → return_used 应 false：%v", res["return_used"])
	}
	h.close()
}

// TestRunProtocolReturnFileState：累计超 64K → $RETURN 转 file 态（回文件名 + 大小）。
func TestRunProtocolReturnFileState(t *testing.T) {
	dir := t.TempDir()
	work := filepath.ToSlash(dir)
	retFile := filepath.ToSlash(filepath.Join(dir, "ret.md"))

	chunk := strings.Repeat("x", 1000)
	var b strings.Builder
	const n = 70 // 70*1000 + 69 分隔符 = 70069 > 65536
	for i := 0; i < n; i++ {
		b.WriteString("SET \"" + chunk + "\" => $RETURN\n")
	}

	h := startProtocol(t)
	h.writeIn(map[string]any{
		"t": "run", "job": "job2", "script": b.String(), "instance": "inst1",
		"work_dir": work, "data_dir": work, "return_file": retFile,
	})

	res := h.readLine()
	if res["t"] != "result" || res["ok"] != true {
		t.Fatalf("result/ok 不符：%v", res)
	}
	if res["overflow"] != true {
		t.Fatalf("overflow = %v, want true", res["overflow"])
	}
	if res["file"] != retFile {
		t.Fatalf("file = %v, want %v", res["file"], retFile)
	}
	want := n*1000 + (n - 1)
	if got := int(res["size"].(float64)); got != want {
		t.Fatalf("size = %d, want %d", got, want)
	}
	if res["text"] != "" {
		t.Fatalf("file 态 text 应为空：%q", res["text"])
	}
	if data, err := os.ReadFile(filepath.Join(dir, "ret.md")); err != nil || len(data) != want {
		t.Fatalf("落盘长度不符：len=%d err=%v", len(data), err)
	}
	h.close()
}

// TestPrefixed 校验动词前缀化：改名不影响原切片（值类型复制）。
func TestPrefixed(t *testing.T) {
	orig := []dsl.Action{{Name: "RPL"}, {Name: "CLK"}}
	got := prefixed("FILE_", orig)
	if got[0].Name != "FILE_RPL" || got[1].Name != "FILE_CLK" {
		t.Fatalf("前缀化失败：%v", []string{got[0].Name, got[1].Name})
	}
	if orig[0].Name != "RPL" {
		t.Fatal("原动作切片不应被修改")
	}
}

// TestMergeActionsPrefixDisambiguation：三域合并后动词全覆盖且零重名（MOV/CLK 等跨域同名已消歧）。
func TestMergeActionsPrefixDisambiguation(t *testing.T) {
	f, err := fileops.NewSession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w, err := browser.NewSession(browser.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	p, err := desktop.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	seen := map[string]bool{}
	for _, a := range mergeActions(f, w, p) {
		if seen[a.Name] {
			t.Fatalf("动词重名未消歧：%s", a.Name)
		}
		seen[a.Name] = true
	}
	for _, want := range []string{"FILE_MOV", "FILE_RPL", "WEB_CLK", "WEB_OPN", "PC_CLK", "PC_MOV"} {
		if !seen[want] {
			t.Fatalf("缺少前缀化动词 %s", want)
		}
	}
}
