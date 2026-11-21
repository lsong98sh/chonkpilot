package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// TestNormKeyNormalizesSeparators：同一 work_dir 的反斜杠 / POSIX / 尾分隔符 / 大小写
// 形态必须归一到同一键。前端 FileTree / CodeView 以 POSIX 形态回填 work_dir，宿主
// `--work-dir` 为反斜杠形态；不归一会导致 Manager.Get 未命中 → filesys 报
// `work dir not bound for instance`（2026-11-21 全量 e2e）。
func TestNormKeyNormalizesSeparators(t *testing.T) {
	base := `E:\Work\Demo`
	posix := strings.ReplaceAll(base, `\`, `/`)
	cases := []string{
		base,
		posix,
		posix + "/",
		base + `\`,
		strings.ToUpper(posix),
		"  " + posix + "  ",
	}
	want := normKey(base)
	if want == "" {
		t.Fatal("normKey 返回空")
	}
	for _, in := range cases {
		if got := normKey(in); got != want {
			t.Errorf("normKey(%q) = %q，期望 %q", in, got, want)
		}
	}
	if got := normKey("   "); got != "" {
		t.Errorf("空白 work_dir 应归一为空，实际 %q", got)
	}
	// Clean 同时收掉 `.` / `..`（同一目录的另一种写法）。
	if got := normKey(`E:\Work\Demo\.`); got != want {
		t.Errorf("normKey(带 .) = %q，期望 %q", got, want)
	}
}

// TestGetAcceptsPosixWorkDirSpelling：实例注册（反斜杠）后用 POSIX 形态 Get 必须命中；
// 反向（POSIX 注册、反斜杠查询）同样成立。覆盖 filesys.resolveWorkDir 的实际调用口。
func TestGetAcceptsPosixWorkDirSpelling(t *testing.T) {
	m, bus, _ := newTestManager(t)
	dir := t.TempDir()
	posix := strings.ReplaceAll(dir, `\`, `/`)

	emit(t, bus, msgkeys.TopicInstanceRegister, map[string]any{
		"instance_id": "ins-spelling", "work_dir": dir,
	})
	if _, ok := m.Get(posix); !ok {
		t.Fatalf("POSIX 形态 Get(%q) 未命中（注册键 = %q）", posix, dir)
	}
	if _, ok := m.Get(strings.ToUpper(posix)); !ok {
		t.Fatalf("大小写形态 Get 未命中")
	}
	if _, ok := m.Get(dir + string(filepath.Separator)); !ok {
		t.Fatalf("尾分隔符形态 Get 未命中")
	}
}