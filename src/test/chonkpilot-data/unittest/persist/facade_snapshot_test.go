// data 门面（inline 绑定）与既有 MQ 面（`data-snapshot-*`）的**行为等价**单测
// （阶段 4 试点验收：一份定义 → 两种绑定，同一落点、同一语义）。
//
// 两条路径（同一实例、同一会话数据层）：
//   - 门面 = inline 直调（`chonkpilot-data/facade/inline`；同进程函数调用，不经 MQ，见 23 §7）
//   - MQ   = persist 的 `data-snapshot-set` / `data-snapshot-get`（消息面，61 §3；既有驱动方式）
//
// 覆盖：写入落点相同 / 读回值逐项相同 / 无快照语义相同 / 读改写无损（DTO 覆盖全部字段）/
// 自登记路径（Scope 提示）/ MQ 应答 payload 形状**不变**（61 零变更：history + snapshot_turn）。
//
// 文件放在 persist 测试目录内，以便与 MQ 面复用同一套宿主 helper
// （newTestPersist / regInstance / dataCall / dataResult）。
package persist_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

const facadeInstance = "ins-test"

// seedSnapshot 造一份"字段齐全"的内核快照（覆盖 kind / reasoning / tool_calls / _meta
// 与空字段消息——压缩的保留段原样回写依赖这些字段全过面）。
func seedSnapshot(turn string) data.Snapshot {
	return data.Snapshot{
		SnapshotTurn: turn,
		History: []data.ChatMsg{
			{Role: "system", Content: "[已压缩早前对话] sum"},
			{Role: "user", Kind: "text", Content: "看一下 a.go"},
			{Role: "assistant", Reasoning: "先读文件", Content: "好", ToolCalls: []data.ToolCall{
				func() data.ToolCall {
					var tc data.ToolCall
					tc.ID, tc.Type = "c1", "function"
					tc.Function.Name, tc.Function.Arguments = "file_read", `{"path":"a.go"}`
					return tc
				}(),
			}},
			{Role: "tool", ToolCallID: "c1", Content: "package main",
				Meta: map[string]any{"category": "fs", "async": "never"}},
		},
	}
}

// snapshotMQPayload 把内核快照转成 MQ 报文里的 snapshot 载荷
// （JSON 形状 = `{history, snapshot_turn}`；刻意不复用门面 DTO 的 json 命名）。
func snapshotMQPayload(t *testing.T, snap data.Snapshot) map[string]any {
	t.Helper()
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return out
}

// mqSetSnapshot 经 MQ 写快照（data-snapshot-set）。
func mqSetSnapshot(t *testing.T, bus mq.Bus, session string, snap data.Snapshot) {
	t.Helper()
	dataCall(t, bus, "data-snapshot-set", map[string]any{
		"instance_id": facadeInstance,
		"data":        map[string]any{"session_id": session, "snapshot": snapshotMQPayload(t, snap)},
	})
}

// mqGetSnapshot 经 MQ 读快照（data-snapshot-get；result.snapshot 原文，无快照 → nil）。
func mqGetSnapshot(t *testing.T, bus mq.Bus, session string) map[string]any {
	t.Helper()
	res := dataResult(t, dataCall(t, bus, "data-snapshot-get", map[string]any{
		"instance_id": facadeInstance, "data": map[string]any{"session_id": session},
	}))
	got, _ := res["snapshot"].(map[string]any)
	return got
}

// facadeGetKernel 经门面（inline）读快照并翻回内核形态（便于与 MQ 面逐字段比较）。
func facadeGetKernel(t *testing.T, api facade.API, session string) data.Snapshot {
	t.Helper()
	resp, err := api.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: facadeInstance, SessionID: session,
	})
	if err != nil {
		t.Fatalf("facade SnapshotGet: %v", err)
	}
	if !resp.Found {
		t.Fatalf("facade SnapshotGet: session %s 无快照", session)
	}
	return data.SnapshotFromFacade(resp.Snapshot)
}

// facadeSetKernel 经门面（inline）写内核快照（经 DTO 翻译）。
func facadeSetKernel(t *testing.T, api facade.API, session string, snap data.Snapshot) {
	t.Helper()
	if _, err := api.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: facadeInstance, Snapshot: data.SnapshotToFacade(session, snap),
	}); err != nil {
		t.Fatalf("facade SnapshotSet: %v", err)
	}
}

// TestFacadeInlineEqualsMQPath：同一份内容分别经门面（s-fac）与 MQ（s-mq）写入 →
// 两条路径读回**逐项相同**；MQ 应答 payload 形状不变（61 零变更）。
func TestFacadeInlineEqualsMQPath(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	// 门面写 s-fac；MQ 写 s-mq（同一份内核快照）
	facadeSetKernel(t, api, "s-fac", seedSnapshot("t7"))
	mqSetSnapshot(t, bus, "s-mq", seedSnapshot("t7"))

	// ① 门面读两条会话 → 内容一致（写入门面 → 读回；写入 MQ → 门面也读得到 = 同一落点）
	viaFacade, viaMQ := facadeGetKernel(t, api, "s-fac"), facadeGetKernel(t, api, "s-mq")
	if !reflect.DeepEqual(viaFacade, viaMQ) {
		t.Fatalf("门面读回内容不一致：\n门面写=%+v\nMQ 写=%+v", viaFacade, viaMQ)
	}
	if !reflect.DeepEqual(viaFacade, seedSnapshot("t7")) {
		t.Fatalf("门面读回与写入值不一致：%+v", viaFacade)
	}

	// ② MQ 读两条会话 → 报文逐项一致（门面写的内容 MQ 面读到同一份）
	mqFacade, mqMQ := mqGetSnapshot(t, bus, "s-fac"), mqGetSnapshot(t, bus, "s-mq")
	if !reflect.DeepEqual(mqFacade, mqMQ) {
		t.Fatalf("MQ 读回内容不一致：\n门面写=%+v\nMQ 写=%+v", mqFacade, mqMQ)
	}

	// ③ 消息面 payload 形状不变（61 零变更）：仅 history + snapshot_turn 两键
	//    （门面 DTO 的领域命名 messages/turn **不外溢**到消息面）
	keys := make([]string, 0, len(mqMQ))
	for k := range mqMQ {
		keys = append(keys, k)
	}
	if len(keys) != 2 || mqMQ["history"] == nil || mqMQ["snapshot_turn"] != "t7" {
		t.Fatalf("MQ payload 形状变化：%+v", mqMQ)
	}
}

// TestFacadeInlineReadModifyWriteIsLossless：门面「读 → 原样写回」= 恒等（DTO 无字段丢失）：
// 这是压缩插件"读快照 → 变换 → 写回"的前提；漏字段即静默丢数据。
func TestFacadeInlineReadModifyWriteIsLossless(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	mqSetSnapshot(t, bus, "s-rt", seedSnapshot("t9"))
	before := mqGetSnapshot(t, bus, "s-rt")

	read := facadeGetKernel(t, api, "s-rt") // 门面读（经 DTO）
	facadeSetKernel(t, api, "s-rt", read)   // 原样写回

	after := mqGetSnapshot(t, bus, "s-rt")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("门面读改写丢失/改动了字段：\nbefore=%+v\nafter =%+v", before, after)
	}
}

// TestFacadeInlineMissingSession：无快照语义两条路径一致
// （门面 Found=false + 零值；MQ snapshot=null）。
func TestFacadeInlineMissingSession(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	resp, err := api.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: facadeInstance, SessionID: "s-none",
	})
	if err != nil {
		t.Fatalf("facade SnapshotGet: %v", err)
	}
	if resp.Found {
		t.Fatalf("无快照应 Found=false：%+v", resp)
	}
	if resp.Snapshot.SessionID != "" || len(resp.Snapshot.Messages) != 0 || resp.Snapshot.Turn != "" {
		t.Fatalf("无快照应返回零值：%+v", resp.Snapshot)
	}
	if got := mqGetSnapshot(t, bus, "s-none"); got != nil {
		t.Fatalf("MQ 面无快照应 snapshot=null：%+v", got)
	}
}

// TestFacadeInlineSelfRegistersFromScope：实例未在本进程登记时，门面按调用方带入的
// 数据根（Scope = 事件载荷里的 work_dir/data_dir 事实）自登记后读写成功
// ——语义等价于原 compress.prjUsrDB 的"凭事件载荷自给"。
func TestFacadeInlineSelfRegistersFromScope(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus) // 登记 ins-test；本用例用**另一个**未登记的实例
	api := inline.New(bus)

	const inst = "ins-scope"
	wd := t.TempDir()
	scope := facade.Scope{WorkDir: wd, DataDir: filepath.Join(wd, ".chonkpilot")}
	snap := seedSnapshot("t1")

	if _, err := api.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: inst, Snapshot: data.SnapshotToFacade("s-scope", snap), Scope: scope,
	}); err != nil {
		t.Fatalf("SnapshotSet（自登记）: %v", err)
	}
	// 自登记后：不带 Scope 也能读（实例绑定表已有该实例）
	resp, err := api.SnapshotGet(facade.SnapshotGetRequest{InstanceID: inst, SessionID: "s-scope"})
	if err != nil || !resp.Found {
		t.Fatalf("SnapshotGet: found=%v err=%v", resp.Found, err)
	}
	if got := data.SnapshotFromFacade(resp.Snapshot); !reflect.DeepEqual(got, snap) {
		t.Fatalf("自登记路径读回不一致：%+v", got)
	}
}
