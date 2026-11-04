// plugin_data_test.go — SANDBOX-WRITE-1 P6 数据能力端点的 L2 用例（2026-11-03）。
//
// 面：插件桥 UDS 方法面 `plugin/v1/data.kv.*` / `data.bucket.*` / `prj.users.list` /
// `session.export`（**非 MQ 主题**，零新增主题；见 61 §7.1）。
//
// 驱动方式沿用既有 L2 范式（pluginbridge_test.go）：真实插件桥 + UDS 帧驱动插件
// （plugin.hello → 直接 Call 能力方法），断言：
//   - `prj.users.list` 经**宿主实现**（plugindata.Store）查询 → 只回元数据、不泄露会话内容；
//   - 审计落盘（`<pluginDataRoot>/_audit/audit.jsonl`）；
//   - kv 键空间按 (插件 id, work_dir) 隔离（同一桥内换 work_dir 读不到）；
//   - G-102 跨用户矩阵（2026-11-03）：kv.get 跨用户 `auth_required`、session.export 跨用户放行；
//   - G-103：session.export 经真实对话库内联导出（缺 session_id → `args_invalid`）。
package llmtest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/transport"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-llm/plugindata"
)

func TestPluginDataPrjUsersListAndAudit(t *testing.T) {
	bus := newBus(t)

	// 项目用户目录（只造文件元数据；dialogs 内容为哨兵串，用于断言不泄露）。
	workDir := t.TempDir()
	usersRoot := filepath.Join(workDir, ".chonkpilot", "users")
	writeFile(t, filepath.Join(usersRoot, "alice", "chonkpilot.db"), "db")
	writeFile(t, filepath.Join(usersRoot, "alice", "dialogs", "dialog-aa.db"), "SECRET-SESSION-CONTENT")
	writeFile(t, filepath.Join(usersRoot, "bob", "chonkpilot.db"), "db")

	root := filepath.Join(t.TempDir(), "plugins")
	// UsersRoot 显式注入：本包其它用例会经 `data.SetDataHome` 改全局数据根（跨用例副作用），
	// 这里固定到本用例的 work_dir，保证断言与执行顺序无关。
	store := plugindata.New(plugindata.Options{
		Root:        root,
		CurrentUser: "alice",
		UsersRoot:   func(string) (string, error) { return usersRoot, nil },
	})

	addr := filepath.Join(t.TempDir(), "plugin.sock")
	pb := pluginbridge.New(pluginbridge.Options{Bus: bus, Addr: addr, Version: "test"})
	pb.SetPluginHooks(pluginbridge.PluginHooks{Data: store.Hooks()})
	if err := pb.Start(); err != nil {
		t.Fatalf("pluginbridge start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: pb.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	if res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": "p6.probe"}); err != nil || !res.OK {
		t.Fatalf("hello: %+v %v", res, err)
	}

	// ① prj.users.list：只回元数据（user / db_present / dialog_count / last_active）。
	res, err := cli.Call(ctx, "plugin/v1/prj.users.list", map[string]any{"work_dir": workDir})
	if err != nil || !res.OK {
		t.Fatalf("prj.users.list: %+v %v", res, err)
	}
	m, _ := res.Value.(map[string]any)
	if m["count"] != float64(2) && m["count"] != 2 {
		t.Fatalf("users.count = %#v (%+v)", m["count"], res.Value)
	}
	raw := mustJSON(t, res.Value)
	if strings.Contains(raw, "SECRET") {
		t.Fatalf("用户列表泄露会话内容: %s", raw)
	}
	users, _ := m["users"].([]any)
	if len(users) != 2 {
		t.Fatalf("users = %+v", m["users"])
	}
	first, _ := users[0].(map[string]any)
	if first["user"] != "alice" || first["db_present"] != true || first["dialog_count"] == 0 {
		t.Fatalf("alice 元数据不符: %+v", first)
	}

	// 给 bob 造一个**真实对话库**（G-103 跨用户导出端到端夹具；须在 ① 的
	// dialog_count=0 断言之后创建）。
	bobDB, derr := data.Open(filepath.Join(usersRoot, "bob", "dialogs", data.DialogFileName("dlg-bob")))
	if derr != nil {
		t.Fatalf("造 bob 对话库: %v", derr)
	}
	now := time.Now().UTC().Format(data.RFC3339FixedNano)
	for _, row := range []struct {
		table string
		key   string
		rec   data.Record
	}{
		{"sessions", "sess-l2", data.Record{"dialog_id": "dlg-bob", "title": "L2", "created_at": now}},
		{"turns", "t1", data.Record{"session_id": "sess-l2", "created_at": now}},
		{"messages", "m1", data.Record{"session_id": "sess-l2", "turn_id": "t1", "role": "user", "content": "hi", "created_at": now}},
	} {
		if err := bobDB.Table(row.table).Insert(row.key, row.rec); err != nil {
			t.Fatalf("造 %s 行 %s: %v", row.table, row.key, err)
		}
	}
	_ = bobDB.Close()

	// ② 审计落盘：prj.users.list 一条 ok 记录。
	rec := readAuditFile(t, filepath.Join(root, "_audit", "audit.jsonl"))
	if !strings.Contains(rec, `"action":"prj.users.list"`) || !strings.Contains(rec, `"plugin":"p6.probe"`) {
		t.Fatalf("审计未落盘 / 缺字段: %s", rec)
	}

	// ③ kv：同 (插件, work_dir) 可读回；换 work_dir 不可见。
	if res, err := cli.Call(ctx, "plugin/v1/data.kv.set", map[string]any{
		"work_dir": workDir, "key": "state", "value": map[string]any{"n": 1},
	}); err != nil || !res.OK {
		t.Fatalf("kv.set: %+v %v", res, err)
	}
	res, err = cli.Call(ctx, "plugin/v1/data.kv.get", map[string]any{"work_dir": workDir, "key": "state"})
	if err != nil || !res.OK {
		t.Fatalf("kv.get: %+v %v", res, err)
	}
	if gm, _ := res.Value.(map[string]any); gm["found"] != true {
		t.Fatalf("kv.get 应命中: %+v", res.Value)
	}
	other := t.TempDir()
	res, err = cli.Call(ctx, "plugin/v1/data.kv.get", map[string]any{"work_dir": other, "key": "state"})
	if err != nil || !res.OK {
		t.Fatalf("kv.get(other): %+v %v", res, err)
	}
	if gm, _ := res.Value.(map[string]any); gm["found"] != false {
		t.Fatalf("跨 work_dir 不应可见: %+v", res.Value)
	}

	// ④ G-102 跨用户授权矩阵（经 UDS 端到端，2026-11-03）：kv.get 跨用户 → `auth_required`
	//（KV 不属于历史记录读取）；session.export 跨用户 → **放行**（历史记录读取），bob 的
	// 真实对话库可导出。
	res, err = cli.Call(ctx, "plugin/v1/data.kv.get", map[string]any{
		"work_dir": workDir, "user": "bob", "key": "state",
	})
	if err != nil {
		t.Fatalf("kv.get(bob) 传输错误: %v", err)
	}
	if res.OK || len(res.Errors) == 0 || !strings.Contains(strings.Join(res.Errors, "; "), pluginbridge.CodeAuthRequired) {
		t.Fatalf("跨用户 kv.get 应回 auth_required: %+v", res)
	}

	res, err = cli.Call(ctx, "plugin/v1/session.export", map[string]any{
		"work_dir": workDir, "user": "bob", "session_id": "sess-l2",
	})
	if err != nil || !res.OK {
		t.Fatalf("跨用户 session.export 应放行: %+v %v", res, err)
	}
	em, _ := res.Value.(map[string]any)
	ex, _ := em["export"].(map[string]any)
	if ex == nil || ex["session_id"] != "sess-l2" || ex["dialog_id"] == "" || ex["exported_at"] == "" {
		t.Fatalf("导出体不符: %#v", res.Value)
	}
	if turns, _ := ex["turns"].([]any); len(turns) != 1 {
		t.Fatalf("导出 turns = %d（应 1）", len(turns))
	}

	// ⑤ session.export 缺 session_id → args_invalid（桥侧入参校验）。
	res, err = cli.Call(ctx, "plugin/v1/session.export", map[string]any{"work_dir": workDir})
	if err != nil {
		t.Fatalf("session.export(缺参) 传输错误: %v", err)
	}
	if res.OK || len(res.Errors) == 0 || !strings.Contains(strings.Join(res.Errors, "; "), "args_invalid") {
		t.Fatalf("缺 session_id 应回 args_invalid: %+v", res)
	}

	// ⑥ bucket.ensure：幂等 + 审计。
	for i := 0; i < 2; i++ {
		res, err := cli.Call(ctx, "plugin/v1/data.bucket.ensure", map[string]any{"work_dir": workDir, "name": "kb"})
		if err != nil || !res.OK {
			t.Fatalf("bucket.ensure(%d): %+v %v", i, res, err)
		}
		bm, _ := res.Value.(map[string]any)
		wantCreated := i == 0
		if bm["created"] != wantCreated {
			t.Fatalf("bucket.ensure(%d).created = %#v", i, bm["created"])
		}
	}
	rec = readAuditFile(t, filepath.Join(root, "_audit", "audit.jsonl"))
	if got := strings.Count(rec, `"action":"data.bucket.ensure"`); got != 1 {
		t.Fatalf("建桶审计条数 = %d（幂等应只 1 条）:\n%s", got, rec)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readAuditFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读审计 %s: %v", path, err)
	}
	return string(raw)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
