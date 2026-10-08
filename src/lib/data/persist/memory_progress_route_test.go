// 记忆提取进度**专用表**白盒（OP-05/06，2026-10-06）：
// `data-memory-extract-{load,save,delete}` 读写 prjusr bbolt 桶 `memory_extract`
// （记录 = {session_id, category, last_turn_id}，主键 = <session_id>\x00<category>）；
// **不再**写 config 键 `memory-extract.<会话>.<类别>`（进度是记录不是配置）。
package persist

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// memExtractCall 发一次 data-memory-extract-* 请求并等应答（收集带 ok 的载荷）。
func memExtractCall(t *testing.T, s *Service, subject string, body map[string]any) map[string]any {
	t.Helper()
	body["req_id"] = "me-" + subject
	ch := make(chan map[string]any, 1)
	sub, err := s.Bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if _, ok := m["ok"]; !ok {
			return nil
		}
		select {
		case ch <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("sub %s: %v", subject, err)
	}
	defer sub.Unsubscribe()
	b, _ := json.Marshal(body)
	_ = s.Bus.Emit(context.Background(), subject, b).Wait()
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting reply for %s", subject)
		return nil
	}
}

// TestMemoryExtractTableCRUD：save/load/delete 往返落 prjusr 桶 memory_extract，且不写 config 键。
func TestMemoryExtractTableCRUD(t *testing.T) {
	wd := t.TempDir()
	s := newSweepTestService(t)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	const inst = "ins-memprog"
	s.onInstanceRegister(instanceRegister, instancePayload(inst, wd, ""))

	// save → prjusr 桶有记录，config 表无该键（旧路径已废）。
	r := memExtractCall(t, s, "data-memory-extract-save", map[string]any{
		"instance_id": inst,
		"data":        map[string]any{"session_id": "s-1", "category": "项目概要", "last_turn_id": "t-9"},
	})
	if ok, _ := r["ok"].(bool); !ok {
		t.Fatalf("extract-save 失败: %+v", r)
	}
	puDB, err := s.prjUsrDB(inst)
	if err != nil {
		t.Fatalf("prjUsrDB: %v", err)
	}
	var rec data.Record
	if ok, err := puDB.Table("memory_extract").Get("s-1\x00项目概要", &rec); err != nil || !ok {
		t.Fatalf("进度应落 prjusr 桶 memory_extract：ok=%v err=%v", ok, err)
	}
	if rec["last_turn_id"] != "t-9" || rec["session_id"] != "s-1" || rec["category"] != "项目概要" {
		t.Fatalf("进度记录字段不符: %+v", rec)
	}
	// prj 团队库直查（`s.prjDB` 已删：生产零调用，D-45 收尾清理）。
	info, ok := s.View.Lookup(inst)
	if !ok {
		t.Fatalf("实例未登记: %s", inst)
	}
	prjDB, releasePrj, err := s.PrjByInst(inst, info)
	if err != nil {
		t.Fatalf("PrjByInst: %v", err)
	}
	defer releasePrj() // 短开（D-45）：用完即释
	if got := kernel.ConfigGet(puDB, "memory-extract.s-1.项目概要"); got != "" {
		t.Fatalf("旧 config 键路径不应再写入（prjusr）：got=%q", got)
	}
	if got := kernel.ConfigGet(prjDB, "memory-extract.s-1.项目概要"); got != "" {
		t.Fatalf("进度不得污染 prj 团队库：got=%q", got)
	}

	// load（整会话）→ 返回该会话全部类别进度。
	r = memExtractCall(t, s, "data-memory-extract-load", map[string]any{
		"instance_id": inst, "session_id": "s-1",
	})
	if ok, _ := r["ok"].(bool); !ok {
		t.Fatalf("extract-load 失败: %+v", r)
	}
	list, _ := r["result"].(map[string]any)["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("load 应返回 1 条进度: %+v", r)
	}
	row, _ := list[0].(map[string]any)
	if row["last_turn_id"] != "t-9" || row["category"] != "项目概要" {
		t.Fatalf("load 行不符: %+v", row)
	}

	// delete（整会话）→ 桶清空。
	r = memExtractCall(t, s, "data-memory-extract-delete", map[string]any{
		"instance_id": inst,
		"data":        map[string]any{"session_id": "s-1"},
	})
	if ok, _ := r["ok"].(bool); !ok {
		t.Fatalf("extract-delete 失败: %+v", r)
	}
	if ok, _ := puDB.Table("memory_extract").Get("s-1\x00项目概要", &rec); ok {
		t.Fatal("delete 后进度应从桶移除")
	}
}
