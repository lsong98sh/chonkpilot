package exedir_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
)

func TestDir(t *testing.T) {
	dir, err := exedir.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("Dir() = %q, want absolute", dir)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir %q: %v", dir, err)
	}
	if !fi.IsDir() {
		t.Errorf("Dir() = %q, not a directory", dir)
	}
}
