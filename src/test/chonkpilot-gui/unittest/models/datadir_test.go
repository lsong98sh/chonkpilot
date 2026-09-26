package models_test

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/models"
)

// TestDataDirDefault 未 SetDataDir 时返回 <workDir>/.chonkpilot（现状默认，兼容模式）。
func TestDataDirDefault(t *testing.T) {
	models.SetDataDir("")
	wd := filepath.Join(t.TempDir(), "proj")
	got := models.DataDir(wd)
	want := filepath.Join(wd, models.UserConfigDirName)
	if got != want {
		t.Fatalf("DataDir default = %q, want %q", got, want)
	}
}

// TestDataDirSet SetDataDir 后返回注册的数据根（与 workDir 无关）。
func TestDataDirSet(t *testing.T) {
	models.SetDataDir("")
	defer models.SetDataDir("")
	wd := filepath.Join(t.TempDir(), "proj")
	models.SetDataDir(`D:\data\proj`)
	if got := models.DataDir(wd); got != `D:\data\proj` {
		t.Fatalf("DataDir after Set = %q, want D:\\data\\proj", got)
	}
	// 重置后回退默认
	models.SetDataDir("")
	if got := models.DataDir(wd); got != filepath.Join(wd, models.UserConfigDirName) {
		t.Fatalf("DataDir after reset = %q, want default", got)
	}
}
