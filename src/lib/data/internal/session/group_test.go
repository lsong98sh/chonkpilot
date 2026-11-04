// 群聊两表（参与者名单 / 每大轮次投递对象）落库单测（SUBCHAT-3 阶段 E）。
//
// 覆盖（E4 要求）：读写往返 · 外键关联（按会话前缀隔离）· 名单固定顺序（seq = 列表顺序）·
// 整表替换（删除已移除项、重复 agent 按首现）· 大轮次投递对象的轮次升序（主键零填充）与
// 非法轮次拒绝。
//
// P4-2 批次 E（2026-10-31）：存储实现体已切到会话组件（`component.Group*`），本用例改经
// 门面方法（`GroupParticipants*` / `GroupRoundTargets*`）驱动 —— 断言口径不变。
package session

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TestGroupParticipantsReplaceAndLoad 参与者名单：顺序 = 列表顺序（seq 1..N）、外键按会话隔离、
// 整表替换删旧项、重复 agent 按首现、空 agent 跳过。
func TestGroupParticipantsReplaceAndLoad(t *testing.T) {
	s, scope := newSessionEnv(t)
	const inst = "ins-group"
	const g1, g2 = "grp-1", "grp-2"
	load := func(sid string) []facade.GroupParticipant {
		t.Helper()
		got, err := s.GroupParticipantsLoad(facade.GroupParticipantsLoadRequest{
			InstanceID: inst, Scope: scope, SessionID: sid,
		})
		if err != nil {
			t.Fatalf("load %s: %v", sid, err)
		}
		return got.List
	}
	save := func(sid string, list []facade.GroupParticipant) {
		t.Helper()
		if _, err := s.GroupParticipantsSave(facade.GroupParticipantsSaveRequest{
			InstanceID: inst, Scope: scope, SessionID: sid, List: list,
		}); err != nil {
			t.Fatalf("save %s: %v", sid, err)
		}
	}

	// 空名单 → 空数组（不报错）。
	if list := load(g1); len(list) != 0 {
		t.Fatalf("空名单应返回空数组：%+v", list)
	}

	// 写名单：顺序 = 列表顺序；空 agent / 重复项被规整。
	save(g1, []facade.GroupParticipant{
		{Agent: "主 agent", Role: "发起者"},
		{Agent: "  "},                  // 空 → 跳过
		{Agent: "审查员", Role: "审 diff"}, // 顺序 2
		{Agent: "审查员", Role: "重复项忽略"},  // 重复 → 按首现
		{Agent: "测试员", Role: "回归"},
	})
	list := load(g1)
	want := []struct {
		agent, role string
	}{{"主 agent", "发起者"}, {"审查员", "审 diff"}, {"测试员", "回归"}}
	if len(list) != len(want) {
		t.Fatalf("名单长度 = %d，期望 %d：%+v", len(list), len(want), list)
	}
	for i, w := range want {
		if list[i].Agent != w.agent || list[i].Role != w.role || list[i].Seq != i+1 {
			t.Fatalf("名单[%d] = %+v，期望 {agent:%s role:%s seq:%d}", i, list[i], w.agent, w.role, i+1)
		}
		if list[i].SessionID != g1 {
			t.Fatalf("名单[%d].SessionID = %q，期望 %q（外键列回落会话 id）", i, list[i].SessionID, g1)
		}
	}

	// 外键隔离：另一群聊的名单不受影响。
	save(g2, []facade.GroupParticipant{{Agent: "别的 agent"}})
	if l2 := load(g2); len(l2) != 1 || l2[0].Agent != "别的 agent" {
		t.Fatalf("g2 名单 = %+v", l2)
	}
	if l1 := load(g1); len(l1) != 3 {
		t.Fatalf("g1 名单被 g2 写入污染：%+v", l1)
	}

	// 整表替换：移除「审查员」→ 该行删除；剩余按新列表顺序重排 seq。
	save(g1, []facade.GroupParticipant{
		{Agent: "测试员", Role: "回归"},
		{Agent: "主 agent", Role: "发起者 + 兜底裁判"},
	})
	list = load(g1)
	if len(list) != 2 || list[0].Agent != "测试员" || list[0].Seq != 1 || list[1].Agent != "主 agent" || list[1].Seq != 2 {
		t.Fatalf("整表替换后名单 = %+v（期望[测试员(1), 主 agent(2)]，且「审查员」已删）", list)
	}
	if list[1].Role != "发起者 + 兜底裁判" {
		t.Fatalf("已存在项应更新 role：%+v", list[1])
	}

	// 清空名单（空列表）→ 该会话名单删净。
	save(g1, nil)
	if l := load(g1); len(l) != 0 {
		t.Fatalf("清空后应为空数组：%+v", l)
	}
}

// TestGroupRoundTargetsSaveAndLoad 大轮次投递对象：单轮幂等覆盖 + 全轮读取按轮次升序
// （主键 6 位零填充 → 10/11 排在 2 之后）+ 非法轮次拒绝 + 会话隔离。
func TestGroupRoundTargetsSaveAndLoad(t *testing.T) {
	s, scope := newSessionEnv(t)
	const inst = "ins-group-round"
	const g1, g2 = "grp-1", "grp-2"
	save := func(sid string, round int, agents []string) error {
		t.Helper()
		_, err := s.GroupRoundTargetsSave(facade.GroupRoundTargetsSaveRequest{
			InstanceID: inst, Scope: scope, SessionID: sid, Round: round, Agents: agents,
		})
		return err
	}
	load := func(sid string, round int) []facade.GroupRoundTarget {
		t.Helper()
		got, err := s.GroupRoundTargetsLoad(facade.GroupRoundTargetsLoadRequest{
			InstanceID: inst, Scope: scope, SessionID: sid, Round: round,
		})
		if err != nil {
			t.Fatalf("load %s/%d: %v", sid, round, err)
		}
		return got.List
	}

	// 非法轮次（<= 0）→ 明确拒绝。
	if err := save(g1, 0, []string{"A"}); err == nil {
		t.Fatal("round=0 应被拒绝")
	}

	for _, r := range []struct {
		round  int
		agents []string
	}{
		{1, []string{"主 agent", "审查员"}},
		{2, []string{"审查员"}},
		{10, []string{"测试员", "审查员"}},
		{11, nil}, // 空 = 无人接收（落空数组）
	} {
		if err := save(g1, r.round, r.agents); err != nil {
			t.Fatalf("save round=%d: %v", r.round, err)
		}
	}
	// 幂等覆盖：重写第 1 轮。
	if err := save(g1, 1, []string{"主 agent"}); err != nil {
		t.Fatalf("rewrite round=1: %v", err)
	}

	all := load(g1, 0)
	if len(all) != 4 {
		t.Fatalf("全轮读取应为 4 条：%+v", all)
	}
	for i, wantRound := range []int{1, 2, 10, 11} {
		if all[i].Round != wantRound {
			t.Fatalf("全轮读取应按轮次升序：位置 %d = %d，期望 %d（%+v）", i, all[i].Round, wantRound, all)
		}
		if all[i].SessionID != g1 {
			t.Fatalf("行[%d].SessionID = %q，期望 %q", i, all[i].SessionID, g1)
		}
	}
	if len(all[0].Agents) != 1 || all[0].Agents[0] != "主 agent" {
		t.Fatalf("第 1 轮应被幂等覆盖为 [主 agent]：%+v", all[0].Agents)
	}
	if all[1].Agents == nil || len(all[1].Agents) != 1 || all[1].Agents[0] != "审查员" {
		t.Fatalf("第 2 轮 agents = %+v", all[1].Agents)
	}
	if all[3].Agents == nil || len(all[3].Agents) != 0 {
		t.Fatalf("空投递对象应落空数组（非 nil/缺键）：%+v", all[3].Agents)
	}

	// 单轮读取：命中 / 未命中（空数组）。
	one := load(g1, 10)
	if len(one) != 1 || one[0].Round != 10 {
		t.Fatalf("单轮读取 round=10：%+v", one)
	}
	if miss := load(g1, 3); len(miss) != 0 {
		t.Fatalf("未写入轮次应返回空数组：%+v", miss)
	}

	// 会话隔离。
	if err := save(g2, 1, []string{"别的 agent"}); err != nil {
		t.Fatalf("save g2: %v", err)
	}
	if l := load(g1, 0); len(l) != 4 {
		t.Fatalf("g1 被 g2 写入污染：%+v", l)
	}
}
