// group_tables_test.go — 群聊两表数据面对账（SUBCHAT-3 阶段 E，2026-10-16）。
//
// 以 `docs/spec/60-reference/61-messages.schema.json` 为唯一基准，**经内存总线驱动**四条
// `data-session-group-*`（§3.2c），断言：① 应答键 ⊆ 契约 result 声明（B1 机制，复用
// contract_reconcile_test.go 的同包 helper）；② **语义级**（顺序 = 名单固定顺序 / 大轮次按轮次
// 升序 / 整表替换）——不只"键存在"。
package persist_test

import (
	"testing"
)

// TestDataSessionGroupKeysSubsetOfContract 驱动群聊参与者名单 / 大轮次投递对象四主题。
func TestDataSessionGroupKeysSubsetOfContract(t *testing.T) {
	c := contractLoad(t)
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	const grp = "smoke-grp"

	// ① 参与者名单：save（整表替换）→ load（按名单固定顺序）。
	assertDataResult(t, c, bus, "data-session-group-participants-save", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{"session_id": grp, "list": []any{
			map[string]any{"agent": "主 agent", "role": "发起者 + 兜底裁判"},
			map[string]any{"agent": "审查员", "role": "审 diff"},
			map[string]any{"agent": "测试员", "role": "回归"},
		}},
	})
	res := assertDataResult(t, c, bus, "data-session-group-participants-load", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "session_id": grp,
	})
	list, _ := res["list"].([]any)
	if len(list) != 3 {
		t.Fatalf("参与者名单应有 3 项：%+v", res)
	}
	wantAgents := []string{"主 agent", "审查员", "测试员"}
	for i, rowAny := range list {
		row, _ := rowAny.(map[string]any)
		if row["agent"] != wantAgents[i] {
			t.Fatalf("名单顺序（seq 升序）第 %d 项 = %v，期望 %s（%+v）", i, row["agent"], wantAgents[i], list)
		}
		if row["seq"] != float64(i+1) {
			t.Fatalf("第 %d 项 seq = %v，期望 %d（= 列表顺序）", i, row["seq"], i+1)
		}
		if row["session_id"] != grp {
			t.Fatalf("第 %d 项 session_id = %v，期望 %s（外键列回落会话 id）", i, row["session_id"], grp)
		}
	}
	// 整表替换：写 2 项（顺序不同）→ 名单只剩这 2 项，seq 重排。
	assertDataResult(t, c, bus, "data-session-group-participants-save", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"session_id": grp, "list": []any{
			map[string]any{"agent": "测试员"},
			map[string]any{"agent": "主 agent"},
		}},
	})
	res = assertDataResult(t, c, bus, "data-session-group-participants-load", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "session_id": grp,
	})
	list, _ = res["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("整表替换后应剩 2 项（「审查员」被删）：%+v", res)
	}
	if first, _ := list[0].(map[string]any); first["agent"] != "测试员" || first["seq"] != float64(1) {
		t.Fatalf("整表替换后首项 = %+v，期望 {测试员, seq:1}", first)
	}

	// ② 大轮次投递对象：save（按 (会话, 轮次) 幂等覆盖）→ load（单轮 / 全轮升序）。
	for _, r := range []struct {
		round  int
		agents []any
	}{
		{1, []any{"主 agent", "审查员"}},
		{2, []any{"审查员"}},
		{10, []any{"测试员"}},
	} {
		assertDataResult(t, c, bus, "data-session-group-round-targets-save", map[string]any{
			"req_id": "rt", "instance_id": "ins-test",
			"data": map[string]any{"session_id": grp, "round": r.round, "agents": r.agents},
		})
	}
	one := assertDataResult(t, c, bus, "data-session-group-round-targets-load", map[string]any{
		"req_id": "rt2", "instance_id": "ins-test", "session_id": grp, "round": 10,
	})
	oneList, _ := one["list"].([]any)
	if len(oneList) != 1 {
		t.Fatalf("单轮读取应恰好 1 条：%+v", one)
	}
	all := assertDataResult(t, c, bus, "data-session-group-round-targets-load", map[string]any{
		"req_id": "rt3", "instance_id": "ins-test", "session_id": grp,
	})
	allList, _ := all["list"].([]any)
	if len(allList) != 3 {
		t.Fatalf("全轮读取应 3 条：%+v", all)
	}
	for i, wantRound := range []float64{1, 2, 10} {
		row, _ := allList[i].(map[string]any)
		if row["round"] != wantRound {
			t.Fatalf("全轮读取第 %d 项 round = %v，期望 %v（按轮次升序；6 位零填充保证 10 排在 2 之后）",
				i, row["round"], wantRound)
		}
	}
}
