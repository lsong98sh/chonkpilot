package systemfs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot 从本测试文件向上找到仓库根（含 src/initdata/capability/system）。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "src", "initdata", "capability", "system")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found from %s", file)
		}
		dir = parent
	}
}

// systemDocKinds 是需与实际出厂源逐字比对的 system 文档 kind（OP-04：含每类记忆提示词）。
func systemDocKinds() []string {
	kinds := []string{"summary", "system-directory"}
	for _, cat := range []string{
		"项目概要", "共同库", "开发规范", "构建发布规则", "接口库", "测试规范", "典型参照", "用户决策",
		"用户偏好", // 唯一用户级类别
	} {
		kinds = append(kinds, "memory/"+cat)
	}
	return kinds
}

// TestDocMatchesFactorySource：embed 落点必须与出厂唯一源
// `src/initdata/capability/system/<kind>.md` **逐字一致**（防漂移；漂移即跑构建脚本同步）。
func TestDocMatchesFactorySource(t *testing.T) {
	for _, kind := range systemDocKinds() {
		got, ok := Doc(kind)
		if !ok || got == "" {
			t.Fatalf("Doc(%s) = %q, ok=%v（embed 落点缺失）", kind, got, ok)
		}
		factory := filepath.Join(repoRoot(t), "src", "initdata", "capability", "system", kind+".md")
		raw, err := os.ReadFile(factory)
		if err != nil {
			t.Fatalf("read factory source: %v", err)
		}
		if want := strings.TrimSpace(string(raw)); got != want {
			t.Fatalf("embed 落点与出厂源漂移（%s）：\n embed=%q\nfactory=%q", kind, got, want)
		}
	}
}

// TestDocMissing：未知 kind / 空 kind → ("", false)。
func TestDocMissing(t *testing.T) {
	if s, ok := Doc("no-such-doc"); ok || s != "" {
		t.Fatalf("unknown kind = %q, %v; want \"\", false", s, ok)
	}
	if s, ok := Doc(""); ok || s != "" {
		t.Fatalf("empty kind = %q, %v; want \"\", false", s, ok)
	}
}
