// export_test.go —— G-102 跨用户授权矩阵 + G-103 session.export 的 L1 用例（2026-11-03）。
//
// 矩阵（G-102 用户拍板）：同用户（缺省 user）全通；显式跨用户仅「历史记录读取」类放行
// （session.export 经本包实现；session.get / session.history 经 plugin/v1 转发数据面、无 user
// 参数，天然不涉跨用户）；KV / 桶的读、写跨用户一律 `auth_required` + 审计 `denied`。
//
// 导出（G-103）：读取经数据层公开 API（`data.OpenExistingDialog` 短开真实对话库），导出体
// 内联 `{session_id, dialog_id, exported_at, session, turns:[{...,messages}]}`；会话不存在 →
// `not_found`；`session_id` 缺失 → `args_invalid`；不落任何导出文件。
package plugindata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// newExportStore 构造带 usersRoot 覆盖的 Store（导出夹具落在 t.TempDir，不碰真实盘符）。
func newExportStore(t *testing.T) (*Store, string) {
	t.Helper()
	usersRoot := filepath.Join(t.TempDir(), "users")
	s := newStore(t, Options{UsersRoot: func(string) (string, error) { return usersRoot, nil }})
	return s, usersRoot
}

// mustCreateDialogDB 在 prjusr 目录造一个**真实对话库**（sessions / turns / messages），
// 时间戳用数据面统一 RFC3339FixedNano 口径（字典序 = 时间序）。
// 首轮消息数减一（turns > 1 时），保证轮间消息数不同以便断言嵌套分组。
func mustCreateDialogDB(t *testing.T, prjUsrDir, dialogID, sessionID string, turns, messages int) {
	t.Helper()
	db, err := data.Open(filepath.Join(prjUsrDir, "dialogs", data.DialogFileName(dialogID)))
	if err != nil {
		t.Fatalf("造对话库: %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(data.RFC3339FixedNano)
	if err := db.Table(tableSessions).Insert(sessionID, data.Record{
		"dialog_id": dialogID, "title": "导出测试", "created_at": now,
	}); err != nil {
		t.Fatalf("造会话行: %v", err)
	}
	for i := 0; i < turns; i++ {
		tid := "t" + string(rune('1'+i))
		if err := db.Table(tableTurns).Insert(tid, data.Record{
			"session_id": sessionID, "created_at": now,
		}); err != nil {
			t.Fatalf("造轮次 %s: %v", tid, err)
		}
		n := messages
		if i == 0 && turns > 1 {
			n--
		}
		for j := 0; j < n; j++ {
			mid := tid + "-m" + string(rune('1'+j))
			if err := db.Table(tableMessages).Insert(mid, data.Record{
				"session_id": sessionID, "turn_id": tid,
				"role": "user", "content": "消息 " + mid, "created_at": now,
			}); err != nil {
				t.Fatalf("造消息 %s: %v", mid, err)
			}
		}
	}
}

// TestCrossUserAuthorizationMatrix：同用户全通；跨用户仅 session.export（历史记录读取）放行，
// KV / 桶读写一律 auth_required；拒绝与放行都落审计。
func TestCrossUserAuthorizationMatrix(t *testing.T) {
	s, usersRoot := newExportStore(t)
	wd := `E:\proj\a`

	// ① 同用户（缺省 user）全通：KV / 桶全链路。
	if err := s.kvSet("p1", wd, "", "k", json.RawMessage(`{"v":1}`), 0); err != nil {
		t.Fatalf("同用户 kv.set: %v", err)
	}
	if _, found, err := s.kvGet("p1", wd, "", "k"); err != nil || !found {
		t.Fatalf("同用户 kv.get: found=%v err=%v", found, err)
	}
	if keys, err := s.kvList("p1", wd, "", "", 100); err != nil || len(keys) != 1 {
		t.Fatalf("同用户 kv.list: %v err=%v", keys, err)
	}
	if err := s.kvSet("p1", wd, "", "a-1", json.RawMessage(`1`), 0); err != nil {
		t.Fatalf("同用户 kv.set(a-1): %v", err)
	}
	if n, err := s.kvDeletePrefix("p1", wd, "", "a-"); err != nil || n != 1 {
		t.Fatalf("同用户 kv.del-prefix: n=%d err=%v", n, err)
	}
	if deleted, err := s.kvDelete("p1", wd, "", "k"); err != nil || !deleted {
		t.Fatalf("同用户 kv.del: deleted=%v err=%v", deleted, err)
	}
	if _, created, err := s.bucketEnsure("p1", wd, "", "kb"); err != nil || !created {
		t.Fatalf("同用户 bucket.ensure: created=%v err=%v", created, err)
	}
	if list, err := s.bucketList("p1", wd, ""); err != nil || len(list) != 1 {
		t.Fatalf("同用户 bucket.list: %+v err=%v", list, err)
	}
	if res, err := s.bucketQuery(pluginbridge.BucketQueryRequest{PluginID: "p1", WorkDir: wd, Bucket: "kb"}); err != nil || res.Total != 0 {
		t.Fatalf("同用户 bucket.query: %+v err=%v", res, err)
	}

	// ② 跨用户：KV / 桶的读、写一律 auth_required（G-102：不属于历史记录读取）。
	deny := func(name string, fn func() error) {
		t.Helper()
		if codeOf(t, fn()) != pluginbridge.CodeAuthRequired {
			t.Fatalf("%s 跨用户应回 auth_required", name)
		}
	}
	deny("kv.get", func() error { _, _, err := s.kvGet("p1", wd, "bob", "k"); return err })
	deny("kv.set", func() error { return s.kvSet("p1", wd, "bob", "k", json.RawMessage(`1`), 0) })
	deny("kv.del", func() error { _, err := s.kvDelete("p1", wd, "bob", "k"); return err })
	deny("kv.list", func() error { _, err := s.kvList("p1", wd, "bob", "", 100); return err })
	deny("kv.del-prefix", func() error { _, err := s.kvDeletePrefix("p1", wd, "bob", "a-"); return err })
	deny("bucket.ensure", func() error { _, _, err := s.bucketEnsure("p1", wd, "bob", "kb"); return err })
	deny("bucket.list", func() error { _, err := s.bucketList("p1", wd, "bob"); return err })
	deny("bucket.query", func() error {
		_, err := s.bucketQuery(pluginbridge.BucketQueryRequest{PluginID: "p1", WorkDir: wd, User: "bob", Bucket: "kb"})
		return err
	})

	// 审计：② 的每次拒绝都有 denied 记录（reason = cross_user_not_history_read）。
	deniedCount := 0
	for _, r := range readAudit(t, s) {
		if r.Result != "denied" {
			continue
		}
		if r.Reason != "cross_user_not_history_read" || r.TargetUser != "bob" {
			t.Fatalf("拒绝审计字段不符: %+v", r)
		}
		deniedCount++
	}
	if deniedCount != 8 {
		t.Fatalf("跨用户拒绝审计条数 = %d（应 8）", deniedCount)
	}

	// 宿主授权端口对**非历史读取**仍可显式放行（宿主级追加口，G-102 内置矩阵之外）。
	s2, _ := newExportStore(t)
	s2.opts.Authorize = func(action, pluginID, actor, workDir, target string) (bool, error) {
		return action == "data.kv.get" && target == "bob", nil
	}
	if _, _, err := s2.kvGet("p1", wd, "bob", "k"); err != nil {
		t.Fatalf("端口放行后跨用户读应可用: %v", err)
	}
	if recs := readAudit(t, s2); len(recs) != 1 || recs[0].Result != "ok" || recs[0].TargetUser != "bob" {
		t.Fatalf("端口放行未写审计: %+v", recs)
	}

	// ③ 跨用户 session.export（历史记录读取）→ 放行：给 bob 造真实对话库后导出成功。
	mustCreateDialogDB(t, filepath.Join(usersRoot, "bob"), "dlg-bob", "sess-bob", 1, 1)
	out, err := s.sessionExport(pluginbridge.SessionExportRequest{
		PluginID: "p1", WorkDir: wd, User: "bob", SessionID: "sess-bob",
	})
	if err != nil {
		t.Fatalf("跨用户导出应放行: %v", err)
	}
	if m, ok := out.(json.RawMessage); !ok || !strings.Contains(string(m), "sess-bob") {
		t.Fatalf("导出体不符: %v", out)
	}
}

// TestSessionExportContent：导出体内容完整（会话元信息 + turns 嵌套各自 messages + 来源标识）、
// 会话不存在 → not_found、session_id 缺失 → args_invalid、同用户缺省 user 可导出、不落文件。
func TestSessionExportContent(t *testing.T) {
	s, usersRoot := newExportStore(t)
	wd := `E:\proj\a`
	aliceDir := filepath.Join(usersRoot, "alice")
	mustCreateDialogDB(t, aliceDir, "dlg-1", "sess-1", 2, 2) // t1=1 条消息、t2=2 条

	// ① 同用户（缺省 user）导出成功，内容完整。
	out, err := s.sessionExport(pluginbridge.SessionExportRequest{
		PluginID: "p1", WorkDir: wd, SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("同用户导出: %v", err)
	}
	raw, _ := out.(json.RawMessage)
	var body struct {
		SessionID  string           `json:"session_id"`
		DialogID   string           `json:"dialog_id"`
		ExportedAt string           `json:"exported_at"`
		Session    map[string]any   `json:"session"`
		Turns      []map[string]any `json:"turns"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("导出体非法 JSON: %v", err)
	}
	if body.SessionID != "sess-1" || body.DialogID != "dlg-1" || body.ExportedAt == "" {
		t.Fatalf("来源标识缺失: %+v", body)
	}
	if body.Session["title"] != "导出测试" {
		t.Fatalf("会话元信息不符: %+v", body.Session)
	}
	if len(body.Turns) != 2 {
		t.Fatalf("turns = %d（应 2）", len(body.Turns))
	}
	// 轮内消息嵌套：t1 = 1 条、t2 = 2 条（造数时首轮减一）。
	if msgs, _ := body.Turns[0]["messages"].([]any); len(msgs) != 1 {
		t.Fatalf("turns[0].messages = %d（应 1）", len(msgs))
	}
	if msgs, _ := body.Turns[1]["messages"].([]any); len(msgs) != 2 {
		t.Fatalf("turns[1].messages = %d（应 2）", len(msgs))
	}

	// ② 不落文件：导出后 prjusr 下无任何新文件（不建导出目录）。
	entries, _ := os.ReadDir(aliceDir)
	for _, e := range entries {
		if e.Name() != "chonkpilot.db" && e.Name() != "dialogs" {
			t.Fatalf("导出不得落盘: %s", e.Name())
		}
	}

	// ③ 会话不存在 → not_found；跨用户同理（bob 无任何对话库）。
	if _, err := s.sessionExport(pluginbridge.SessionExportRequest{
		PluginID: "p1", WorkDir: wd, SessionID: "nope",
	}); codeOf(t, err) != pluginbridge.CodeNotFound {
		t.Fatalf("会话不存在应回 not_found: %v", err)
	}
	if _, err := s.sessionExport(pluginbridge.SessionExportRequest{
		PluginID: "p1", WorkDir: wd, User: "bob", SessionID: "sess-1",
	}); codeOf(t, err) != pluginbridge.CodeNotFound {
		t.Fatalf("跨用户会话不存在应回 not_found: %v", err)
	}

	// ④ session_id 缺失 → args_invalid（授权判定之前先校验）。
	if _, err := s.sessionExport(pluginbridge.SessionExportRequest{
		PluginID: "p1", WorkDir: wd,
	}); codeOf(t, err) != pluginbridge.CodeArgsInvalid {
		t.Fatalf("session_id 缺失应回 args_invalid: %v", err)
	}

	// ⑤ 审计：① 同用户导出 = 导出 ok 一条；③ 跨用户（bob）= 授权 ok 一条（读取已放行，
	// 随后 not_found 是查询结果，不再补审计）；同用户 not_found / args_invalid 不写审计。
	exports, crossOK := 0, 0
	for _, r := range readAudit(t, s) {
		if r.Action != "session.export" {
			continue
		}
		exports++
		if r.Result == "ok" && r.TargetUser == "bob" {
			crossOK++
		}
	}
	if exports != 2 || crossOK != 1 {
		t.Fatalf("导出审计不符: exports=%d crossOK=%d", exports, crossOK)
	}
}
