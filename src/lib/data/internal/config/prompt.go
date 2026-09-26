// summary_prompt 文件化（12-数据层）：上下文压缩摘要提示词不再存 prj config，
// 改为文件 `<级别>/capability/prompts/summary.prompt.md`（系统 + 项目两级，用户级不使用）。
//
// 读写经既有 `data-prompt-{load,save,delete}` 消息面（key = summary_prompt）——**契约不变**，
// 仅存储侧由 config 表 key 改为文件；文件形态 = *.prompt.md 原语文档（与知识库原语同构，
// 便于文件树 / PrimitivePanel 展示与编辑）。
//
// 阶段 4「internal 下沉」：本文件由 `chonkpilot-data/persist` 整体下移（逻辑逐字未改；
// load/save/delete 分发在 config_facade.go 的 ConfigKV* 方法内）。
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
)

// summaryPromptKey 是 prompt 域中走文件存储的 key（其余 key 仍走 prj config 表）。
const summaryPromptKey = "summary_prompt"

// summaryPromptFileName 独立提示词文件名（prompts 根下，12-数据层）。
const summaryPromptFileName = "summary.prompt.md"

// summaryPromptTitle 文件内原语文档标题。
const summaryPromptTitle = "Summary Prompt"

// summaryPromptFile 项目级文件路径（workDir 空 → 空串，走系统级）。
func summaryPromptFile(workDir string) string {
	if workDir == "" {
		return ""
	}
	return filepath.Join(capfs.PromptsRoot(capfs.ProjectRoot(workDir)), summaryPromptFileName)
}

// readSummaryPrompt 读摘要提示词：项目文件 → 系统文件 → 旧 prj config key（兼容旧数据）；
// 全部缺失/为空 → 内置默认（与压缩插件实际回落同源，见 chonkpilot-data.DefaultSummaryPrompt），
// 保证设置页显示的"有效默认"与实际生效值一致（I-65 ⑧）。
func (s *Service) readSummaryPrompt(workDir, instanceID string) string {
	if p := summaryPromptFile(workDir); p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			return capfs.ParseDoc(string(raw)).Content
		}
	}
	return s.inheritedSummaryPrompt(instanceID)
}

// inheritedSummaryPrompt 取**继承值**（不含项目级文件）：系统级文件 → 旧 prj config key → 内置默认。
// 用于 load 回落与 save 的"是否与继承值相同"判定（见 writeSummaryPrompt）。
func (s *Service) inheritedSummaryPrompt(instanceID string) string {
	if sys, err := capfs.SystemRoot(s.AppDir); err == nil {
		if raw, err := os.ReadFile(filepath.Join(capfs.PromptsRoot(sys), summaryPromptFileName)); err == nil {
			return capfs.ParseDoc(string(raw)).Content
		}
	}
	if v := s.legacyConfigValue(instanceID, "prompt-"+summaryPromptKey); v != "" {
		return v
	}
	return data.DefaultSummaryPrompt
}

// writeSummaryPrompt 写项目级文件（P0 修复：消除写放大）：
//   - 内容为空 → 删除项目级文件（回落系统级，既有语义）；
//   - 内容与**继承值**（系统文件 → 旧 prj config → 内置默认）完全相同 → 同样删除项目级文件
//     （保持继承）——设置页回填的是"有效值"（可能来自系统级/内置默认），点保存即固化为项目级
//     副本会**永久遮蔽**系统级后续更新；相同即不覆盖；
//   - 不同 → 写项目级覆盖。
func (s *Service) writeSummaryPrompt(workDir, instanceID, content string) error {
	p := summaryPromptFile(workDir)
	if p == "" {
		return nil
	}
	if strings.TrimSpace(content) == "" || strings.TrimSpace(content) == strings.TrimSpace(s.inheritedSummaryPrompt(instanceID)) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(capfs.BuildDoc(capfs.Doc{Title: summaryPromptTitle, Content: content})), 0o644)
}
