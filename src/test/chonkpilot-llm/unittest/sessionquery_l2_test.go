// sessionquery_l2_test.go —— SESSION-QUERY-1 的 L2 真桥用例（2026-11-15）。
//
// 覆盖：真实插件桥（UDS + plugin.hello）→ `plugin/v1/session.search` / `.browse` →
// 宿主 `plugindata.Store` 读取 prjusr 对话库；断言 private 模式对他人 `permission_denied`。
package llmtest

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-llm/plugindata"
	"github.com/lsong98sh/chonkpilot-framework/host/core/transport"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// seedSessionDialog 在指定用户 prjusr 根造一个真实对话库（会话 + 一轮 + 一条消息）。
func seedSessionDialog(t *testing.T, prjUsrDir, dialogID, sessionID, content string) {
	t.Helper()
	db, err := data.Open(filepath.Join(prjUsrDir, "dialogs", data.DialogFileName(dialogID)))
	if err != nil {
		t.Fatalf("造对话库: %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(data.RFC3339FixedNano)
	if err := db.Table("sessions").Insert(sessionID, data.Record{
		"session_id": sessionID, "dialog_id": dialogID, "title": "L2 会话",
		"parent_id": "", "created_at": now, "updated_at": now,
	}); err != nil {
		t.Fatalf("造会话: %v", err)
	}
	turnID := "t-" + sessionID
	if err := db.Table("turns").Insert(turnID, data.Record{
		"session_id": sessionID, "turn_id": turnID, "created_at": now, "status": "done",
	}); err != nil {
		t.Fatalf("造轮次: %v", err)
	}
	if err := db.Table("messages").Insert("m-"+sessionID, data.Record{
		"session_id": sessionID, "turn_id": turnID, "role": "user",
		"content": content, "created_at": now,
	}); err != nil {
		t.Fatalf("造消息: %v", err)
	}
}

// TestSessionSearchViaPluginBridge：真桥检索自己的会话 + private 下点名他人被拒。
func TestSessionSearchViaPluginBridge(t *testing.T) {
	bus := newBus(t)
	llm := startLLM(t, bus, textLLMServer("ok").URL, true)

	wd := t.TempDir()
	t.Cleanup(data.Reset) // 先关库连接，再删临时目录（LIFO）
	bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-session-l2", "client_type": "unittest", "work_dir": wd,
	})).Wait()

	aliceDir, err := data.PrjUsrDir(wd, "")
	if err != nil {
		t.Fatalf("prjusr dir: %v", err)
	}
	seedSessionDialog(t, aliceDir, "d-alice", "s-alice", "交付时间定在周五")
	usersRoot := filepath.Dir(aliceDir)
	seedSessionDialog(t, filepath.Join(usersRoot, "bob"), "d-bob", "s-bob", "交付时间改到下周一")

	store := plugindata.New(plugindata.Options{Root: filepath.Join(t.TempDir(), "plugins")})
	llm.SetPluginData(store)

	pb := pluginbridge.New(pluginbridge.Options{
		Bus: bus, Addr: filepath.Join(t.TempDir(), "plugin.sock"), Version: "test",
	})
	if err := pb.Start(); err != nil {
		t.Fatalf("pluginbridge start: %v", err)
	}
	t.Cleanup(func() { _ = pb.Close() })
	llm.SetPluginRegistry(pb.Registry())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli, err := transport.Dial(ctx, transport.Options{Backend: transport.BackendUDS, Addr: pb.Addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if res, err := cli.Call(ctx, "plugin.hello", map[string]any{"id": "session.test"}); err != nil || !res.OK {
		t.Fatalf("hello: %+v %v", res, err)
	}

	// ① 自己的会话：命中并带 session_id / message_id / snippet。
	res, err := cli.Call(ctx, "plugin/v1/session.search", map[string]any{
		"work_dir": wd, "query": "交付时间",
	})
	if err != nil || !res.OK {
		t.Fatalf("session.search: %+v %v", res, err)
	}
	val, _ := res.Value.(map[string]any)
	hits, _ := val["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("应命中 1 条自己的消息: %+v", val)
	}
	hit, _ := hits[0].(map[string]any)
	if hit["session_id"] != "s-alice" || hit["message_id"] != "m-s-alice" ||
		!strings.Contains(hit["snippet"].(string), "周五") {
		t.Fatalf("命中字段异常: %+v", hit)
	}

	// ② private（缺省）下点名 bob → permission_denied。
	res2, err := cli.Call(ctx, "plugin/v1/session.search", map[string]any{
		"work_dir": wd, "query": "交付时间", "targets": []string{"bob"},
	})
	if err == nil && res2.OK {
		t.Fatalf("private 下点名他人应被拒: %+v", res2)
	}
	joined := strings.Join(res2.Errors, "; ")
	if !strings.Contains(joined, "permission_denied") {
		t.Fatalf("错误应含 permission_denied: %q", joined)
	}
}
