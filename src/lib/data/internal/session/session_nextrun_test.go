// `next_run` 计数器（`sessions.next_run_count`）门面单测（NEXTRUN-1 §3.3.2）。
//
// 口径：上限判定 + 自增单事务原子；0 = 关闭（不增）；重置 / 补偿 -1；仅 > 0 才写键。
package session

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

func TestSessionNextRunCounter(t *testing.T) {
	s, scope := newSessionEnvBus(t) // SessionEnsure 首次落库会广播 session-new（需总线）
	const inst, sid = "ins-nextrun", "s-nextrun"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	if _, err := s.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: inst, SessionID: sid, Scope: scope,
	}); err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}

	add := func(max int) facade.SessionNextRunCheckAndAddResponse {
		t.Helper()
		res, err := s.SessionNextRunCheckAndAdd(facade.SessionNextRunCheckAndAddRequest{
			InstanceID: inst, SessionID: sid, Max: max, Scope: scope,
		})
		if err != nil {
			t.Fatalf("CheckAndAdd(max=%d): %v", max, err)
		}
		return res
	}
	get := func() int {
		t.Helper()
		res, err := s.SessionNextRunGet(facade.SessionNextRunGetRequest{
			InstanceID: inst, SessionID: sid, Scope: scope,
		})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return res.Count
	}

	// 上限 2：前两次受理并逐次 +1，第三次拒绝且不增。
	if res := add(2); !res.Allowed || res.Count != 1 {
		t.Fatalf("第 1 次应受理 count=1：%+v", res)
	}
	if res := add(2); !res.Allowed || res.Count != 2 {
		t.Fatalf("第 2 次应受理 count=2：%+v", res)
	}
	if res := add(2); res.Allowed || res.Count != 2 {
		t.Fatalf("第 3 次应拒绝且 count=2：%+v", res)
	}
	if got := get(); got != 2 {
		t.Fatalf("计数器应持久化 2：got=%d", got)
	}
	// 0 = 关闭：不增、不清零。
	if res := add(0); res.Allowed || res.Count != 2 {
		t.Fatalf("max=0 应拒绝且保持 2：%+v", res)
	}

	// 用户发送重置 → 0，随后可再次用满额度。
	if _, err := s.SessionNextRunReset(facade.SessionNextRunResetRequest{
		InstanceID: inst, SessionID: sid, Scope: scope,
	}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got := get(); got != 0 {
		t.Fatalf("重置后应为 0：got=%d", got)
	}
	var row data.Record
	if ok, err := prj.Table("sessions").Get(sid, &row); err != nil || !ok {
		t.Fatalf("读会话行：ok=%v err=%v", ok, err)
	}
	if _, exists := row[nextRunCountKey]; exists {
		t.Fatalf("计数归零后应删键（行形状与从未计数一致）：%+v", row)
	}

	// 补偿 -1（inbox 落库失败回滚）→ 不小于 0。
	add(2)
	add(2)
	dec, err := s.SessionNextRunDec(facade.SessionNextRunDecRequest{
		InstanceID: inst, SessionID: sid, Scope: scope,
	})
	if err != nil || dec.Count != 1 {
		t.Fatalf("Dec 应为 1：%+v err=%v", dec, err)
	}
	if dec, err = s.SessionNextRunDec(facade.SessionNextRunDecRequest{
		InstanceID: inst, SessionID: sid, Scope: scope,
	}); err != nil || dec.Count != 0 {
		t.Fatalf("Dec 到 0：%+v err=%v", dec, err)
	}
	if dec, err = s.SessionNextRunDec(facade.SessionNextRunDecRequest{
		InstanceID: inst, SessionID: sid, Scope: scope,
	}); err != nil || dec.Count != 0 {
		t.Fatalf("Dec 不应小于 0：%+v err=%v", dec, err)
	}

	// 会话不存在 → CheckAndAdd 报错；Reset 幂等成功；Get 回落 0。
	if _, err := s.SessionNextRunCheckAndAdd(facade.SessionNextRunCheckAndAddRequest{
		InstanceID: inst, SessionID: "no-such", Max: 5, Scope: scope,
	}); err == nil {
		t.Fatalf("会话不存在应报错")
	}
	if _, err := s.SessionNextRunReset(facade.SessionNextRunResetRequest{
		InstanceID: inst, SessionID: "no-such", Scope: scope,
	}); err != nil {
		t.Fatalf("不存在会话的 Reset 应幂等成功: %v", err)
	}
}
