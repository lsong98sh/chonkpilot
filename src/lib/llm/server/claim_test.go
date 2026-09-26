// instance-claim 黑盒（阶段 2a；61-消息一览 §4.1 ①）——**消息驱动**：Emit 发 `instance-claim` →
// 断言 ① v.Result（请求-响应应答）② 总线 `instance-register` 广播（照发、payload 不改）
// ③ 内存实例表（instance.Manager）登记。
//
// 覆盖：入口绑定（bridge/httpapi 注入 instance_id）+ 实例既有登记解析；无绑定 → 服务端生成
// uuid v4 + 启动参数回落；work_dir 提议与绑定不一致 → instance-forbidden（**不跳登录**，
// 走 2a 占位判定，真实「用户-项目权限」校验属 2b）。
package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// claimBroadcasts 订阅 instance-register 广播（claim 成功后服务端照发的观测口）。
func claimBroadcasts(t *testing.T, s *Server) chan map[string]any {
	t.Helper()
	got := make(chan map[string]any, 16)
	sub, err := s.bus.On("instance-register", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		select {
		case got <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe instance-register: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return got
}

// drainClaimBroadcast 取一条广播（带超时；无 → 失败）。
func drainClaimBroadcast(t *testing.T, got chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 instance-register 广播")
		return nil
	}
}

// assertNoBroadcast 断言窗口内无 instance-register 广播。
func assertNoBroadcast(t *testing.T, got chan map[string]any) {
	t.Helper()
	select {
	case m := <-got:
		t.Fatalf("不应广播 instance-register，却收到 %v", m)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestInstanceClaimEntryBound：入口绑定（payload 的 instance_id = 桥/入口注入的连接绑定）+
// 实例既有登记（启动期 instance-register）→ 应答 {instance_id, work_dir, data_dir} +
// 照发 instance-register + 内存实例表登记；重复认领幂等。
func TestInstanceClaimEntryBound(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset) // 数据层连接缓存先于 TempDir 清理关闭

	got := claimBroadcasts(t, s)
	dataDir := filepath.Join(testWorkDir, ".chonkpilot")
	const id = "ins-claim"
	s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": id, "client_type": "gui", "work_dir": testWorkDir, "data_dir": dataDir,
	}))
	drainClaimBroadcast(t, got) // 消费启动期登记（前置）

	// claim（desktop 口径：payload 省略 work_dir，仅带入口注入的 instance_id）
	v := s.bus.Emit(context.Background(), SubjectInstanceClaim, jb(map[string]any{"instance_id": id})).Wait()
	if v.Err() != nil {
		t.Fatalf("instance-claim 失败: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatalf("instance-claim 无应答（v.Result 为空）")
	}
	if res["instance_id"] != id {
		t.Fatalf("应答 instance_id = %v，期望 %s", res["instance_id"], id)
	}
	if res["work_dir"] != testWorkDir || res["data_dir"] != dataDir {
		t.Fatalf("应答绑定 = %v/%v，期望 %s/%s", res["work_dir"], res["data_dir"], testWorkDir, dataDir)
	}

	// claim 成功后照发 instance-register（主题与 payload 不改：data/filesys 据此登记，G-21）
	m := drainClaimBroadcast(t, got)
	if m["instance_id"] != id || m["work_dir"] != testWorkDir {
		t.Fatalf("instance-register 广播 = %v，期望 instance_id=%s work_dir=%s", m, id, testWorkDir)
	}

	// 内存实例表（{instance_id, work_dir, data_dir, lastBeat, client_type}）
	rec, ok := s.im.Lookup(id)
	if !ok || rec.WorkDir != testWorkDir || rec.DataDir != dataDir || rec.ClientType != "gui" {
		t.Fatalf("内存实例表登记 = %+v (ok=%v)", rec, ok)
	}
	if rec.LastBeat.IsZero() {
		t.Fatal("内存实例表应刷新 LastBeat")
	}

	// 重复认领幂等（同 id 不新增条目、仍成功）
	v2 := s.bus.Emit(context.Background(), SubjectInstanceClaim, jb(map[string]any{"instance_id": id})).Wait()
	if v2.Err() != nil {
		t.Fatalf("重复 instance-claim 失败: %v", v2.Err())
	}
	if res2, _ := v2.Result.(map[string]any); res2 == nil || res2["instance_id"] != id {
		t.Fatalf("重复认领应答异常: %v", v2.Result)
	}
	if s.im.Count() != 1 {
		t.Fatalf("重复认领不应新增实例，count=%d", s.im.Count())
	}
	drainClaimBroadcast(t, got) // 重复认领亦照发（幂等：消费方按 instance_id 覆盖）
}

// TestInstanceClaimWorkDirProposalForbidden：work_dir 提议与入口绑定不一致 →
// {ok:false, error:"instance-forbidden"}（同码进 errors），**不广播、不改写绑定**（不跳登录）。
func TestInstanceClaimWorkDirProposalForbidden(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)

	got := claimBroadcasts(t, s)
	const id = "ins-claim-f"
	s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": id, "client_type": "browser", "work_dir": testWorkDir,
	}))
	drainClaimBroadcast(t, got)

	v := s.bus.Emit(context.Background(), SubjectInstanceClaim, jb(map[string]any{
		"instance_id": id, "work_dir": filepath.Join(testWorkDir, "elsewhere"),
	})).Wait()
	res, _ := v.Result.(map[string]any)
	if res == nil || res["ok"] != false || res["error"] != claimErrForbidden {
		t.Fatalf("期望 {ok:false, error:%s}，got %v", claimErrForbidden, v.Result)
	}
	if v.Err() == nil || !strings.Contains(v.Err().Error(), claimErrForbidden) {
		t.Fatalf("错误码应同时进 errors（前端可读任一通道），got %v", v.Err())
	}
	assertNoBroadcast(t, got)
	if rec, ok := s.im.Lookup(id); !ok || rec.WorkDir != testWorkDir {
		t.Fatalf("绑定不应被提议改写: %+v (ok=%v)", rec, ok)
	}
}

// TestInstanceClaimGeneratesIDAndFallsBackToStartupParams：无入口绑定（裸发布方）→ 服务端
// 生成 uuid v4；work_dir/data_dir 回落**启动参数**（Options.WorkDir/DataDir，缺省 cwd 由宿主解析）。
func TestInstanceClaimGeneratesIDAndFallsBackToStartupParams(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)

	// 启动参数（宿主 flag 解析后注入；见 chonkpilot-llm/main.go）
	s.opts.WorkDir = testWorkDir
	s.opts.DataDir = filepath.Join(testWorkDir, ".chonkpilot")

	got := claimBroadcasts(t, s)
	v := s.bus.Emit(context.Background(), SubjectInstanceClaim, jb(map[string]any{})).Wait()
	if v.Err() != nil {
		t.Fatalf("instance-claim 失败: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatal("instance-claim 无应答")
	}
	id, _ := res["instance_id"].(string)
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("应生成 uuid v4，got %q", id)
	}
	if res["work_dir"] != testWorkDir {
		t.Fatalf("work_dir 应回落启动参数 %s，got %v", testWorkDir, res["work_dir"])
	}
	if res["data_dir"] != s.opts.DataDir {
		t.Fatalf("data_dir 应回落启动参数 %s，got %v", s.opts.DataDir, res["data_dir"])
	}
	if m := drainClaimBroadcast(t, got); m["instance_id"] != id {
		t.Fatalf("instance-register 广播 id 应与应答一致: %v vs %s", m["instance_id"], id)
	}
	if _, ok := s.im.Lookup(id); !ok {
		t.Fatal("生成 id 应写入内存实例表")
	}
}
