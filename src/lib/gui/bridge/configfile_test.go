// configfile_test.go — gui.file.save 单测（批 3 · ⑯ 配置导入/导出 + 恢复出厂）：
//   - mode=backup 落 <prjusr 数据根>/backup/<name>（导入前 / 恢复出厂前自动备份的唯一落点；
//     prjusr 根经 SetPrjUsrRoot 注入 = main 的 `data.PrjUsrDir` 结果，[24 §3.2] MW-8）
//   - 未注入 prjusr 根（-no-server 薄客户端）→ 回落 <workDir>/.chonkpilot/backup/
//   - 文件名净化（防路径穿越）+ 空名拒绝
//   - 内容逐字节落盘（快照 JSON 原样，不加工）
//
// 注：mode=dialog（系统「另存为」）为**模态框**，无法在单测中驱动 → 不覆盖（口径同 folder.PickFile）。
package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// callFileSave 以 gui.file.save 的 payload 形态直调实现。
func callFileSave(t *testing.T, b *Bridge, payload string) map[string]any {
	t.Helper()
	raw, err := callSaveConfigFile(b, context.Background(), []json.RawMessage{json.RawMessage(payload)})
	if err != nil {
		t.Fatalf("callSaveConfigFile(%s) 失败：%v", payload, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("返回非对象：%s", raw)
	}
	return m
}

// TestSaveConfigFileBackupIntoDataDir 显式 --data-dir：prjusr 根 = 数据根（main 注入同值）
// → 备份落 <数据根>/backup/ 且内容原样。
func TestSaveConfigFileBackupIntoDataDir(t *testing.T) {
	dataDir := t.TempDir()
	b := New("ins-1", t.TempDir(), dataDir, func(string) {}, nil)
	b.SetPrjUsrRoot(dataDir) // main 装配：显式 --data-dir → prjusr 根与 prj 同根
	const content = `{"app":"chonkpilot","data":{"theme":"dark"}}`

	payload, _ := json.Marshal(map[string]any{
		"name": "chonkpilot-config-backup-20260920-101112.json", "content": content, "mode": "backup",
	})
	got := callFileSave(t, b, string(payload))

	wantPath := filepath.Join(dataDir, "backup", "chonkpilot-config-backup-20260920-101112.json")
	if got["path"] != wantPath {
		t.Fatalf("落盘路径不符：got=%v want=%s", got["path"], wantPath)
	}
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("备份文件不存在：%v", err)
	}
	if string(body) != content {
		t.Fatalf("备份内容须原样：got=%s", body)
	}
	// 只落该文件：备份目录下不应有其它产物
	entries, _ := os.ReadDir(filepath.Join(dataDir, "backup"))
	if len(entries) != 1 {
		t.Fatalf("备份目录应仅 1 个文件，实际 %d", len(entries))
	}
}

// TestSaveConfigFileBackupIntoPrjUsrRoot B 方案（desktop 缺省 --data-dir）：prjusr 根 =
// `~/.chonkpilot/data/<prj-id>/`（main 经 data.PrjUsrDir 解析后注入，≠ <workDir>/.chonkpilot）
// → 自动备份随 prjusr 根落 <prjusr 根>/backup/（个人数据不进项目目录，[24 §3.2] MW-8）。
func TestSaveConfigFileBackupIntoPrjUsrRoot(t *testing.T) {
	workDir := t.TempDir()
	prjUsrRoot := t.TempDir()
	b := New("ins-1", workDir, "", func(string) {}, nil)
	b.SetPrjUsrRoot(prjUsrRoot)
	payload, _ := json.Marshal(map[string]any{"name": "b.json", "content": "{}", "mode": "backup"})
	got := callFileSave(t, b, string(payload))

	wantPath := filepath.Join(prjUsrRoot, "backup", "b.json")
	if got["path"] != wantPath {
		t.Fatalf("应落 prjusr 根 backup：got=%v want=%s", got["path"], wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("prjusr 根未落盘：%v", err)
	}
	// 项目数据根不得出现 backup/（个人运行态已迁走）
	if _, err := os.Stat(filepath.Join(workDir, ".chonkpilot", "backup")); err == nil {
		t.Fatalf("项目数据根不应出现 backup/（已随 prjusr 根迁移）")
	}
}

// TestSaveConfigFileBackupWithoutPrjUsrRoot 未注入 prjusr 根（-no-server 薄客户端）
// → 回落 <workDir>/.chonkpilot/backup/（与附件落盘同口径，见 uploadRoot）。
func TestSaveConfigFileBackupWithoutPrjUsrRoot(t *testing.T) {
	workDir := t.TempDir()
	b := New("ins-1", workDir, "", func(string) {}, nil)
	payload, _ := json.Marshal(map[string]any{"name": "b.json", "content": "{}", "mode": "backup"})
	got := callFileSave(t, b, string(payload))

	wantPath := filepath.Join(workDir, ".chonkpilot", "backup", "b.json")
	if got["path"] != wantPath {
		t.Fatalf("回落路径不符：got=%v want=%s", got["path"], wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("回落位置未落盘：%v", err)
	}
}

// TestSaveConfigFileBackupSanitizesName 文件名净化：绝对/相对路径穿越只取 base。
func TestSaveConfigFileBackupSanitizesName(t *testing.T) {
	dataDir := t.TempDir()
	b := New("ins-1", t.TempDir(), dataDir, func(string) {}, nil)
	b.SetPrjUsrRoot(dataDir)
	payload, _ := json.Marshal(map[string]any{
		"name": "../../evil.json", "content": "{}", "mode": "backup",
	})
	got := callFileSave(t, b, string(payload))

	gotPath, _ := got["path"].(string)
	wantPath := filepath.Join(dataDir, "backup", "evil.json")
	if gotPath != wantPath {
		t.Fatalf("须只取 base 名：got=%v want=%s", gotPath, wantPath)
	}
	if !strings.HasPrefix(gotPath, filepath.Join(dataDir, "backup")+string(filepath.Separator)) {
		t.Fatalf("落盘越界：%s", gotPath)
	}
}

// TestSaveConfigFileRequiresName 空名/纯分隔符名 → 报错（不静默落盘）。
func TestSaveConfigFileRequiresName(t *testing.T) {
	b := New("ins-1", t.TempDir(), t.TempDir(), func(string) {}, nil)
	for _, name := range []string{"", "   ", ".", "..", "./"} {
		payload, _ := json.Marshal(map[string]any{"name": name, "content": "{}", "mode": "backup"})
		if _, err := callSaveConfigFile(b, context.Background(),
			[]json.RawMessage{json.RawMessage(payload)}); err == nil {
			t.Fatalf("name=%q 应报错", name)
		}
	}
}
