package mgr

import (
	"context"
	"errors"
	"testing"
)

// runningPlugin 造一个运行中的插件条目（测试用：不启进程、不读 manifest 文件）。
func runningPlugin(id, version string) *Plugin {
	return &Plugin{ID: id, Manifest: &Manifest{ID: id, Version: version}, status: StatusRunning}
}

// TestUIAssetHit：宿主 → 插件请求 `plugin/v1/ui.asset.get` → base64 往返后字节一致。
func TestUIAssetHit(t *testing.T) {
	const body = "<html>config</html>"
	var gotMethod, gotPath string
	m := New(Options{RequestPlugin: func(_ context.Context, id, method string, payload any) (any, error) {
		if id != "codebase" {
			t.Fatalf("id=%s", id)
		}
		gotMethod = method
		if pm, ok := payload.(map[string]any); ok {
			gotPath, _ = pm["path"].(string)
		}
		// 与真实桥一致：返回经 JSON 编码的载荷（[]byte → base64 字符串）。
		return map[string]any{"data": []byte(body), "size": len(body)}, nil
	}})
	m.plugins["codebase"] = runningPlugin("codebase", "1.0.0")

	data, found, err := m.UIAsset(context.Background(), "codebase", "config.html")
	if err != nil || !found {
		t.Fatalf("UIAsset: found=%v err=%v", found, err)
	}
	if gotMethod != MethodUIAssetGet || gotPath != "config.html" {
		t.Fatalf("method=%q path=%q", gotMethod, gotPath)
	}
	if string(data) != "<html>config</html>" {
		t.Fatalf("data=%q", string(data))
	}
}

// TestUIAssetNotReady：未登记 / 未就绪 / 插件返回错误 → found=false（宿主按 404 处理）。
func TestUIAssetNotReady(t *testing.T) {
	m := New(Options{RequestPlugin: func(context.Context, string, string, any) (any, error) {
		return nil, errors.New("not_found: nope.html")
	}})
	if _, found, err := m.UIAsset(context.Background(), "ghost", "a.html"); found || err == nil {
		t.Fatalf("未登记插件应 found=false 且有 err")
	}
	m.plugins["codebase"] = &Plugin{ID: "codebase", Manifest: &Manifest{Version: "1.0.0"}, status: StatusStarting}
	if _, found, err := m.UIAsset(context.Background(), "codebase", "a.html"); found || err == nil {
		t.Fatalf("未就绪插件应 found=false 且有 err")
	}
	m.plugins["codebase"].status = StatusRunning
	if _, found, err := m.UIAsset(context.Background(), "codebase", "nope.html"); found || err == nil {
		t.Fatalf("插件报错应 found=false 且有 err")
	}
}

// TestUIAssetVersion：版本分量 = manifest.version + 运行代次（重启即变）。
func TestUIAssetVersion(t *testing.T) {
	m := New(Options{})
	if got := m.UIAssetVersion("ghost"); got != "" {
		t.Fatalf("未登记插件 version=%q want 空", got)
	}
	p := runningPlugin("codebase", "1.0.0")
	m.plugins["codebase"] = p
	if got := m.UIAssetVersion("codebase"); got != "1.0.0#0" {
		t.Fatalf("初版 version=%q want 1.0.0#0", got)
	}
	p.mu.Lock()
	p.gen++
	p.mu.Unlock()
	if got := m.UIAssetVersion("codebase"); got != "1.0.0#1" {
		t.Fatalf("重启后 version=%q want 1.0.0#1", got)
	}
}
