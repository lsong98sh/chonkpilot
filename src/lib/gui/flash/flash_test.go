package flash

import (
	"path/filepath"
	"testing"
)

func TestDirResolverFallsBackToDefaultWorkspace(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	r := DirResolver{
		Exists: DirExists,
		Free:   func(string) bool { return true },
		// Picker 为空：若没有默认工作目录回落，Resolve 会返回 ErrNoProject。
	}
	got, err := r.Resolve("", nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if filepath.Base(got) != "workspace" {
		t.Fatalf("resolved dir = %q, want basename %q", got, "workspace")
	}
}
