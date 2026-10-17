package models

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDefaultWorkspace(t *testing.T) {
	base := t.TempDir()
	t.Setenv("APPDATA", base)
	t.Setenv("XDG_CONFIG_HOME", base)

	dir, err := EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("EnsureDefaultWorkspace: %v", err)
	}
	if filepath.Base(dir) != DefaultWorkspaceDirName {
		t.Fatalf("workspace dir = %q, want basename %q", dir, DefaultWorkspaceDirName)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat default workspace: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("default workspace is not a directory: %s", dir)
	}
}
