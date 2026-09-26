// 场景目录读写 + 出厂默认场景（12-数据层 §5.2 · 25-MCP与场景分层模型 §6）——原
// persist/persist_capability.go 的场景段与 persist/persist_scenario.go 逐字下移。
//
// 场景 = **独立根 `scenarios/`**（与 capability/ **平级**）/<场景目录>/ 内含：
//
//	scenario.json   {name, description, createdAt, updatedAt}
//	main.agent.md   主 agent（固定文件名）
//	*.agent.md      其余子 agent（一文件一 agent）
//
// agent 文件沿用 mcp 四原语分区契约（# 标题 + [meta] + [description] + [content]），
// 复用同包的契约解析/组装。
//
// 三级根（app / user / project）与 capability 根**同构但目录不同**（见 ScenarioRoots）；
// 场景 id **全局唯一（跨级亦然）**，三级"覆盖"语义不存在（25 §6）。
// **同一场景内 agent 名必须唯一** —— 重名（含大小写 / 主与子撞名 `main` / 空名）保存即拒绝（25 §6.1 · 42 §2 (175)）。
package capfs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// DefaultScenarioKey 出厂默认场景的目录名（app 级随发布资源 `scenarios/default/`；restore 缺省 id）。
const DefaultScenarioKey = "default"

// ScenariosDirName 场景独立根目录名（与 capability/ **平级**，25-MCP与场景分层模型 §6）。
const ScenariosDirName = "scenarios"

const (
	scenarioMetaFile = "scenario.json"
	scenarioMainFile = "main.agent.md"
	agentFileSuffix  = ".agent.md"
)

// ScenarioSystemRoot 系统级场景根 = <appDir 的父目录>/scenarios（appDir = 系统级 capability 根，
// 故 scenarios 与 capability **平级**；appDir 空 → <exeDir>/scenarios）。
func ScenarioSystemRoot(appDir string) (string, error) {
	if appDir != "" {
		return filepath.Join(appDir, "..", ScenariosDirName), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), ScenariosDirName), nil
}

// ScenarioUserRoot 用户级场景根 = ~/.chonkpilot/scenarios（usrPath 注入时随其所在目录）。
// 与 UserRoot（capability）同目录规则，仅末级目录名不同。
func ScenarioUserRoot(usrPath string) string {
	if usrPath != "" {
		return filepath.Join(filepath.Dir(usrPath), ScenariosDirName)
	}
	return filepath.Join(filepath.Dir(data.UserPath()), ScenariosDirName)
}

// ScenarioProjectRoot 项目级场景根 = <workdir>/.chonkpilot/scenarios。
func ScenarioProjectRoot(workDir string) string {
	return filepath.Join(workDir, ".chonkpilot", ScenariosDirName)
}

// ScenarioRoots 返回三级场景根（系统 → 用户 → 项目；workDir 空则不含项目级）。
// 与三级 capability 根（SystemRoot / UserRoot / ProjectRoot）**同构**，仅末级目录名由 capability 换为 scenarios（25 §6）。
func ScenarioRoots(appDir, usrPath, workDir string) []Level {
	out := []Level{}
	if sys, err := ScenarioSystemRoot(appDir); err == nil {
		out = append(out, Level{Kind: KindApp, Root: sys})
	}
	out = append(out, Level{Kind: KindUser, Root: ScenarioUserRoot(usrPath)})
	if workDir != "" {
		out = append(out, Level{Kind: KindProject, Root: ScenarioProjectRoot(workDir)})
	}
	return out
}

// ListScenarioDirs 列出场景根下的场景目录名（字母序；跳过隐藏项与非目录）。
func ListScenarioDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// scenarioMeta 是 scenario.json 结构。
type scenarioMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

// ReadScenarioDir 读一个场景目录 → 场景元素（含 agents）。root = 某级**场景根**
// （ScenarioSystemRoot / ScenarioUserRoot / ScenarioProjectRoot，25 §6）。
// 元素字段与旧 DB 版一致（id/key/name/description/agents/createdAt/updatedAt），
// 另加 level（app|user|project，供只读判定）。
func ReadScenarioDir(kind, root, dir string) (map[string]any, error) {
	dirPath := filepath.Join(root, dir)
	meta := scenarioMeta{Name: dir}
	if raw, err := os.ReadFile(filepath.Join(dirPath, scenarioMetaFile)); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	agents := []any{}
	mainAgent := map[string]any(nil)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), agentFileSuffix) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dirPath, name))
		if err != nil {
			continue
		}
		a := agentFromDoc(ParseDoc(string(raw)), name)
		if isMainAgent(a) && mainAgent == nil {
			mainAgent = a // 主 agent 恒排首位（与旧 DB 版顺序一致，UI 依赖 agents[0]）
			continue
		}
		agents = append(agents, a)
	}
	if mainAgent != nil {
		agents = append([]any{mainAgent}, agents...)
	}
	if meta.Name == "" {
		meta.Name = dir
	}
	sc := map[string]any{
		"id":          dir,
		"key":         dir,
		"name":        meta.Name,
		"description": meta.Description,
		"level":       kind,
		"agents":      agents,
		// systemPrompt 为**保留供兼容的派生字段**：= 主 agent 的 prompt（v6 起该内容由
		// main.agent.md 承载）。25 §3 起**场景层提示词不再取用本字段** —— 由 `llm/server` 侧
		// 按三层（全局 / 场景 / agent）自行拼接（`loadScenario` 只取 `description` + `agents`），
		// 故此处保留字段仅为兼容既有 `data-scenario-*` 消费方，不改变其取值语义。
		"systemPrompt": mainAgentPrompt(agents),
	}
	if meta.CreatedAt != "" {
		sc["createdAt"] = meta.CreatedAt
	}
	if meta.UpdatedAt != "" {
		sc["updatedAt"] = meta.UpdatedAt
	}
	return sc, nil
}

// ValidateScenarioAgents 校验**同一场景内** agent 命名唯一性（25-MCP与场景分层模型 §6.1 ·
// 2026-09-26 用户裁决，42 §2 (175)）：agent **允许跨场景重名**（以场景前缀消歧），但同一场景内
// **必须唯一** → 同名即**拒绝写入**（**不得静默覆盖 / 不得只保留先到者**）。
//
// 判定键 = agent 的**落盘文件名**（agentFileName，大小写不敏感）—— 与 WriteScenarioDir 实际写盘
// 口径一致，故一并覆盖：① 直呼同名；② 大小写差异（Windows 文件名不敏感）；③ 主 agent 与子 agent
// 撞名「main」（同落 main.agent.md）；④ 空名（子 agent 回落 agent.agent.md）。错误信息含
// **场景 id + 重复的 agent 名**，便于诊断。
func ValidateScenarioAgents(scenarioID string, sc map[string]any) error {
	agents, _ := sc["agents"].([]any)
	seen := map[string]string{} // 落盘文件名（小写）→ 首次命中的 agent 名
	for _, a := range agents {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(kernel.SvalOf(am["name"]))
		fileName := agentFileName(am)
		key := strings.ToLower(fileName)
		if prev, dup := seen[key]; dup {
			first, cur := prev, name
			if first == "" {
				first = fileName
			}
			if cur == "" {
				cur = fileName
			}
			return fmt.Errorf("场景 %q 内 agent 重名：%q 与 %q 解析为同一文件 %q，"+
				"同一场景内 agent 名必须唯一；请改名后重试", scenarioID, first, cur, fileName)
		}
		seen[key] = name
	}
	return nil
}

// WriteScenarioDir 写一个场景目录（scenario.json + main.agent.md + *.agent.md）。
// root = 某级**场景根**（25 §6）。已存在的旧 agent 文件先清理，避免改名后残留。
// 写盘前先做**同场景 agent 重名校验**（ValidateScenarioAgents）→ 重名即拒绝、不落盘（42 §2 (175)）。
func WriteScenarioDir(kind, root, dir string, sc map[string]any) error {
	if err := ValidateScenarioAgents(dir, sc); err != nil {
		return err
	}
	dirPath := filepath.Join(root, dir)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return err
	}
	// 清理既有 *.agent.md（按新 agents 全新落盘）
	if entries, err := os.ReadDir(dirPath); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), agentFileSuffix) {
				_ = os.Remove(filepath.Join(dirPath, e.Name()))
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	createdAt := kernel.SvalOf(sc["createdAt"])
	if createdAt == "" {
		createdAt = now
	}
	meta := scenarioMeta{
		Name:        kernel.SvalOf(sc["name"]),
		Description: kernel.SvalOf(sc["description"]),
		CreatedAt:   createdAt,
		UpdatedAt:   now,
	}
	if meta.Name == "" {
		meta.Name = dir
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dirPath, scenarioMetaFile), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	agents, _ := sc["agents"].([]any)
	for _, a := range agents {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		fileName := agentFileName(am)
		if err := os.WriteFile(filepath.Join(dirPath, fileName), []byte(BuildDoc(agentToDoc(am))), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeScenarioPayload 归一化保存载荷：只给 systemPrompt 而未给主 agent prompt（旧前端形态）
// → 落到主 agent 的 prompt；无 main agent 且给了 systemPrompt → 补一条主 agent。
// 同时剔除派生字段 systemPrompt（它由 main.agent.md 承载，写盘时不再单独保存）。
func NormalizeScenarioPayload(sc map[string]any) map[string]any {
	agents, _ := sc["agents"].([]any)
	sys := kernel.SvalOf(sc["systemPrompt"])
	if sys != "" && mainAgentPrompt(agents) == "" {
		merged := false
		for _, a := range agents {
			m, ok := a.(map[string]any)
			if !ok || !isMainAgent(m) {
				continue
			}
			m["prompt"] = sys
			merged = true
			break
		}
		if !merged {
			agents = append([]any{map[string]any{
				"name": "Loop Engineer", "roleTag": "主", "isMain": true, "prompt": sys,
			}}, agents...)
		}
		sc["agents"] = agents
	}
	delete(sc, "systemPrompt")
	return sc
}

// CopyScenarioDir 复制场景目录（复制下行 / 还原出厂默认用；目标已存在则先清空）。
// srcRoot / dstRoot = 源 / 目标级**场景根**（25 §6）。
func CopyScenarioDir(srcRoot, dstRoot, dir string) error {
	src := filepath.Join(srcRoot, dir)
	dst := filepath.Join(dstRoot, dir)
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ScenarioDirExists 判断某级**场景根**下场景目录是否存在（25 §6）。
func ScenarioDirExists(root, dir string) bool {
	st, err := os.Stat(filepath.Join(root, dir))
	return err == nil && st.IsDir()
}

// mainAgentPrompt 取主 agent 的 prompt（无主 agent → 空）。
func mainAgentPrompt(agents []any) string {
	for _, a := range agents {
		if m, ok := a.(map[string]any); ok && isMainAgent(m) {
			return kernel.SvalOf(m["prompt"])
		}
	}
	return ""
}

// agentFileName agent 文件名：主 agent 固定 main.agent.md，其余 <名称>.agent.md。
func agentFileName(a map[string]any) string {
	if isMainAgent(a) {
		return scenarioMainFile
	}
	name := SanitizeName(kernel.SvalOf(a["name"]))
	if name == "" {
		name = "agent"
	}
	return name + agentFileSuffix
}

// isMainAgent 判断是否主 agent（isMain 字段为真）。
func isMainAgent(a map[string]any) bool {
	switch v := a["isMain"].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// agentToDoc agent 字段 → 契约文档（标题 + meta + description + content）。
func agentToDoc(a map[string]any) Doc {
	doc := Doc{Meta: map[string]string{}, Title: kernel.SvalOf(a["name"])}
	if rt := kernel.SvalOf(a["roleTag"]); rt != "" {
		doc.Meta["roletag"] = rt
	}
	if isMainAgent(a) {
		doc.Meta["ismain"] = "true"
	}
	if tools := kernel.SvalOf(a["tools"]); tools != "" {
		doc.Meta["tools"] = tools
	}
	if llm := kernel.SvalOf(a["llmRef"]); llm != "" {
		doc.Meta["llm"] = llm
	}
	if dc := kernel.SvalOf(a["delegateCond"]); dc != "" {
		doc.Meta["delegate"] = dc
	}
	doc.Description = kernel.SvalOf(a["description"])
	doc.Content = kernel.SvalOf(a["prompt"])
	return doc
}

// agentFromDoc 契约文档 → agent 字段。
func agentFromDoc(doc Doc, fileName string) map[string]any {
	isMain := strings.EqualFold(doc.Meta["ismain"], "true") ||
		strings.EqualFold(fileName, scenarioMainFile)
	a := map[string]any{
		"name":        doc.Title,
		"description": doc.Description,
		"roleTag":     doc.Meta["roletag"],
		"isMain":      isMain,
		"prompt":      doc.Content,
	}
	if a["name"] == "" {
		a["name"] = strings.TrimSuffix(fileName, agentFileSuffix)
	}
	if v := doc.Meta["tools"]; v != "" {
		a["tools"] = v
	}
	if v := doc.Meta["llm"]; v != "" {
		a["llmRef"] = v
	}
	if v := doc.Meta["delegate"]; v != "" {
		a["delegateCond"] = v
	}
	return a
}
