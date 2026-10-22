// instance-claim 的 desktop 分支行为（PRODSIM-1：本地认证面已整体删除）。
//
// 历史：本文件曾覆盖令牌鉴权（unauthorized）、允许根校验（forbidden）、用户提议交叉校验、
// 自建项目登记等用例 —— 随 `login-*` / auth 库 / `--project-roots` 一并移除。
// 现存唯一形态 = desktop（本机 UDS 前门免鉴权），故只保留 desktop 用例。
package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

// claimOnce 发一次 instance-claim，返回 (应答 result, v.Err())。
func claimOnce(t *testing.T, s *Server, payload map[string]any) (map[string]any, error) {
	t.Helper()
	v := s.bus.Emit(context.Background(), SubjectInstanceClaim, jb(payload)).Wait()
	res, _ := v.Result.(map[string]any)
	return res, v.Err()
}

// TestClaimDesktopSkipsAuth：`desktop`（Options.Form 空 = 默认构建）→
// **跳过鉴权与权限校验**（本机单用户）：
// PRODSIM-1：work_dir 取**客户端提议**，无提议 → forbidden（**不再回落启动参数**）。
func TestClaimDesktopSkipsAuth(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	if s.Form() != FormDesktop {
		t.Fatalf("默认形态应为 desktop，got %s", s.Form())
	}
	s.opts.WorkDir = testWorkDir
	s.opts.DataDir = filepath.Join(testWorkDir, ".chonkpilot")
	got := claimBroadcasts(t, s)

	// ① 无令牌、带 work_dir 提议 → 成功（work_dir = 提议）
	res, err := claimOnce(t, s, map[string]any{"instance_id": "ins-sa", "work_dir": testWorkDir})
	if err != nil {
		t.Fatalf("desktop 免鉴权应成功，got %v", err)
	}
	if res["work_dir"] != filepath.Clean(testWorkDir) {
		t.Fatalf("work_dir 应取提议 %s，got %v", testWorkDir, res["work_dir"])
	}
	if m := drainClaimBroadcast(t, got); m["instance_id"] != "ins-sa" {
		t.Fatalf("应照发 instance-register：%v", m)
	}
	// ② 无 work_dir 提议、无实例登记 → forbidden（不静默绑定启动参数）
	res2, _ := claimOnce(t, s, map[string]any{"instance_id": "ins-sa2"})
	if res2 == nil || res2["error"] != claimErrForbidden {
		t.Fatalf("desktop 无提议应 %s，got %v", claimErrForbidden, res2)
	}
}
