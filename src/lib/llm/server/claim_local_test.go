package server

import (
	"path/filepath"
	"testing"
)

// TestResolveClaimTargetLocalFallsBackToBoundWorkDir 覆盖 flash 黑屏根因的修复：
// UDS 前门（`__local=true`）下，客户端**未提议 work_dir** 时须回落**连接已绑定实例的
// work_dir**（`instWorkDir`；flash 薄壳的 SPA 按 desktop 语义认领不带 work_dir，其 work_dir
// 已由 flash 自身 DialClaim 绑定并经 instance-register 登记）。缺失回落会让免鉴权的本地前端
// 认领被误判 instance-forbidden → 前端停在“无权访问该工作目录”前置视图（= 全黑）。
//
// ④ 覆盖收紧口径（P1-1）：回落**只认实例绑定**，**不**落 server 启动参数（`boundWorkDir`
// —— 独立 server 下 = 安装目录），否则实例未绑定时会静默绑定安装目录（fail-open）。
func TestResolveClaimTargetLocalFallsBackToBoundWorkDir(t *testing.T) {
	s := &Server{}
	bound := filepath.Join("C:", "proj")

	// ① 未提议 + 实例已绑定 → 回落实例绑定（修复点）。
	wd, dd, code := s.resolveClaimTarget(&claimReq{Local: true}, bound, "", bound)
	if code != "" {
		t.Fatalf("未提议 work_dir 应回落实例绑定并放行，got code=%q", code)
	}
	if wd != bound {
		t.Fatalf("work_dir = %q，期望回落为实例绑定值 %q", wd, bound)
	}
	if want := filepath.Join(bound, ".chonkpilot"); dd != want {
		t.Fatalf("data_dir = %q，期望 %q", dd, want)
	}

	// ② 有提议 → 提议优先（不回落）。
	proposed := filepath.Join("D:", "other")
	wd2, dd2, code2 := s.resolveClaimTarget(&claimReq{Local: true, WorkDir: proposed}, bound, "", bound)
	if code2 != "" || wd2 != filepath.Clean(proposed) {
		t.Fatalf("有提议应以提议为准：wd=%q code=%q", wd2, code2)
	}
	if want := filepath.Join(proposed, ".chonkpilot"); dd2 != want {
		t.Fatalf("data_dir = %q，期望 %q", dd2, want)
	}

	// ③ 提议与实例绑定皆空 → 明确失败（不静默绑定空目录）。
	if _, _, code3 := s.resolveClaimTarget(&claimReq{Local: true}, "", "", ""); code3 != claimErrForbidden {
		t.Fatalf("无提议且无实例绑定应为 %s，got %q", claimErrForbidden, code3)
	}

	// ④ 实例未绑定、但 server 启动参数（boundWorkDir = 安装目录）非空 → 仍 forbidden
	//    （收紧点：回落只认实例绑定，**不**落启动参数 → 不静默绑定安装目录）。
	if _, _, code4 := s.resolveClaimTarget(&claimReq{Local: true}, filepath.Join("C:", "srvInstall"), "", ""); code4 != claimErrForbidden {
		t.Fatalf("实例未绑定时不得回落启动参数，应 %s，got %q", claimErrForbidden, code4)
	}
}
