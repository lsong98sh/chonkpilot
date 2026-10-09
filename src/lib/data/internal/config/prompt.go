// prompt 域文件化（12-数据层；OP-01/OP-02/OP-04，2026-10-06）：
// 上下文压缩摘要提示词与记忆类别沉淀提示词改存**纯文本系统文档**
// `<级别>/capability/system/<kind>.md`（kind = `summary` / `memory/<类别名>`；无 `[content]`
// 分区、无 `.prompt` 后缀；系统 + 用户 + 项目三级，项目级私有级不使用）。
//
// 读取链（OP-02）= 项目级文件 → 用户级文件 → 系统级文件 → **embed 内置**
// （`data.SystemDoc(kind)`，= 出厂文件 `src/initdata/capability/system/<kind>.md`）。
// 读写经既有 `data-prompt-{load,save,delete}` 消息面（key = `summary_prompt` /
// `memory_prompt.<类别名>`）——**契约不变**，仅存储侧由 config 表 key 改为文件。
//
// 写级别（OP-04）：摘要与**项目级**记忆类别 → 项目级文件；唯一用户级记忆类别「用户偏好」
// （`facade.MemoryUserCategory`）→ **用户级文件**（跨项目）。空串 / 与继承值相同 = 删覆盖
// 文件回落继承（既有语义，避免把有效值固化为覆盖件永久遮蔽上级更新）。
//
// 阶段 4「internal 下沉」：本文件由 `chonkpilot-data/persist` 整体下移（load/save/delete 分发在
// config_facade.go 的 ConfigKV* 方法内）。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
)

const (
	// summaryPromptKey 是摘要提示词的文件化 key。
	summaryPromptKey = "summary_prompt"
	// memoryPromptPrefix 是**记忆类别沉淀提示词**的文件化 key 前缀：`memory_prompt.<类别名>`
	// （值 = 提示词全文；键缺失/空 → 回落继承链；OP-04 取代旧 prj `memory.prompt.<类别名>`）。
	memoryPromptPrefix = "memory_prompt."
)

// systemDocKind 按 prompt 域 key 解析 system 文档 kind（非文件化键 → ok=false）：
//   - `summary_prompt` → `summary`
//   - `memory_prompt.<类别名>` → `memory/<类别名>`（类别名须过 capfs.ValidMemoryCategoryName，A-37：
//     否则含 `..` / 分隔符的类别名会被拼进路径 → 越过 system 目录读写）
func systemDocKind(key string) (string, bool) {
	if key == summaryPromptKey {
		return "summary", true
	}
	if cat, ok := strings.CutPrefix(key, memoryPromptPrefix); ok && capfs.ValidMemoryCategoryName(cat) {
		return "memory/" + cat, true
	}
	return "", false
}

// memoryPromptCategory 取该 key 承载的记忆类别名（非记忆提示词键 → 空串）。
func memoryPromptCategory(key string) string {
	if cat, ok := strings.CutPrefix(key, memoryPromptPrefix); ok {
		return cat
	}
	return ""
}

// promptSystemFile 某级 capability 根下的提示词文档路径（= <capRoot>/system/<kind>.md）。
func promptSystemFile(capRoot, kind string) string {
	return capfs.SystemDocFile(capRoot, kind)
}

// promptProjectFile 项目级提示词文件路径（workDir 空 → 空串 = 走继承）。
func promptProjectFile(workDir, kind string) string {
	if workDir == "" {
		return ""
	}
	return promptSystemFile(capfs.ProjectRoot(workDir), kind)
}

// promptUserFile 用户级提示词文件路径（usrPath 注入时随其所在目录）。
func promptUserFile(usrPath, kind string) string {
	return promptSystemFile(capfs.UserRoot(usrPath), kind)
}

// readSystemDoc 读某级 system 文档文件的**纯文本**正文（去首尾空白；缺失/空 → ""）。
func readSystemDoc(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// readPromptDoc 读提示词有效值（OP-02/OP-04 读序）：
// 项目级文件 → 用户级文件 → 系统级文件 → embed 内置。
func (s *Service) readPromptDoc(workDir, kind string) string {
	if v := readSystemDoc(promptProjectFile(workDir, kind)); v != "" {
		return v
	}
	return s.inheritedPromptDoc(kind)
}

// inheritedPromptDoc 取**继承值**（不含项目级文件）：
// 用户级文件 → 系统级文件 → embed 内置（同一份出厂内容）。
// 用于 load 回落与 save 的"是否与继承值相同"判定（见 writePrompt）。
func (s *Service) inheritedPromptDoc(kind string) string {
	if v := readSystemDoc(promptUserFile(s.UsrPath, kind)); v != "" {
		return v
	}
	if sys, err := capfs.SystemRoot(s.AppDir); err == nil {
		if v := readSystemDoc(promptSystemFile(sys, kind)); v != "" {
			return v
		}
	}
	return data.SystemDoc(kind)
}

// writePrompt 写提示词覆盖文件：
//   - 内容为空 / 与**继承值**（用户文件 → 系统文件 → embed 内置）完全相同 → 删除覆盖文件
//     （回落继承）——设置页回填的是"有效值"（可能来自继承），点保存即固化为覆盖件会**永久
//     遮蔽**上级后续更新；相同即不覆盖（P0 修复：消除写放大）；
//   - 内容不同 → 写覆盖文件：唯一用户级类别「用户偏好」写**用户级文件**，其余（含 summary）写
//     **项目级文件**（纯文本）。
func (s *Service) writePrompt(workDir, key, content string) error {
	kind, ok := systemDocKind(key)
	if !ok {
		return fmt.Errorf("persist: unknown prompt key %q", key)
	}
	if strings.TrimSpace(content) == "" || strings.TrimSpace(content) == strings.TrimSpace(s.inheritedPromptDoc(kind)) {
		return s.removePromptOverrides(workDir, key)
	}
	target := promptProjectFile(workDir, kind)
	capRoot := capfs.ProjectRoot(workDir)
	if memoryPromptCategory(key) == facade.MemoryUserCategory {
		target = promptUserFile(s.UsrPath, kind) // 用户偏好：跨项目，写用户级
		capRoot = capfs.UserRoot(s.UsrPath)
	}
	if target == "" {
		return nil // 无项目级（workDir 空）且非用户级 → 无处可写，走继承
	}
	// 写前越界复验（A-37）：类别名已过校验，此处再以 RealPathInside 复验实路径
	// （system 目录内指向根外的 symlink 不得被 MkdirAll/WriteFile 跟随）。
	if !capfs.RealPathInside(capRoot, target) {
		return fmt.Errorf("persist: prompt path escapes capability root: %s", key)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(content), 0o644)
}

// removePromptOverrides 删除某 key 的覆盖文件（回落继承）：
//   - 摘要（summary）：仅项目级文件（既有语义不变——用户/系统级为工厂资源，不删）；
//   - 记忆类别提示词：项目级 + 用户级（两者皆为用户可编辑覆盖）
//     ——「用户偏好」实际只落用户级，删除幂等。
func (s *Service) removePromptOverrides(workDir, key string) error {
	kind, ok := systemDocKind(key)
	if !ok {
		return nil
	}
	paths := []string{promptProjectFile(workDir, kind)}
	if memoryPromptCategory(key) != "" {
		paths = append(paths, promptUserFile(s.UsrPath, kind))
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
