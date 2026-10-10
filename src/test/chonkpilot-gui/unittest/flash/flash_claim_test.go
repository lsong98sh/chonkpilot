// flash_claim_test.go — flash UDS 认领 / 置顶（`flash.DialClaim` / `flash.BringToTop`）验证
// （🆕 2026-10-10；61 §4.1 ①⑥）。
//
// 覆盖：claim 上行携带必填 `user` + `work_dir`、应答解析（含缺省回落）；跨用户 → instance-forbidden。
package flash_test

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/flash"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/uds"
)

// claimFront 是最小 UDS 前门：实例化 claim 绑定连接；记录收到的上行载荷。
type claimFront struct {
	srv *uds.Server

	mu     sync.Mutex
	claims []map[string]any
}

func startClaimFront(t *testing.T, path string, forbidden bool) *claimFront {
	t.Helper()
	f := &claimFront{}
	f.srv = uds.NewServer(uds.ServerOptions{
		Path: path,
		OnRequest: func(c *uds.Conn, typ string, payload json.RawMessage) uds.Response {
			switch typ {
			case msgkeys.TopicInstanceClaim:
				var m map[string]any
				_ = json.Unmarshal(payload, &m)
				f.mu.Lock()
				f.claims = append(f.claims, m)
				f.mu.Unlock()
				c.SetInstance("i-flash")
				return uds.Response{OK: true, Result: map[string]any{
					msgkeys.InstanceClaimResultInstanceId: "i-flash",
					msgkeys.InstanceClaimResultWorkDir:    "C:/wd",
					msgkeys.InstanceClaimResultDataDir:    "C:/wd/.chonkpilot",
				}}
			case msgkeys.TopicInstanceBringToTop:
				if forbidden {
					return uds.Response{OK: false, Errors: []string{"instance-forbidden"}}
				}
				return uds.Response{OK: true, Result: map[string]any{"ok": true}}
			default:
				return uds.Response{OK: true}
			}
		},
	})
	if err := f.srv.Start(); err != nil {
		t.Fatalf("uds server start: %v", err)
	}
	t.Cleanup(func() { _ = f.srv.Close() })
	return f
}

func (f *claimFront) lastClaim() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.claims) == 0 {
		return nil
	}
	return f.claims[len(f.claims)-1]
}

// TestDialClaimCarriesUserAndWorkDir：claim 上行带必填 `user`（本机 OS 用户名）+ `work_dir`，
// 并解析出 instance_id / work_dir / data_dir。
func TestDialClaimCarriesUserAndWorkDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	f := startClaimFront(t, path, false)

	cli, claim, err := flash.DialClaim(flash.ClaimOptions{WorkDir: "C:/wd", UDSPath: path})
	if err != nil {
		t.Fatalf("DialClaim: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	if claim.InstanceID != "i-flash" || claim.WorkDir != "C:/wd" || claim.DataDir != "C:/wd/.chonkpilot" {
		t.Fatalf("claim = %+v", claim)
	}
	m := f.lastClaim()
	if m == nil {
		t.Fatal("前门未收到 instance-claim")
	}
	if u, _ := m[msgkeys.InstanceClaimPayloadUser].(string); u == "" {
		t.Fatalf("claim 缺必填 user: %#v", m)
	}
	if wd, _ := m[msgkeys.InstanceClaimPayloadWorkDir].(string); wd != "C:/wd" {
		t.Fatalf("claim work_dir = %q，期望 C:/wd", wd)
	}
}

// TestDialClaimNoServer：UDS 不存在 → 明确错误（提示先启动服务端）。
func TestDialClaimNoServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")
	if _, _, err := flash.DialClaim(flash.ClaimOptions{WorkDir: "C:/wd", UDSPath: path}); err == nil {
		t.Fatal("无 server 时应返回错误")
	}
}

// TestBringToTopForbidden：跨用户 → 前门回 instance-forbidden，且 IsForbidden 判定为真。
func TestBringToTopForbidden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	startClaimFront(t, path, true)
	err := flash.BringToTop("C:/wd", path, nil)
	if err == nil {
		t.Fatal("跨用户应返回错误")
	}
	if !flash.IsForbidden(err) {
		t.Fatalf("应为 instance-forbidden，得 %v", err)
	}
}

// TestBringToTopOK：同用户 → 置顶请求成功（nil）。
func TestBringToTopOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	startClaimFront(t, path, false)
	if err := flash.BringToTop("C:/wd", path, nil); err != nil {
		t.Fatalf("BringToTop: %v", err)
	}
}
