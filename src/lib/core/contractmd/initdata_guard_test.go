package contractmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitdataAssetsAreV2 守卫：出厂 capability 资产全部为 v2 契约且可严格解析
// （缺失目录 → 跳过；供「一次性迁移」不回归）。system/ 下为非契约纯文本，跳过。
func TestInitdataAssetsAreV2(t *testing.T) {
	base := filepath.Join("..", "..", "..", "initdata", "capability")
	if _, err := os.Stat(base); err != nil {
		t.Skipf("initdata dir not found (%s): %v", base, err)
	}
	count := 0
	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "system" {
				return filepath.SkipDir
			}
			return nil
		}
		if TypeOfFileName(p) == nil {
			return nil // 非原语（含 system/ 已跳过）
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if _, err := Parse(string(raw)); err != nil {
			t.Errorf("asset %s is not valid v2: %v", p, err)
		}
		if !strings.Contains(string(raw), "# "+SecMeta) {
			t.Errorf("asset %s missing %q section", p, SecMeta)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if count == 0 {
		t.Fatalf("no primitive assets found under %s", base)
	}
	t.Logf("validated %d assets", count)
}
