package paths_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// TestNoInstanceErrors 缺 instance（宿主未注入调用上下文）→ 错误语义（决策 R-11 二次升级）：
// SetTempRoot("") 报错、不回落 default；未设置时 TempRoot/!/ 解析报错（非 panic）。
// 注意：本用例须先于任何 SetTempRoot 成功调用执行（全局 tempRoot 未设置）。
func TestNoInstanceErrors(t *testing.T) {
	t.Run("SetTempRoot 空 instance 报错", func(t *testing.T) {
		if got, err := paths.SetTempRoot(""); err == nil || got != "" {
			t.Fatalf("空 instance 应报错，got (%q,%v)", got, err)
		}
	})
	t.Run("TempRoot 未设置报错", func(t *testing.T) {
		if got, err := paths.TempRoot(); err == nil || got != "" {
			t.Fatalf("未设置应报错，got (%q,%v)", got, err)
		}
	})
	t.Run("!/ 缺 instance 返回统一错误消息", func(t *testing.T) {
		got, msg := paths.ResolvePath("!/x.txt", "")
		if got != "" || !strings.Contains(msg, "缺少 instance") {
			t.Fatalf("!/ 缺 instance 应报错，got (%q,%q)", got, msg)
		}
	})
}

// TestExpandHome 覆盖 ~ / ~/x / ~\x / ~foo（不展开）/ 空。
func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cases := []struct {
		in   string
		want string
	}{
		{"~", home},
		{"~/data", filepath.Join(home, "data")},
		{`~\data`, filepath.Join(home, "data")},
		{"~foo", "~foo"}, // 非 ~/ 前缀不展开
		{"c:/x", "c:/x"}, // 非 ~ 前缀原样
		{"", ""},         // 空原样
	}
	for _, c := range cases {
		if got := paths.ExpandHome(c.in); got != c.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestResolveDir 覆盖三规则：~ 展开 / 绝对原样 Clean / 相对 Join base（可 ../ 跳出）。
func TestResolveDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	base := `C:\work\proj`
	cases := []struct {
		raw  string
		base string
		want string
	}{
		{"", base, ""}, // 空 raw → 空
		{"~/data", "", filepath.Join(home, "data")}, // ~ 展开（与 base 无关）
		{`C:\tmp`, base, `C:\tmp`},                  // 绝对原样
		{`C:/tmp`, base, `C:\tmp`},                  // 绝对（/ 归一 Clean）
		{`data`, base, `C:\work\proj\data`},         // 相对 Join
		{`./data`, base, `C:\work\proj\data`},       // 相对 ./ 归一
		{`..\x`, base, `C:\work\x`},                 // ../ 可跳出 base（有意行为）
		{`data`, "", `data`},                        // base 空 → 相对按字面
	}
	for _, c := range cases {
		if got := paths.ResolveDir(c.raw, c.base); got != c.want {
			t.Errorf("ResolveDir(%q, %q) = %q, want %q", c.raw, c.base, got, c.want)
		}
	}
}

// TestResolvePath R-11 唯一入口：绝对 / ~ / !/ 合法；相对 + base 拼接；相对无 base 报错；空值原样空。
func TestResolvePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	tempRoot, err := paths.SetTempRoot("unittest-paths")
	if err != nil {
		t.Fatalf("SetTempRoot: %v", err)
	}

	t.Run("绝对路径 Clean 原样", func(t *testing.T) {
		got, msg := paths.ResolvePath(`C:/tmp/a.txt`, "")
		if msg != "" || got != `C:\tmp\a.txt` {
			t.Fatalf("got (%q,%q)", got, msg)
		}
	})
	t.Run("~ 开头展开", func(t *testing.T) {
		got, msg := paths.ResolvePath("~/data/x.txt", "")
		if msg != "" || got != filepath.Join(home, "data", "x.txt") {
			t.Fatalf("got (%q,%q)", got, msg)
		}
	})
	t.Run("!/ 前缀映射临时目录", func(t *testing.T) {
		got, msg := paths.ResolvePath("!/tmp.csv", "")
		if msg != "" || got != filepath.Join(tempRoot, "tmp.csv") {
			t.Fatalf("got (%q,%q)，want %q", got, msg, filepath.Join(tempRoot, "tmp.csv"))
		}
		if got, msg := paths.ResolvePath(`!\sub\x.bin`, ""); msg != "" || got != filepath.Join(tempRoot, "sub", "x.bin") {
			t.Fatalf("!\\ 前缀 got (%q,%q)", got, msg)
		}
	})
	t.Run("相对路径 + base 拼接（仅供 CLI 参数解析）", func(t *testing.T) {
		got, msg := paths.ResolvePath("data/x.txt", `C:\work\proj`)
		if msg != "" || got != `C:\work\proj\data\x.txt` {
			t.Fatalf("got (%q,%q)", got, msg)
		}
	})
	t.Run("相对路径无 base 报错且含原值与三种前缀提示", func(t *testing.T) {
		got, msg := paths.ResolvePath("src/main.py", "")
		if got != "" || msg == "" {
			t.Fatalf("相对无 base 应报错，got (%q,%q)", got, msg)
		}
		for _, part := range []string{"src/main.py", "绝对路径", "~/", "!/", "CHONKPILOT_WORKDIR"} {
			if !strings.Contains(msg, part) {
				t.Fatalf("消息缺 %q，got %q", part, msg)
			}
		}
	})
	t.Run("空值返回空", func(t *testing.T) {
		if got, msg := paths.ResolvePath("", ""); got != "" || msg != "" {
			t.Fatalf("空值应返回 (\"\",\"\")，got (%q,%q)", got, msg)
		}
	})
}

// TestTempRootPerInstance 缺口 4（2026-09-19）：临时根按 instance 分桶（多 instance 同进程互不覆盖）；
// 新增 instance 显式入口 TempRootFor / ResolvePathFor，与既有入口（SetTempRoot / TempRoot /
// ResolvePath）并存且单 instance 语义等价。
func TestTempRootPerInstance(t *testing.T) {
	rootA, err := paths.TempRootFor("ins-a")
	if err != nil {
		t.Fatalf("TempRootFor(ins-a): %v", err)
	}
	rootB, err := paths.TempRootFor("ins-b")
	if err != nil {
		t.Fatalf("TempRootFor(ins-b): %v", err)
	}
	if rootA == rootB {
		t.Fatalf("不同 instance 的临时根不应相同：%q == %q", rootA, rootB)
	}
	if want := filepath.Join(os.TempDir(), "chonkpilot", "ins-a"); rootA != want {
		t.Fatalf("TempRootFor(ins-a) = %q, want %q", rootA, want)
	}
	if st, err := os.Stat(rootB); err != nil || !st.IsDir() {
		t.Fatalf("instance 临时根应按需创建为目录: %v", err)
	}

	// 互不覆盖：SetTempRoot(a) 之后 b 的根/解析不受影响（旧实现是进程级单值 → 会被覆盖）
	if _, err := paths.SetTempRoot("ins-a"); err != nil {
		t.Fatalf("SetTempRoot(ins-a): %v", err)
	}
	if got, err := paths.TempRootFor("ins-b"); err != nil || got != rootB {
		t.Fatalf("SetTempRoot(a) 不应覆盖 b 的根：got (%q,%v), want %q", got, err, rootB)
	}
	if got, msg := paths.ResolvePathFor("ins-b", "!/x.txt", ""); msg != "" || got != filepath.Join(rootB, "x.txt") {
		t.Fatalf("ResolvePathFor(b, !/x.txt) = (%q,%q), want %q", got, msg, filepath.Join(rootB, "x.txt"))
	}
	// 兼容旧入口：SetTempRoot(a) → TempRoot()/ResolvePath 仍是 a 的根（单 instance 语义等价）
	if got, err := paths.TempRoot(); err != nil || got != rootA {
		t.Fatalf("TempRoot() = (%q,%v), want %q", got, err, rootA)
	}
	if got, msg := paths.ResolvePath("!/x.txt", ""); msg != "" || got != filepath.Join(rootA, "x.txt") {
		t.Fatalf("ResolvePath(!/x.txt) = (%q,%q), want %q", got, msg, filepath.Join(rootA, "x.txt"))
	}
	// 同一 instance 幂等；非 !/ 路径与 instance 无关（委托 ResolvePath）
	if again, err := paths.TempRootFor("ins-a"); err != nil || again != rootA {
		t.Fatalf("TempRootFor 应幂等：got (%q,%v), want %q", again, err, rootA)
	}
	if got, msg := paths.ResolvePathFor("ins-b", `C:/tmp/a.txt`, ""); msg != "" || got != `C:\tmp\a.txt` {
		t.Fatalf("非 !/ 路径应与 instance 无关：(%q,%q)", got, msg)
	}
	// 缺 instance → 与旧语义一致的错误（不 panic）
	if got, err := paths.TempRootFor(""); err == nil || got != "" {
		t.Fatalf("TempRootFor(\"\") 应报错，got (%q,%v)", got, err)
	}
	if got, msg := paths.ResolvePathFor("", "!/x.txt", ""); got != "" || !strings.Contains(msg, "缺少 instance") {
		t.Fatalf("ResolvePathFor(\"\", !/x.txt) 应报错，got (%q,%q)", got, msg)
	}
}

// TestTempRoot instance 分目录 + 空 instance 报错（不回落 default）+ 幂等。
func TestTempRoot(t *testing.T) {
	r1, err := paths.SetTempRoot("ins-abc")
	if err != nil {
		t.Fatalf("SetTempRoot: %v", err)
	}
	want := filepath.Join(os.TempDir(), "chonkpilot", "ins-abc")
	if r1 != want {
		t.Fatalf("SetTempRoot = %q, want %q", r1, want)
	}
	if got, gerr := paths.TempRoot(); gerr != nil || got != want {
		t.Fatalf("TempRoot = (%q,%v), want (%q,nil)", got, gerr, want)
	}
	if st, err := os.Stat(r1); err != nil || !st.IsDir() {
		t.Fatalf("临时根应已创建为目录: %v", err)
	}
	// 空 instance → 报错，不再回落 default
	if got, err := paths.SetTempRoot(""); err == nil || got != "" {
		t.Fatalf("空 instance 应报错，got (%q,%v)", got, err)
	}
	// 幂等：同一 instance 重复调用返回同一路径
	if got, err := paths.SetTempRoot("ins-abc"); err != nil || got != want {
		t.Fatalf("重复 SetTempRoot 应幂等，got (%q,%v)", got, err)
	}
}

// TestLogicalPath（G-24）DB 逻辑路径双向转换：落库前 ToLogical（workdir 相对 + 斜杠归一），
// 读取/展示前 FromLogical（还原绝对）。workdir 之外与带 scheme 的引用保持原样。
func TestLogicalPath(t *testing.T) {
	wd := `C:\work\proj`
	logicalCases := []struct {
		name string
		in   string
		want string
	}{
		{"workdir 内绝对 → 相对", `C:\work\proj\src\a.txt`, "src/a.txt"},
		{"workdir 内斜杠写法 → 相对", `C:/work/proj/src/a.txt`, "src/a.txt"},
		{"workdir 本身 → 空", wd, ""},
		{"workdir 外绝对 → 原样", `C:\other\x.txt`, `C:\other\x.txt`},
		{"带 scheme → 原样", "db://kb/x", "db://kb/x"},
		{"空 → 空", "", ""},
		{"已是相对 → 斜杠归一原样", `src\a.txt`, "src/a.txt"},
		{"workdir 空 → 原样", `C:\work\proj\a.txt`, `C:\work\proj\a.txt`},
	}
	for _, c := range logicalCases {
		wdIn := wd
		if c.name == "workdir 空 → 原样" {
			wdIn = ""
		}
		if got := paths.ToLogical(wdIn, c.in); got != c.want {
			t.Errorf("ToLogical(%q, %q) = %q, want %q", wdIn, c.in, got, c.want)
		}
	}
	cases := []struct {
		name string
		wd   string
		in   string
		want string
	}{
		{"相对 → 绝对", wd, "src/a.txt", `C:\work\proj\src\a.txt`},
		{"绝对（旧数据）→ 原样", wd, `C:\other\x.txt`, `C:\other\x.txt`},
		{"带 scheme → 原样", wd, "db://kb/x", "db://kb/x"},
		{"空 → 空", wd, "", ""},
		{"workdir 空 → 原样", "", "src/a.txt", "src/a.txt"},
	}
	for _, c := range cases {
		if got := paths.FromLogical(c.wd, c.in); got != c.want {
			t.Errorf("FromLogical(%q, %q) = %q, want %q", c.wd, c.in, got, c.want)
		}
	}
	// 回环：FromLogical(ToLogical(abs)) == abs（workdir 内）
	abs := filepath.Join(wd, "src", "a.txt")
	if got := paths.FromLogical(wd, paths.ToLogical(wd, abs)); got != abs {
		t.Fatalf("回环：ToLogical→FromLogical = %q, want %q", got, abs)
	}
}
