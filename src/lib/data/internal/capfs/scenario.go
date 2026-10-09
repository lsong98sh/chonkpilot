// 场景目录读写 + 出厂场景校验（12-数据层 §5.2 · 25-MCP与场景分层模型 §6）——原
// persist/persist_capability.go 的场景段与 persist/persist_scenario.go 逐字下移。
//
// 场景 = **capability 根下的 `scenarios/` 子目录**（`<级别根>/capability/scenarios/`；四级同构
// app / user / project / prjusr）/<场景目录>/ 内含：
//
//	scenario.json   {name, description, createdAt, updatedAt, agents:[<引用路径>…]}
//	main.agent.md   主 agent（固定文件名；**内联**，可编辑、不可选）
//
// **子 agent 唯一形态 = 引用（ref）**（P4，2026-10-01；批 1 2026-10-04 去内联双形态）：`scenario.json`
// 的 `agents` 存 agent 引用路径（变量前缀 `${exeDir}`(系统级) / `${usrDir}`(用户级) / `${workDir}`(项目级)
// / `${dataDir}`(项目私有级) 之后为相对路径，如 `${exeDir}/capability/agents/UX 设计师.agent.md`）。
// 子 agent **只能**经引用承载，场景目录内**不放** `*.agent.md` 子文件；**悬空引用静默删除**
// （文件缺失 / 变量不可解析 → 跳过，不报错）。**无 ref 的非主 agent 保存即拒绝**。
//
// agent 文件沿用 mcp 四原语分区契约（# 标题 + [meta] + [description] + [content]），
// 复用同包的契约解析/组装。
//
// 四级根（app / user / project / prjusr）与 capability 根**同构**（见 ScenariosRoot / ScenarioRoots）；
// 场景 id **全局唯一（跨级亦然）**，四级"覆盖"语义不存在（25 §6）。
// **同一场景内 agent 名必须唯一** —— 重名（含大小写 / 主与子撞名 `main` / 空名）保存即拒绝（25 §6.1 · 42 §2 (175)）。
//
// 四级根均可编辑；app 级出厂场景 = **磁盘目录** `<exeDir>/capability/scenarios/`（源 `src/initdata/capability/scenarios/`，
// 由构建脚本投放）——**不再 embed、不再自动物化**：根缺失即为缺装状态，由上层给出明确提示
// （见 internal/scenario.checkFactoryScenarios）。
package capfs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// ScenariosDirName 场景子目录名（**capability 根下**：<级别根>/capability/scenarios）。
const ScenariosDirName = DirScenarios

const (
	scenarioMetaFile = "scenario.json"
	scenarioMainFile = "main.agent.md"
	agentFileSuffix  = ".agent.md"
)

// RefRoots 四级 **capability 根**（供场景 agent 引用的展开/生成；P4）。
type RefRoots struct {
	App     string // 系统级 capability 根
	User    string // 用户级 capability 根
	Project string // 项目级 capability 根（<workDir>/.chonkpilot/capability）
	PrjUsr  string // 项目私有级 capability 根
}

// refVarPrefix 是「引用变量前缀 → 级别 kind」的映射（顺序即匹配优先）。
// 引用形如 `<prefix>/agents/<名>.agent.md`：prefix 即该级 capability 根的变量写法。
var refVarPrefix = []struct {
	prefix string
	kind   string
}{
	{"${exeDir}/capability", KindApp},
	{"${usrDir}/capability", KindUser},
	{"${workDir}/.chonkpilot/capability", KindProject},
	{"${dataDir}/capability", KindPrjUsr},
}

// CapRootOf 返回某级 capability 根（缺失 → 空串）。
func (r RefRoots) CapRootOf(kind string) string {
	switch kind {
	case KindApp:
		return r.App
	case KindUser:
		return r.User
	case KindProject:
		return r.Project
	case KindPrjUsr:
		return r.PrjUsr
	}
	return ""
}

// resolveAgentRef 展开引用的**纯路径**部分（不校验目标是否存在）：按变量前缀映射到对应级
// capability 根后拼相对路径，并做越界复验（A-36）。返回：
//   - abs     ：展开后的绝对路径（ok 时有效）；
//   - matched ：ref 命中了某个变量前缀（据此区分「悬空变量」与「完全不属已知形态」）；
//   - ok      ：命中前缀、该级根可解析、且展开结果**未越界**（词法 + 实路径双复验）。
//
// 越界复验（A-36）：前缀匹配**不校验** rel 中的 `..`，恶意引用（如
// `${exeDir}/capability/../../secret`）经 Join 可逃出 capRoot → 越权读任意文件并经场景列表/
// 详情回吐。此处词法（StrictlyWithin）判 `..` 逃逸 + 实路径（RealPathInside）判 capRoot 内
// 指向根外的 symlink；任一越界 → ok=false。
func resolveAgentRef(ref string, roots RefRoots) (abs string, matched, ok bool) {
	ref = strings.TrimSpace(strings.ReplaceAll(ref, "\\", "/"))
	if ref == "" {
		return "", false, false
	}
	for _, v := range refVarPrefix {
		if ref == v.prefix || strings.HasPrefix(ref, v.prefix+"/") {
			capRoot := roots.CapRootOf(v.kind)
			if capRoot == "" {
				return "", true, false // 该级根不可解析 → 悬空
			}
			rel := strings.TrimPrefix(ref, v.prefix)
			rel = strings.TrimPrefix(rel, "/")
			p := filepath.Join(capRoot, filepath.FromSlash(rel))
			if !StrictlyWithin(capRoot, p) || !RealPathInside(capRoot, p) {
				return "", true, false // 越界 → 拒绝
			}
			return p, true, true
		}
	}
	return "", false, false
}

// ExpandAgentRef 展开场景 agent 引用路径 → 绝对路径：按变量前缀映射到对应级 capability 根，
// 其后为相对路径拼在根下。**悬空/越权**（变量不可解析 / 不在任一前缀下 / 越界 / 目标文件缺失）
// → ("", false)。
func ExpandAgentRef(ref string, roots RefRoots) (string, bool) {
	abs, _, ok := resolveAgentRef(ref, roots)
	if !ok {
		return "", false
	}
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		return "", false // 悬空引用 → 静默删除
	}
	return abs, true
}

// AgentRefOf 由绝对路径生成场景 agent 引用（须落在某级 capability 根下）；不属任一级 → ("", false)。
// 反函数：ExpandAgentRef(AgentRefOf(p)) == p（同级根内）。
func AgentRefOf(absPath string, roots RefRoots) (string, bool) {
	p := filepath.ToSlash(filepath.Clean(absPath))
	// 具体级优先（prjusr → project → user → app），与 LevelPriority 一致
	for _, kind := range []string{KindPrjUsr, KindProject, KindUser, KindApp} {
		capRoot := roots.CapRootOf(kind)
		if capRoot == "" {
			continue
		}
		root := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(capRoot)), "/")
		if p == root || !strings.HasPrefix(p, root+"/") {
			continue
		}
		rel := strings.TrimPrefix(p, root+"/")
		for _, v := range refVarPrefix {
			if v.kind == kind {
				return v.prefix + "/" + rel, true
			}
		}
	}
	return "", false
}

// ScenariosRoot 某级 capability 根下的场景根 = <capRoot>/scenarios。
func ScenariosRoot(capRoot string) string {
	return filepath.Join(capRoot, ScenariosDirName)
}

// ScenarioSystemRoot 系统级场景根 = <appDir>/scenarios（appDir = 系统级 capability 根；
// appDir 空 → <exeDir>/capability/scenarios）。
func ScenarioSystemRoot(appDir string) (string, error) {
	cap, err := SystemRoot(appDir)
	if err != nil {
		return "", err
	}
	return ScenariosRoot(cap), nil
}

// ScenarioUserRoot 用户级场景根 = ~/.chonkpilot/capability/scenarios（usrPath 注入时随其所在目录）。
func ScenarioUserRoot(usrPath string) string {
	return ScenariosRoot(UserRoot(usrPath))
}

// ScenarioProjectRoot 项目级场景根 = <workdir>/.chonkpilot/capability/scenarios。
func ScenarioProjectRoot(workDir string) string {
	return ScenariosRoot(ProjectRoot(workDir))
}

// ScenarioPrjUsrRoot 项目私有级场景根 = <prjusr 数据根>/capability/scenarios。
func ScenarioPrjUsrRoot(projectID string) string {
	return ScenariosRoot(PrjUsrRoot(projectID))
}

// ScenarioRoots 返回四级场景根（系统 → 用户 → 项目 → 项目私有）。
// prjUsrCapRoot 为**项目私有 capability 根**（空串则不含该级，例如实例未登记）；
// workDir 空则不含项目级。与四级 capability 根**同构**（25 §6）。
func ScenarioRoots(appDir, usrPath, workDir, prjUsrCapRoot string) []Level {
	out := []Level{}
	if sys, err := ScenarioSystemRoot(appDir); err == nil {
		out = append(out, Level{Kind: KindApp, Root: sys})
	}
	out = append(out, Level{Kind: KindUser, Root: ScenarioUserRoot(usrPath)})
	if workDir != "" {
		out = append(out, Level{Kind: KindProject, Root: ScenarioProjectRoot(workDir)})
	}
	if prjUsrCapRoot != "" {
		out = append(out, Level{Kind: KindPrjUsr, Root: ScenariosRoot(prjUsrCapRoot)})
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

// ValidScenarioID 校验场景 id 是否可作为场景目录名（A-31）。口径对齐前端 slugify 字符集
// （小写字母/数字/下划线/连字符，另放宽大写字母）：**禁空 / `.` / `..` / 路径分隔符（`/` `\`）/
// 其余非法字符**。非法 id **拒绝写入/定位/删除**——否则 `..` 等拼接会越出场景根（如
// `os.RemoveAll(<root>/..)` 会删掉整棵 capability 树）。
func ValidScenarioID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_':
		case r == '-':
			if i == 0 { // 首字符不得为连字符（前端 slugify 亦会裁掉首尾连字符）
				return false
			}
		default:
			return false
		}
	}
	return true
}

// scenarioMeta 是 scenario.json 结构。
type scenarioMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	// Agents 子 agent 引用路径（变量前缀 + 相对路径；主 agent 不在其中 —— 由 main.agent.md 内联承载）。
	Agents []string `json:"agents,omitempty"`
}

// ReadScenarioDir 读一个场景目录 → 场景元素（含 agents）。root = 某级**场景根**
// （ScenarioSystemRoot / ScenarioUserRoot / ScenarioProjectRoot / ScenarioPrjUsrRoot，25 §6）。
// 元素字段与旧 DB 版一致（id/key/name/description/agents/createdAt/updatedAt），
// 另加 level（app|user|project|prjusr，级别标识）。
//
// agents 来源（**唯一形态 = 引用**）：主 agent 内联（main.agent.md，恒排首位）；子 agent =
// `scenario.json.agents` 的**引用路径**（`roots` 展开；**悬空/越权 → 静默删除**）。被引用的
// agent 条目附带 `ref` 字段（原引用串）。
func ReadScenarioDir(kind, root, dir string, roots RefRoots) (map[string]any, error) {
	dirPath := filepath.Join(root, dir)
	if st, err := os.Stat(dirPath); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("scenario dir not found: %s", dirPath)
	}
	meta := scenarioMeta{Name: dir}
	if raw, err := ReadFileCapped(filepath.Join(dirPath, scenarioMetaFile), MaxDocReadBytes); err == nil {
		if err := json.Unmarshal(raw, &meta); err != nil {
			warn("scenario: 解析场景元信息 %s 失败（退化为默认元信息）：%v", filepath.Join(dirPath, scenarioMetaFile), err) // 尽力读，失败留痕（A-42）
		}
	}
	agents := []any{}
	// 主 agent：固定 main.agent.md 内联（恒排首位，UI 依赖 agents[0]）
	if raw, err := ReadFileCapped(filepath.Join(dirPath, scenarioMainFile), MaxDocReadBytes); err == nil {
		agents = append(agents, agentFromDoc(ParseDoc(string(raw)), scenarioMainFile))
	}
	// 子 agent：只从 meta.Agents 的引用展开（逐条；悬空/越权 → 静默删除）
	for _, ref := range meta.Agents {
		abs, ok := ExpandAgentRef(ref, roots)
		if !ok {
			continue
		}
		raw, err := ReadFileCapped(abs, MaxDocReadBytes)
		if err != nil {
			continue
		}
		a := agentFromDoc(ParseDoc(string(raw)), filepath.Base(abs))
		a["isMain"] = false // 引用 = 子 agent（不因被引文件 ismain 而升为主）
		a["ref"] = ref
		agents = append(agents, a)
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

// WriteScenarioDir 写一个场景目录（scenario.json + main.agent.md）。
// root = 某级**场景根**（25 §6）。已存在的旧 agent 文件先清理，避免改名后残留。
// 写盘前先做**同场景 agent 重名校验**（ValidateScenarioAgents）→ 重名即拒绝、不落盘（42 §2 (175)）。
//
// 子 agent 落盘（**唯一形态 = 引用**）：带 `ref` 的 agent → 写入 `scenario.json.agents`（引用路径，
// 不落单独文件）；主 agent → 内联写 `main.agent.md`；**无 ref 的非主 agent → 拒绝**（子 agent 必须以
// 引用形式保存，内联子 agent 形态已废除）。
func WriteScenarioDir(kind, root, dir string, sc map[string]any, roots RefRoots) error {
	// id 合法性（A-31）：非法 id（空 / `.` / `..` / 路径分隔符 / 非法字符）拒绝落盘——否则 `dir`
	// 拼接会越出场景根（写盘先 MkdirAll/清理，越界会污染甚至删除根外目录）。
	if !ValidScenarioID(dir) {
		return fmt.Errorf("场景 id %q 非法：仅允许字母/数字/下划线/连字符，不得为空或含路径分隔符", dir)
	}
	if err := ValidateScenarioAgents(dir, sc); err != nil {
		return err
	}
	// 先校验 / 收集子 agent 引用（**无 ref 的非主 agent → 拒绝，且不建目录/不落盘**）
	agents, _ := sc["agents"].([]any)
	refs := []string{}
	for _, a := range agents {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		if isMainAgent(am) {
			continue // 主 agent 内联写 main.agent.md（下方统一落盘）
		}
		ref := strings.TrimSpace(kernel.SvalOf(am["ref"]))
		if ref == "" {
			return fmt.Errorf("场景 %q 的 agent %q 缺少引用（ref）：子 agent 必须以引用形式保存",
				dir, kernel.SvalOf(am["name"]))
		}
		// 绝对路径（前端可能直接给绝对路径）→ 归一为变量前缀形态
		if filepath.IsAbs(filepath.FromSlash(ref)) {
			if v, ok := AgentRefOf(ref, roots); ok {
				ref = v
			}
		}
		// 写侧越界复验（A-36）：引用命中某变量前缀但展开越界 → 拒绝落盘，避免把越界引用
		// 固化进 scenario.json（读侧 ExpandAgentRef 同样复验，此处提前拒绝）。
		if _, matched, ok := resolveAgentRef(ref, roots); matched && !ok {
			return fmt.Errorf("场景 %q 的 agent %q 引用越界：%s", dir, kernel.SvalOf(am["name"]), ref)
		}
		refs = append(refs, ref) // 引用形态：只记引用路径
	}
	dirPath := filepath.Join(root, dir)
	// 越界复验（A-31/A-38）：id 已过 ValidScenarioID，此处再以 StrictlyWithin 兜底词法，
	// 并以 RealPathInside 复验实路径（场景根内指向根外的 symlink 不得被 MkdirAll/WriteFile 跟随）。
	if !StrictlyWithin(root, dirPath) || !RealPathInside(root, dirPath) {
		return fmt.Errorf("场景 id %q 越出场景根", dir)
	}
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return err
	}
	// 清理既有 *.agent.md（按新 agents 全新落盘；场景目录不再承载子 agent 文件）
	if entries, err := os.ReadDir(dirPath); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), agentFileSuffix) {
				if err := os.Remove(filepath.Join(dirPath, e.Name())); err != nil && !os.IsNotExist(err) {
					warn("scenario: 清理旧 agent 文件 %s 失败：%v", filepath.Join(dirPath, e.Name()), err) // 尽力清理，失败留痕（A-42）
				}
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
		Agents:      refs,
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
	// 主 agent 内联落盘
	for _, a := range agents {
		am, ok := a.(map[string]any)
		if !ok || !isMainAgent(am) {
			continue
		}
		if err := os.WriteFile(filepath.Join(dirPath, scenarioMainFile), []byte(BuildDoc(agentToDoc(am))), 0o644); err != nil {
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
