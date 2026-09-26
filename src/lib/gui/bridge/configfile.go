// 配置快照写出（gui.file.save，批 3 · ⑯「配置 导入/导出 + 恢复出厂」新增的**唯一** native 能力）：
//
// 用途（两个 mode 覆盖全部落盘需求）：
//   - mode 缺省 / "dialog"：系统「另存为」对话框 → 用户选路径后写盘（**导出**）。
//   - mode "backup"：不弹框，直接落 `<prjusr 数据根>/backup/<name>`（**导入前 / 恢复出厂前的自动备份**）。
//
// 安全（硬要求）：
//   - 内容可能含 API Key 等敏感信息（llms[].apiKey / mcps[].env、headers）→
//     本文件**绝不记录 content**（无任何日志打印、不写错误详情），只回传落盘路径；
//   - 文件名经 filepath.Base 净化（防路径穿越），空名拒绝；
//   - 备份目录固定 `<prjusr 数据根>/backup/`（`uploadRoot()`：data_dir 空 → `<home>/.chonkpilot/data/<prj-id>/backup/`，
//     [24 §3.2] MW-8；未注入 prjusr 根 → 回落 `<workDir>/.chonkpilot/backup/`，与附件落盘同口径）。
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// backupSubdir 备份目录名（数据根下）：导入 / 恢复出厂前自动备份的落点。
const backupSubdir = "backup"

// saveConfigFileReq 是 gui.file.save 的入参。
type saveConfigFileReq struct {
	Name    string `json:"name"`    // 目标文件名（仅取 base，防穿越）
	Content string `json:"content"` // 快照 JSON 全文（**不记录**）
	Mode    string `json:"mode"`    // ""|dialog = 系统另存为；backup = 直接落备份目录
}

// callSaveConfigFile 实现 gui.file.save：
// 返回 {path}（成功 = 落盘绝对路径；用户取消 = ""）。
func callSaveConfigFile(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	var req saveConfigFileReq
	if len(params) > 0 {
		raw := params[0]
		// 兼容调用方误传单元素数组 [{...}]（与 upload 同法）
		if len(raw) > 0 && raw[0] == '[' {
			var arr []json.RawMessage
			if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
				raw = arr[0]
			}
		}
		_ = json.Unmarshal(raw, &req)
	}
	name := filepath.Base(strings.TrimSpace(req.Name))
	// 空名 / 纯分隔符（base 后为 "."）/ 上级目录（".."）→ 拒绝：`..` 经 Join 会清洗掉
	// `backup/` 段（落到数据根本身），故显式拦下（防路径穿越的硬要求）。
	if name == "" || name == "." || name == ".." {
		return nil, fmt.Errorf("file.save: name required")
	}

	if req.Mode == "backup" {
		// 落 <prjusr 数据根>/backup/（个人数据不进项目目录；[24 §3.2] MW-8）——复用
		// uploadRoot()（= main 注入的 `data.PrjUsrDir` 结果，与附件/截图同一落盘根）。
		dest := filepath.Join(b.uploadRoot(), backupSubdir, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return nil, fmt.Errorf("file.save: mkdir backup: %w", err)
		}
		if err := os.WriteFile(dest, []byte(req.Content), 0644); err != nil {
			return nil, fmt.Errorf("file.save: write backup: %w", err)
		}
		return json.Marshal(map[string]string{"path": dest})
	}

	// 系统「另存为」（纯 picker + 写盘；取消 → path 空串，与 pick-executable 同口径）
	dest, err := pickSaveFileName("Save Configuration", name,
		"JSON files\x00*.json\x00All files\x00*.*\x00\x00")
	if err != nil {
		return nil, fmt.Errorf("file.save: %w", err)
	}
	if dest == "" {
		return json.Marshal(map[string]string{"path": ""})
	}
	if err := os.WriteFile(dest, []byte(req.Content), 0644); err != nil {
		return nil, fmt.Errorf("file.save: write: %w", err)
	}
	return json.Marshal(map[string]string{"path": dest})
}
