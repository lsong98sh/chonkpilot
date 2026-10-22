package models

// 「最近项目」记录（PRODSIM-1：原 flash 包的 recents 辅助迁入 models —— flash 薄壳已删除，
// 但托盘「项目列表」与 gui.dir.open 仍需要这份最近目录清单）。
//
// 落盘位置 = `<home>/.chonkpilot/flash-recents.json`（兼容既有文件，不改名以免丢历史记录）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// recentsFileName 是「最近项目」记录文件名（落用户数据根 `<home>/.chonkpilot/`）。
const recentsFileName = "flash-recents.json"

// RecentsCap 是「最近项目」记录上限（对齐既有 recent_dirs 的 10 条截断）。
const RecentsCap = 10

// RecentsPath 返回「最近项目」记录文件路径：`<home>/.chonkpilot/flash-recents.json`
// （home 不可解析 → 回落系统临时目录）。
func RecentsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".chonkpilot", recentsFileName)
}

// LoadRecents 读取「最近项目」列表（最近在前）；文件缺失/损坏 → nil（不阻断启动）。
func LoadRecents(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var dirs []string
	if json.Unmarshal(b, &dirs) != nil {
		return nil
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if s := strings.TrimSpace(d); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SaveRecent 把 dir 置为最近并落盘（去重、上限 RecentsCap，最近在前）。
func SaveRecent(path, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	kept := []string{dir}
	for _, d := range LoadRecents(path) {
		if strings.EqualFold(d, dir) {
			continue
		}
		kept = append(kept, d)
	}
	if len(kept) > RecentsCap {
		kept = kept[:RecentsCap]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// DirExists 判定路径存在且为目录。
func DirExists(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}
