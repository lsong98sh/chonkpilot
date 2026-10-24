// upload_test.go — 附件落盘根与二级目录（[24 §3.2] MW-8；61 §1 gui.upload「server 落盘、只回 name」）：
//   - 注入 prjusr 数据根 → 落 <prjusr 根>/<dir>/<name>；未注入 → 回落 <workDir>/.chonkpilot/<dir>/<name>；
//   - result 只回 {name}（不暴露服务器路径：不带 path/url/file_id）；
//   - payload.dir 决定二级目录（缺省 tmp/uploads；截图 screenshot）；穿越/超限 → 回落缺省。
package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// uploadCall 构造一次 gui.upload 调用载荷（base64 附件；dir 为空则不传 dir 字段）。
func uploadCall(t *testing.T, name string, content []byte, dir string) []json.RawMessage {
	t.Helper()
	body := map[string]any{
		"name": name,
		"data": base64.StdEncoding.EncodeToString(content),
		"kind": "file",
	}
	if dir != "" {
		body["dir"] = dir
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal upload req: %v", err)
	}
	return []json.RawMessage{raw}
}

// assertUploadedTo 断言上传结果只含 name，且文件落在 wantDir/<name> 且内容一致。
func assertUploadedTo(t *testing.T, out []byte, content []byte, wantDir string) {
	t.Helper()
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("upload result not json: %v (%s)", err, out)
	}
	name, _ := res["name"].(string)
	if name == "" {
		t.Fatalf("upload 未返回 name: %s", out)
	}
	// 只回 name（不暴露服务器路径）
	for _, k := range []string{"path", "url", "file_id"} {
		if _, ok := res[k]; ok {
			t.Fatalf("upload 结果不应含 %q（只回 name）: %s", k, out)
		}
	}
	dest := filepath.Join(wantDir, name)
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("读回附件 %s: %v", dest, err)
	}
	if string(got) != string(content) {
		t.Fatalf("附件内容不一致: %q", got)
	}
}

// TestUploadLandsInPrjUsrRoot：注入 prjusr 数据根（`<workDir>/.chonkpilot/users/<OS 用户名>`）后，
// 缺省 dir → 落 `<prjusr 根>/tmp/uploads`（不落项目目录）。
func TestUploadLandsInPrjUsrRoot(t *testing.T) {
	workDir := t.TempDir()
	prjUsrRoot := t.TempDir()
	content := []byte("attachment-body\n")

	b := New("ins-1", workDir, "", func(string) {}, nil)
	b.SetPrjUsrRoot(prjUsrRoot)

	out, err := callUploadAttachment(b, context.Background(), uploadCall(t, "a.txt", content, ""))
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
	out, err := callUploadAttachment(b, context.Background(), uploadCall(t, "b.txt", content, ""))
	if err != nil {
		t.Fatalf("callUploadAttachment: %v", err)
	}
	assertUploadedTo(t, out, content, filepath.Join(workDir, ".chonkpilot", "tmp", "uploads"))
}

// TestUploadDirScreenshot：payload.dir=screenshot → 落 <root>/screenshot（截图路径）。
func TestUploadDirScreenshot(t *testing.T) {
	workDir := t.TempDir()
	prjUsrRoot := t.TempDir()
	content := []byte("png-body")

	b := New("ins-1", workDir, "", func(string) {}, nil)
	b.SetPrjUsrRoot(prjUsrRoot)

	out, err := callUploadAttachment(b, context.Background(), uploadCall(t, "screenshot.png", content, "screenshot"))
	if err != nil {
		t.Fatalf("callUploadAttachment: %v", err)
	}
	assertUploadedTo(t, out, content, filepath.Join(prjUsrRoot, "screenshot"))
}

// TestUploadDirSanitized：dir 含穿越 / 绝对路径 / 层级超限 → 回落缺省 tmp/uploads（不逃逸数据根）。
func TestUploadDirSanitized(t *testing.T) {
	prjUsrRoot := t.TempDir()
	for _, dir := range []string{"../../evil", "C:/Windows", "a/b/c"} {
		b := New("ins-1", t.TempDir(), "", func(string) {}, nil)
		b.SetPrjUsrRoot(prjUsrRoot)
		content := []byte("x")
		out, err := callUploadAttachment(b, context.Background(), uploadCall(t, "n.txt", content, dir))
		if err != nil {
			t.Fatalf("callUploadAttachment(dir=%q): %v", dir, err)
		}
		assertUploadedTo(t, out, content, filepath.Join(prjUsrRoot, "tmp", "uploads"))
	}
	// 数据根外不得出现 evil 目录
	if _, err := os.Stat(filepath.Join(filepath.Dir(prjUsrRoot), "evil")); err == nil {
		t.Fatal("穿越 dir 逃出数据根")
	}
}
