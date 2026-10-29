// scope_test.go —— 三个作用域键（SCOPE-1）与「活动位归宿主」的守卫（SESSION-DB-2）。
package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectKey：项目键 = 归一化 work_dir（绝对化 + 清理 + 小写）；work_dir 为空 → 空串
// （不以窗口冒充项目，见 scope.go）。
func TestProjectKey(t *testing.T) {
	if got := projectKey(""); got != "" {
		t.Fatalf("空 work_dir 的项目键应为空，实际 %q", got)
	}
	if got := projectKey("   "); got != "" {
		t.Fatalf("空白 work_dir 的项目键应为空，实际 %q", got)
	}
	dir := t.TempDir()
	a, b := projectKey(dir), projectKey(strings.ToUpper(dir)+string(os.PathSeparator))
	if a == "" || a != b {
		t.Fatalf("同一项目的大小写/尾分隔符变体应归一到同一键：%q vs %q", a, b)
	}
	if other := projectKey(t.TempDir()); other == a {
		t.Fatalf("不同项目不应共用键：%q", other)
	}
}

// TestNoActiveSessionDirectRead（SESSION-DB-2）：`active_session_id` 是**宿主推送的 UI 运行态**，
// 归宿主/server 独占库（prjusr config；61 §3.2 `data-session-active-set` / `-active-get`）。
// llm-server **不得**跨组件直读该键（否则又把配置库当共享读源，破坏两 owner 边界）。
func TestNoActiveSessionDirectRead(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(b)
		for _, lit := range []string{"active_session_id", "data-session-active-get", "data-session-active-set"} {
			if strings.Contains(src, lit) {
				t.Fatalf("%s 直读了活动位（%q）：活动位只能由宿主推送，llm-server 不得跨组件读配置库", name, lit)
			}
		}
	}
}
