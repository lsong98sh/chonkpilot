package models_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/models"
)

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct{ in, want string }{
		{"~", home},
		{"~/data", filepath.Join(home, "data")},
		{`~\data`, filepath.Join(home, "data")},
		{"data", "data"},
		{"~foo", "~foo"}, // 非 ~/ 前缀不展开
		{"", ""},
	}
	for _, c := range cases {
		if got := models.ExpandHome(c.in); got != c.want {
			t.Fatalf("ExpandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	base := filepath.Join(t.TempDir(), "proj")

	// 绝对路径原样 Clean
	abs := filepath.Join(t.TempDir(), "abs", "dir")
	if got := models.ResolveDir(abs, base); got != filepath.Clean(abs) {
		t.Fatalf("ResolveDir absolute = %q, want %q", got, filepath.Clean(abs))
	}
	// 相对 base
	want := filepath.Clean(filepath.Join(base, "data"))
	if got := models.ResolveDir("data", base); got != want {
		t.Fatalf("ResolveDir relative = %q, want %q", got, want)
	}
	// ../ 跳出 base
	want = filepath.Clean(filepath.Join(base, "../x"))
	if got := models.ResolveDir("../x", base); got != want {
		t.Fatalf("ResolveDir parent = %q, want %q", got, want)
	}
	// ~ 展开
	want = filepath.Join(home, "data")
	if got := models.ResolveDir("~/data", base); got != want {
		t.Fatalf("ResolveDir home = %q, want %q", got, want)
	}
	// 空
	if got := models.ResolveDir("", base); got != "" {
		t.Fatalf("ResolveDir empty = %q, want empty", got)
	}
}
