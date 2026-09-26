// upload_test.go — 附件落盘根（[24 §3.2] MW-8）：
// 注入 prjusr 数据根 → 落 <prjusr 根>/tmp/uploads；未注入 → 回落 <workDir>/.chonkpilot/tmp/uploads。
package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// uploadCall 构造一次 gui.upload 调用载荷（base64 附件）。
func uploadCall(t *testing.T, name string, content []byte) []json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name": name,
		"data": base64.StdEncoding.EncodeToString(content),
		"kind": "file",
	})
	if err != nil {
		t.Fatalf("marshal upload req: %v", err)
	}
	return []json.RawMessage{raw}
}

// assertUploadedTo 断言上传结果落在 wantDir 且内容一致。
func assertUploadedTo(t *testing.T, out []byte, content []byte, wantDir string) {
	t.Helper()
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("upload result not json: %v (%s)", err, out)
	}
	dest, _ := res["path"].(string)
	if dest == "" {
		t.Fatalf("upload 未返回 path: %s", out)
	}
	if got := filepath.Dir(dest); got != wantDir {
		t.Fatalf("附件落点 = %q，want %q", got, wantDir)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("读回附件: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("附件内容不一致: %q", got)
	}
	// url 供 /show/ 预览（同名 url 与 path 对应）
	if url, _ := res["url"].(string); url == "" {
		t.Fatal("upload 未返回 url")
	}
}

// TestUploadLandsInPrjUsrRoot：注入 prjusr 数据根（B 方案：~/.chonkpilot/data/<prj-id>）后，
// 附件落 `<prjusr 根>/tmp/uploads`（不落项目目录）。
func TestUploadLandsInPrjUsrRoot(t *testing.T) {
	workDir := t.TempDir()
	prjUsrRoot := t.TempDir()
	content := []byte("attachment-body\n")

	b := New("ins-1", workDir, "", func(string) {}, nil)
	b.SetPrjUsrRoot(prjUsrRoot)

	out, err := callUploadAttachment(b, context.Background(), uploadCall(t, "a.txt", content))
	if err != nil {
		t.Fatalf("callUploadAttachment: %v", err)
	}
	assertUploadedTo(t, out, content, filepath.Join(prjUsrRoot, "tmp", "uploads"))
	// 项目数据根不得被写入
	if _, err := os.Stat(filepath.Join(workDir, ".chonkpilot", "tmp")); err == nil {
		t.Fatal("附件不应落项目数据根（<workDir>/.chonkpilot/tmp）")
	}
}

// TestUploadFallsBackToWorkDirDataRoot：未注入 prjusr 根（薄客户端/独立形态宿主未解析）→
// 回落 `<workDir>/.chonkpilot/tmp/uploads`（旧行为，不回归）。
func TestUploadFallsBackToWorkDirDataRoot(t *testing.T) {
	workDir := t.TempDir()
	content := []byte("fallback-body\n")

	b := New("ins-1", workDir, "", func(string) {}, nil)
	out, err := callUploadAttachment(b, context.Background(), uploadCall(t, "b.txt", content))
	if err != nil {
		t.Fatalf("callUploadAttachment: %v", err)
	}
	assertUploadedTo(t, out, content, filepath.Join(workDir, ".chonkpilot", "tmp", "uploads"))
}
