package vfts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newManagePlugin 构造只用于管理面单测的插件：不 spawn 引擎，engineCallFn 注入假引擎应答。
func newManagePlugin(debounce time.Duration, call func(ctx context.Context, name string, args map[string]any) (string, error)) *Vfts {
	p := New(Options{Exe: "dummy-not-spawned.exe", RebuildDebounce: debounce})
	p.logf = func(string, ...any) {}
	p.engineCallFn = call
	return p
}

// stopTimers 停掉去抖器全部未触发定时器并清空（测试收尾，避免后台重建干扰）。
func stopTimers(p *Vfts) {
	p.rebuild.mu.Lock()
	defer p.rebuild.mu.Unlock()
	for _, tm := range p.rebuild.timers {
		tm.Stop()
	}
	p.rebuild.timers = map[string]*time.Timer{}
}

// pending 返回当前已登记（未触发）的重建目标集合。
func pending(p *Vfts) map[string]bool {
	p.rebuild.mu.Lock()
	defer p.rebuild.mu.Unlock()
	out := map[string]bool{}
	for k := range p.rebuild.timers {
		out[k] = true
	}
	return out
}

// TestDictHandlers：vfts.dict.get / vfts.dict.set 直调引擎并写回 v.Result（载荷透传）。
func TestDictHandlers(t *testing.T) {
	var gotArgs map[string]any
	// 去抖窗口 1h：dict.set 触发的重建不真正执行（本用例只断言回执与载荷透传）。
	p := newManagePlugin(time.Hour, func(_ context.Context, name string, args map[string]any) (string, error) {
		switch name {
		case "vfts_dict_get":
			return `{"dict_dir":"C:/d","user_dict_path":"C:/d/user_dict.txt","user_dict":"朝彻","word_count":1,"base_dicts":["a.utf8","b.utf8"]}`, nil
		case "vfts_dict_set":
			gotArgs = args
			return `{"ok":true,"word_count":2}`, nil
		}
		return "", nil
	})
	defer stopTimers(p)
	p.works["/wd"] = &workRec{workDir: "/wd", refs: 1, enabled: true}

	g := &mq.Value{Payload: []byte(`{"instance_id":"i1"}`)}
	if err := p.onDictGet(context.Background(), topicDictGet, g); err != nil {
		t.Fatalf("onDictGet: %v", err)
	}
	gm, _ := g.Result.(map[string]any)
	if gm["dict_dir"] != "C:/d" || gm["word_count"] != float64(1) {
		t.Fatalf("dict.get 回执不符：%+v", gm)
	}

	s := &mq.Value{Payload: []byte(`{"instance_id":"i1","user_dict":"朝彻\nChonkPilot\n"}`)}
	if err := p.onDictSet(context.Background(), topicDictSet, s); err != nil {
		t.Fatalf("onDictSet: %v", err)
	}
	sm, _ := s.Result.(map[string]any)
	if sm["ok"] != true || sm["word_count"] != float64(2) {
		t.Fatalf("dict.set 回执不符：%+v", sm)
	}
	if gotArgs["user_dict"] != "朝彻\nChonkPilot\n" {
		t.Fatalf("dict.set 未透传 user_dict：%+v", gotArgs)
	}
	// 系统级词典变更 → 对启用中的 workdir 调度一次重建。
	if !pending(p)["/wd"] {
		t.Fatalf("dict.set 应调度 /wd 重建，实际：%v", pending(p))
	}
}

// TestReindexScopedToInstance：vfts.reindex 按调用实例定位 workdir，命中后立即回执
// {ok,started} 且**只**对该 workdir 调度重建（不波及同启用的其它 workdir）。
func TestReindexScopedToInstance(t *testing.T) {
	p := newManagePlugin(time.Hour, nil)
	defer stopTimers(p)
	p.works["/wd1"] = &workRec{workDir: "/wd1", refs: 1, enabled: true}
	p.works["/wd2"] = &workRec{workDir: "/wd2", refs: 1, enabled: true}
	p.insts["i1"] = &instRec{workdir: "/wd1"}

	v := &mq.Value{Payload: []byte(`{"instance_id":"i1"}`)}
	if err := p.onReindex(context.Background(), topicReindex, v); err != nil {
		t.Fatalf("onReindex: %v", err)
	}
	res, _ := v.Result.(map[string]any)
	if res["ok"] != true || res["started"] != true {
		t.Fatalf("reindex 回执不符：%+v", res)
	}
	got := pending(p)
	if !got["/wd1"] {
		t.Fatalf("应按实例定位到 /wd1 触发重建，实际：%v", got)
	}
	if got["/wd2"] {
		t.Fatalf("不应重建非本实例 workdir：%v", got)
	}
}

// TestScheduleRebuildAllTargetsEnabledOnly：scheduleRebuildAll 只对「引用中且启用」的 workdir
// 调度（排序稳定），停用/无引用者不调度。
func TestScheduleRebuildAllTargetsEnabledOnly(t *testing.T) {
	p := newManagePlugin(time.Hour, nil)
	defer stopTimers(p)
	p.works["/wd-b"] = &workRec{workDir: "/wd-b", refs: 1, enabled: true}
	p.works["/wd-a"] = &workRec{workDir: "/wd-a", refs: 2, enabled: true}
	p.works["/wd-off"] = &workRec{workDir: "/wd-off", refs: 1, enabled: false}
	p.works["/wd-idle"] = &workRec{workDir: "/wd-idle", refs: 0, enabled: true}

	got := p.scheduleRebuildAll("test")
	want := []string{"/wd-a", "/wd-b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("调度目标不符：got=%v want=%v", got, want)
	}
	if g := pending(p); !g["/wd-a"] || !g["/wd-b"] || len(g) != 2 {
		t.Fatalf("应仅登记启用中的 workdir：%v", g)
	}
}
