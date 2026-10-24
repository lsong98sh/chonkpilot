// 群聊两表（参与者名单 / 每大轮次投递对象）落库单测（SUBCHAT-3 阶段 E）。
//
// 覆盖（E4 要求）：读写往返 · 外键关联（按会话前缀隔离）· 名单固定顺序（seq = 列表顺序）·
// 整表替换（删除已移除项、重复 agent 按首现）· 大轮次投递对象的轮次升序（主键零填充）与
// 非法轮次拒绝。
package session

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// openGroupTestDB 开临时库（跑迁移 → 建出两桶）。
func openGroupTestDB(t *testing.T) *data.DB {
	t.Helper()
	db, err := data.Open(filepath.Join(t.TempDir(), "prjusr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestGroupParticipantsReplaceAndLoad 参与者名单：顺序 = 列表顺序（seq 1..N）、外键按会话隔离、
// 整表替换删旧项、重复 agent 按首现、空 agent 跳过。
func TestGroupParticipantsReplaceAndLoad(t *testing.T) {
	db := openGroupTestDB(t)
	const g1, g2 = "grp-1", "grp-2"

	// 空名单 → 空数组（不报错）。
	if list, err := groupParticipantsLoad(db, g1); err != nil || len(list) != 0 {
		t.Fatalf("空名单应返回空数组：len=%d err=%v", len(list), err)
	}

	// 写名单：顺序 = 列表顺序；空 agent / 重复项被规整。
	if err := groupParticipantsReplace(db, g1, []facade.GroupParticipant{
		{Agent: "主 agent", Role: "发起者"},
		{Agent: "  "},                  // 空 → 跳过
		{Agent: "审查员", Role: "审 diff"}, // 顺序 2
		{Agent: "审查员", Role: "重复项忽略"},  // 重复 → 按首现
		{Agent: "测试员", Role: "回归"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	list, err := groupParticipantsLoad(db, g1)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
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
	if err := groupParticipantsReplace(db, g2, []facade.GroupParticipant{{Agent: "别的 agent"}}); err != nil {
		t.Fatalf("replace g2: %v", err)
	}
	if l2, err := groupParticipantsLoad(db, g2); err != nil || len(l2) != 1 || l2[0].Agent != "别的 agent" {
		t.Fatalf("g2 名单 = %+v err=%v", l2, err)
	}
	if l1, err := groupParticipantsLoad(db, g1); err != nil || len(l1) != 3 {
		t.Fatalf("g1 名单被 g2 写入污染：%+v err=%v", l1, err)
	}

	// 整表替换：移除「审查员」→ 该行删除；剩余按新列表顺序重排 seq。
	if err := groupParticipantsReplace(db, g1, []facade.GroupParticipant{
		{Agent: "测试员", Role: "回归"},
		{Agent: "主 agent", Role: "发起者 + 兜底裁判"},
	}); err != nil {
		t.Fatalf("replace 2: %v", err)
	}
	list, err = groupParticipantsLoad(db, g1)
	if err != nil {
		t.Fatalf("load 2: %v", err)
	}
	if len(list) != 2 || list[0].Agent != "测试员" || list[0].Seq != 1 || list[1].Agent != "主 agent" || list[1].Seq != 2 {
		t.Fatalf("整表替换后名单 = %+v（期望[测试员(1), 主 agent(2)]，且「审查员」已删）", list)
	}
	if list[1].Role != "发起者 + 兜底裁判" {
		t.Fatalf("已存在项应更新 role：%+v", list[1])
	}

	// 清空名单（空列表）→ 该会话名单删净。
	if err := groupParticipantsReplace(db, g1, nil); err != nil {
		t.Fatalf("replace empty: %v", err)
	}
	if l, err := groupParticipantsLoad(db, g1); err != nil || len(l) != 0 {
		t.Fatalf("清空后应为空数组：%+v err=%v", l, err)
	}
}

// TestGroupRoundTargetsSaveAndLoad 大轮次投递对象：单轮幂等覆盖 + 全轮读取按轮次升序
// （主键 6 位零填充 → 10/11 排在 2 之后）+ 非法轮次拒绝 + 会话隔离。
func TestGroupRoundTargetsSaveAndLoad(t *testing.T) {
	db := openGroupTestDB(t)
	const g1, g2 = "grp-1", "grp-2"

	// 非法轮次（<= 0）→ 明确拒绝。
	if err := groupRoundTargetSave(db, g1, 0, []string{"A"}); err == nil {
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
		if err := groupRoundTargetSave(db, g1, r.round, r.agents); err != nil {
			t.Fatalf("save round=%d: %v", r.round, err)
		}
	}
	// 幂等覆盖：重写第 1 轮。
	if err := groupRoundTargetSave(db, g1, 1, []string{"主 agent"}); err != nil {
		t.Fatalf("rewrite round=1: %v", err)
	}

	all, err := groupRoundTargetsLoad(db, g1, 0)
	if err != nil {
		t.Fatalf("load all: %v", err)
	}
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
	one, err := groupRoundTargetsLoad(db, g1, 10)
	if err != nil || len(one) != 1 || one[0].Round != 10 {
		t.Fatalf("单轮读取 round=10：%+v err=%v", one, err)
	}
	if miss, err := groupRoundTargetsLoad(db, g1, 3); err != nil || len(miss) != 0 {
		t.Fatalf("未写入轮次应返回空数组：%+v err=%v", miss, err)
	}

	// 会话隔离。
	if err := groupRoundTargetSave(db, g2, 1, []string{"别的 agent"}); err != nil {
		t.Fatalf("save g2: %v", err)
	}
	if l, err := groupRoundTargetsLoad(db, g1, 0); err != nil || len(l) != 4 {
		t.Fatalf("g1 被 g2 写入污染：%+v err=%v", l, err)
	}
}
