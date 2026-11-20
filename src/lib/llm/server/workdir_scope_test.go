package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	chonkpilotdata "github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	wslayout "github.com/chonkpilot/chonkpilot-workspace/layout"
)

// TestOneWindowTwoWorkDirs：同一窗口实例（ins-test）同时跑两个 workdir ——
// 显式 work_dir 的轮次只能读写该项目库，默认项目库不被污染；完成事件带项目作用域。
//
// 覆盖：项目注册表信任边界（未注册 workdir 拒绝）→ 注册 B → B 起轮/收尾 →
// A/B 两个会话库互不可见。
func TestOneWindowTwoWorkDirs(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	wdB := t.TempDir()
	// 测试夹具经 persist.New 注入 UsrPath → SetDataHome 把 prjusr 根覆盖成单一目录，
	// 会掩盖「数据随项目目录走」的隔离语义。本用例临时恢复默认布局，结束后还原。
	prevRoot := filepath.Dir(filepath.Dir(chonkpilotdata.PrjUsrPath(testWorkDir, "")))
	wslayout.SetUsrRootOverride("")
	t.Cleanup(func() { wslayout.SetUsrRootOverride(prevRoot) })
	// 未注册 → 显式 work_dir 必须拒绝（不接受任意绝对路径）。
	v := emitWait(t, s.bus, "session-start", jb(map[string]any{
		"req_id": "r-unreg", "instance_id": "ins-test", "session": "s-unreg", "turn": "t-unreg",
		"work_dir": wdB,
	}))
	if res, _ := v.Result.(map[string]any); res == nil || res["accepted"] == true {
		t.Fatalf("未注册 workdir 应拒绝，实际 %+v", v.Result)
	}

	// 注册 B（等价 gui.project.add → workdir-opened）→ 数据层登记显式项目根。
	s.bus.Publish("workdir-opened", jb(map[string]any{
		"work_dir": wdB, "data_dir": "",
	}))

	// 已注册 workdir + 不匹配的 data_dir → 仍拒绝（数据根不可由请求改写）。
	v = emitWait(t, s.bus, "session-start", jb(map[string]any{
		"req_id": "r-datadir", "instance_id": "ins-test", "session": "s-dd", "turn": "t-dd",
		"work_dir": wdB, "data_dir": t.TempDir(),
	}))
	if res, _ := v.Result.(map[string]any); res == nil || res["accepted"] == true {
		t.Fatalf("data_dir 与注册表不一致应拒绝，实际 %+v", v.Result)
	}

	// B 的会话（显式 scope 建会话；dialog 自举）。
	storeB := newSessionStore(s.bus, "ins-test", facade.Scope{WorkDir: wdB})
	if err := storeB.EnsureSession("s-b"); err != nil {
		t.Fatalf("EnsureSession(B): %v", err)
	}

	// B 起轮 + 发送，直到终态。
	v = emitWait(t, s.bus, "session-start", jb(map[string]any{
		"req_id": "r-b", "instance_id": "ins-test", "session": "s-b", "turn": "t-b",
		"work_dir": wdB,
	}))
	if res, _ := v.Result.(map[string]any); res == nil || res["accepted"] != true {
		t.Fatalf("B 起轮应受理，实际 %+v", v.Result)
	}
	s.bus.Publish("session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-b", "turn": "t-b",
		"type": "text-user", "content": "hello B",
	}))
	evs := collectTurn(t, s.bus, "t-b", 10*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("B 轮次未正常完成: %+v", evs)
	}
	if got := strings.TrimSpace(str(c["work_dir"])); !strings.EqualFold(got, wdB) {
		t.Fatalf("llm-complete 应带 B 的 work_dir=%q，实际 %q", wdB, got)
	}

	// A（实例默认项目）看不到 B 会话；B 能看到自己的会话。
	listA, err := s.cfg.SessionList(facade.SessionListRequest{InstanceID: "ins-test"})
	if err != nil {
		t.Fatalf("SessionList(A): %v", err)
	}
	for _, it := range listA.List {
		if it.ID == "s-b" {
			t.Fatalf("A 项目库不应出现 B 会话: %+v", listA.List)
		}
	}
	listB, err := s.cfg.SessionList(facade.SessionListRequest{
		InstanceID: "ins-test", Scope: facade.Scope{WorkDir: wdB},
	})
	if err != nil {
		t.Fatalf("SessionList(B): %v", err)
	}
	found := false
	for _, it := range listB.List {
		if it.ID == "s-b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("B 项目库应包含 B 会话: %+v", listB.List)
	}
}
