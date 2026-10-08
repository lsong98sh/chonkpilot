// pair 级启动清理单测（35-错误处理与恢复 附录 S2/S3）：`data-session-cleanup-stale` 除把遗留
// running 轮次标 interrupted 外，还把**残留的非终态 tool_pair**（tool_call_status ∈
// pending/running/provisional）标 interrupted（状态列 + content.result.status 同源），已完成 pair 不动。
//
// 外部模块黑盒（package persist_test）：经 persist.New/Start + 总线 data-session-cleanup-stale 驱动；
// 落库断言经直连 prj 库（regInstance 已 data.Register）。
package persist_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// TestSessionCleanupStaleToolPairs：遗留非终态 tool_pair → cleanup-stale 标 interrupted。
func TestSessionCleanupStaleToolPairs(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	prj, _ := data.PrjUsr("ins-test")
	mb := prj.Table("messages")

	// 直连种子：三种非终态 + 一个已完成 pair（有/无 result 段各覆盖）。
	seed := func(key, status string, withResult bool) {
		tc := map[string]any{
			"call": map[string]any{"tool_call_id": "tc-" + status, "name": "core_file_read"},
		}
		if withResult {
			tc["result"] = map[string]any{"content": "部分结果", "status": status}
		}
		b, _ := json.Marshal(tc)
		if err := mb.Upsert(key, data.Record{
			"turn_id": "t-pair", "role": "tool", "tool_call_id": "tc-" + status,
			"tool_call_status": status, "content": string(b),
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("m-run", "running", true)
	seed("m-prov", "provisional", false)
	seed("m-pend", "pending", false)
	seed("m-done", "completed", true)

	dataCall(t, bus, "data-session-cleanup-stale", map[string]any{"instance_id": "ins-test"})

	// 三个非终态 → interrupted（状态列）
	for _, k := range []string{"m-run", "m-prov", "m-pend"} {
		var rec data.Record
		if ok, _ := mb.Get(k, &rec); !ok {
			t.Fatalf("%s missing", k)
		}
		if got := persist.Sval(rec["tool_call_status"]); got != "interrupted" {
			t.Fatalf("%s tool_call_status=%q, want interrupted", k, got)
		}
	}
	// 有 result 段的 pair：content.result.status 同源更新（视图优先读该字段）
	var rr data.Record
	mb.Get("m-run", &rr)
	if c := persist.Sval(rr["content"]); !strings.Contains(c, `"status":"interrupted"`) {
		t.Fatalf("m-run content.result.status 未同步：%s", c)
	}
	// 历史文本不改（result.content 原样）
	if c := persist.Sval(rr["content"]); !strings.Contains(c, "部分结果") {
		t.Fatalf("m-run result.content 被改写：%s", c)
	}
	// 已完成 pair 不受影响
	var rd data.Record
	mb.Get("m-done", &rd)
	if got := persist.Sval(rd["tool_call_status"]); got != "completed" {
		t.Fatalf("m-done 被误改：%q", got)
	}
}
