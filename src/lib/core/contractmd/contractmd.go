// Package contractmd 是 ChonkPilot 能力资产契约（`*.type.md`）的**唯一**解析/序列化实现。
//
// v2 格式（2026-10-14，见 docs/spec/20-modules/25-mcp-server.md §3.1）：
//
//	# 基本信息
//	```yaml
//	name: <原语名>          # 必填、不可删（= 文件名去后缀）
//	title: <可读标题>
//	<类型专属键>
//	```
//
//	# 摘要
//	<description>
//
//	# 参数                    # 仅 tool / prompt
//	```json
//	{...JSON Schema...}
//	```
//
//	# 参考                    # 仅 skill / agent
//	```yaml
//	skills:
//	  - <name>
//	tools:
//	  - <name>
//	```
//
//	# 委托                    # 仅 agent
//	<委派条件文本>
//
//	# 正文                    # 必须是最后一个分区
//	<markdown>
//
// 资源（resource）可**指向外部内容文件**：在 `# 基本信息` 里加 `path: <文件>`（相对契约文件目录或绝对
// 路径）→ 该资源**无「正文」**，内容取自该文件、mimetype 按该文件后缀识别（见 MimetypeOfFile）；
// 无 `path` 时资源内容 = `# 正文`（markdown）、mimetype 恒 DefaultMimetype。
//
// 旧方括号分区（`[meta]`/`[description]`/…）**不再兼容读**。此前分散在
// capfs / mcp-server contract / llm-server domainmd / mcp-tools cli 的 4 份重复实现全部收敛到本包。
package contractmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Kind 一类能力资产类型（= 文件后缀 token）。
type Kind string

const (
	KindTool     Kind = "tool"
	KindSkill    Kind = "skill"
	KindPrompt   Kind = "prompt"
	KindResource Kind = "resource"
	KindAgent    Kind = "agent"
	KindScenario Kind = "scenario" // 目录形态，非 *.scenario.md（仅类型名映射）
)

// Type 一类能力面条目（目录名 ↔ 后缀 token ↔ 显示名）。
type Type struct {
	Dir   string // 类型目录名（prompts/tools/resources/skills/agents/scenarios）
	Rel   string // 相对 capability 根的物理路径（= Dir，扁平）
	Token string // 文件后缀 token（prompt/tool/resource/skill/agent/scenario）
	Label string // 显示名
}

// Types 是 6 类能力面条目的规范清单（顺序即知识库根下的展示顺序）。
var Types = []Type{
	{Dir: "prompts", Rel: "prompts", Token: "prompt", Label: "提示词"},
	{Dir: "tools", Rel: "tools", Token: "tool", Label: "工具"},
	{Dir: "resources", Rel: "resources", Token: "resource", Label: "资源"},
	{Dir: "skills", Rel: "skills", Token: "skill", Label: "技能"},
	{Dir: "agents", Rel: "agents", Token: "agent", Label: "智能体"},
	{Dir: "scenarios", Rel: "scenarios", Token: "scenario", Label: "场景"},
}

// TypeByDir 按目录名取类型（未知 → nil）。
func TypeByDir(dir string) *Type {
	for i := range Types {
		if Types[i].Dir == dir {
			return &Types[i]
		}
	}
	return nil
}

// TypeByToken 按后缀 token 取类型（未知 → nil）。
func TypeByToken(tok string) *Type {
	for i := range Types {
		if Types[i].Token == tok {
			return &Types[i]
		}
	}
	return nil
}

// 分区名（H1）。出现顺序即规范顺序。
const (
	SecMeta     = "基本信息"
	SecSummary  = "摘要"
	SecParams   = "参数"
	SecRefs     = "参考"
	SecDelegate = "委托"
	SecContent  = "正文"
)

var sectionOrder = []string{SecMeta, SecSummary, SecParams, SecRefs, SecDelegate, SecContent}

func isSectionName(name string) bool {
	for _, s := range sectionOrder {
		if s == name {
			return true
		}
	}
	return false
}

// Doc 解析后的契约文档（v2）。
type Doc struct {
	Kind        Kind                `json:"kind,omitempty"`
	Name        string              `json:"name"`               // 基本信息 name（= 文件名去后缀）
	Title       string              `json:"title"`              // 基本信息 title（可读标题）
	Meta        map[string]string   `json:"meta,omitempty"`     // 基本信息其余键（不含 name/title），按类型专属
	Description string              `json:"description"`        // 摘要
	Parameters  string              `json:"parameters,omitempty"` // 参数（json 文本，去围栏）
	Refs        map[string][]string `json:"refs,omitempty"`     // 参考：skills / tools
	Delegate    string              `json:"delegate,omitempty"` // 委托（agent）
	Content     string              `json:"content"`            // 正文
}

// InferURI resource 的隐式 uri（不写盘，仅注册 MCP 时提供）。
func InferURI(name string) string { return "mcp://chonkpilot/resources/" + name }

// DefaultMimetype 是资源**无外部内容文件**（纯 markdown 契约）时的隐式 mimetype。
const DefaultMimetype = "text/markdown"

// mimetypeByExt 是资源外部内容文件（meta `path`）的扩展名 → MIME 映射。
// 用**确定性**表（不读系统注册表，避免跨机差异）；未收录扩展 → application/octet-stream。
var mimetypeByExt = map[string]string{
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".txt":      "text/plain",
	".json":     "application/json",
	".yaml":     "application/yaml",
	".yml":      "application/yaml",
	".toml":     "application/toml",
	".csv":      "text/csv",
	".tsv":      "text/tab-separated-values",
	".xml":      "application/xml",
	".html":     "text/html",
	".htm":      "text/html",
	".css":      "text/css",
	".js":       "text/javascript",
	".mjs":      "text/javascript",
	".pdf":      "application/pdf",
	".png":      "image/png",
	".jpg":      "image/jpeg",
	".jpeg":     "image/jpeg",
	".gif":      "image/gif",
	".webp":     "image/webp",
	".svg":      "image/svg+xml",
	".ico":      "image/x-icon",
	".zip":      "application/zip",
	".gz":       "application/gzip",
	".tar":      "application/x-tar",
	".wasm":     "application/wasm",
}

// MimetypeOfFile 按文件扩展名推断 MIME 类型（资源 meta `path` 指向的外部内容文件）。
// 未知 / 无扩展 → application/octet-stream。
func MimetypeOfFile(fileName string) string {
	if mt, ok := mimetypeByExt[strings.ToLower(filepath.Ext(fileName))]; ok {
		return mt
	}
	return "application/octet-stream"
}

// IsTextMime 判定 MIME 是否文本（决定资源内容经 `text` 还是 `blob` 承载）。
func IsTextMime(mt string) bool {
	if strings.HasPrefix(mt, "text/") {
		return true
	}
	switch mt {
	case "application/json", "application/xml", "application/yaml", "application/toml", "application/javascript":
		return true
	}
	return strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml")
}

// NameOfFile 由文件名取原语名（去 `.<token>.md` / `.md` 后缀）。
func NameOfFile(fileName string) string {
	base := fileName
	if t := TypeOfFileName(fileName); t != nil {
		base = strings.TrimSuffix(base, "."+t.Token+".md")
	} else if strings.HasSuffix(strings.ToLower(base), ".md") {
		base = base[:len(base)-3]
	}
	return base
}

// TypeOfFileName 由文件名后缀判定类型（`*.tool.md` → KindTool；否则 nil）。
func TypeOfFileName(fileName string) *Type {
	low := strings.ToLower(fileName)
	for i := range Types {
		if strings.HasSuffix(low, "."+Types[i].Token+".md") {
			return &Types[i]
		}
	}
	return nil
}

// -------- 解析 --------

// Parse 解析 v2 契约文本。旧方括号分区一律视为正文内容（不再兼容）。
func Parse(raw string) (Doc, error) {
	doc := Doc{Meta: map[string]string{}}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")

	type block struct {
		name string
		body []string
	}
	var blocks []block
	cur := -1
	for _, ln := range lines {
		if name, ok := sectionHeader(ln); ok {
			blocks = append(blocks, block{name: name})
			cur = len(blocks) - 1
			continue
		}
		if cur >= 0 {
			blocks[cur].body = append(blocks[cur].body, ln)
		}
		// 首个分区之前的行（含旧格式/空行）一律忽略
	}
	if len(blocks) == 0 {
		return doc, fmt.Errorf("contract: 未找到任何 H1 分区（需 %s）", SecMeta)
	}
	for idx, b := range blocks {
		switch b.name {
		case SecMeta:
			parseMetaBlock(strings.Join(b.body, "\n"), &doc)
		case SecSummary:
			doc.Description = strings.TrimSpace(strings.Join(b.body, "\n"))
		case SecParams:
			doc.Parameters = extractFence(strings.Join(b.body, "\n"), "json")
		case SecRefs:
			doc.Refs = parseRefsBlock(strings.Join(b.body, "\n"))
		case SecDelegate:
			doc.Delegate = strings.TrimSpace(strings.Join(b.body, "\n"))
		case SecContent:
			// 正文必须是最后一个分区；其后整段原样（保留首尾换行由 TrimSpace 收敛）
			if idx != len(blocks)-1 {
				return doc, fmt.Errorf("contract: %s 必须是最后一个分区", SecContent)
			}
			doc.Content = strings.TrimSpace(strings.Join(b.body, "\n"))
		}
	}
	if doc.Name == "" {
		return doc, fmt.Errorf("contract: %s 缺少必填键 name", SecMeta)
	}
	return doc, nil
}

// sectionHeader 识别 H1 分区头（`# <分区名>`）。非固定分区名 → ok=false（视作正文/其它行）。
func sectionHeader(ln string) (string, bool) {
	t := strings.TrimSpace(ln)
	if !strings.HasPrefix(t, "# ") {
		return "", false
	}
	name := strings.TrimSpace(t[2:])
	if isSectionName(name) {
		return name, true
	}
	return "", false
}

// parseMetaBlock 解析基本信息 yaml 块（扁平 key: value）。
func parseMetaBlock(body string, doc *Doc) {
	for _, ln := range yamlLines(body) {
		k, v := splitKV(ln)
		if k == "" {
			continue
		}
		switch k {
		case "name":
			doc.Name = v
		case "title":
			doc.Title = v
		default:
			doc.Meta[k] = v
		}
	}
}

// parseRefsBlock 解析参考 yaml 块（key:\n  - item）。
func parseRefsBlock(body string) map[string][]string {
	refs := map[string][]string{}
	curKey := ""
	for _, ln := range yamlLines(body) {
		if strings.HasPrefix(strings.TrimSpace(ln), "- ") || strings.TrimSpace(ln) == "-" {
			item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "-"))
			if curKey != "" && item != "" {
				refs[curKey] = append(refs[curKey], item)
			}
			continue
		}
		k, v := splitKV(ln)
		if k == "" {
			continue
		}
		if v == "" {
			curKey = k // 列表键
			if _, ok := refs[curKey]; !ok {
				refs[curKey] = nil
			}
		} else {
			refs[k] = []string{v} // 行内单值
		}
	}
	return refs
}

// yamlLines 取 yaml 围栏内的行；无围栏则取全部非空行的原文。
func yamlLines(body string) []string {
	inner := extractFenceRaw(body, "yaml")
	src := strings.Split(inner, "\n")
	var out []string
	for _, ln := range src {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// splitKV 按**首个** ':' 拆 k/v；去包裹引号。
func splitKV(ln string) (string, string) {
	i := strings.Index(ln, ":")
	if i < 0 {
		return "", ""
	}
	k := strings.TrimSpace(ln[:i])
	v := strings.TrimSpace(ln[i+1:])
	v = strings.Trim(v, `"'`)
	return k, v
}

// extractFence 取 ```<lang> … ``` 围栏内的文本（无围栏 → 全部 trim）。
func extractFence(body, lang string) string {
	return strings.TrimSpace(extractFenceRaw(body, lang))
}

// extractFenceRaw 取围栏内原文（保留换行；无围栏 → 原文本）。
func extractFenceRaw(body, lang string) string {
	lines := strings.Split(body, "\n")
	start := -1
	fence := "```" + lang
	for i, ln := range lines {
		if start < 0 {
			t := strings.TrimSpace(ln)
			if t == "```"+lang || strings.EqualFold(t, fence) {
				start = i + 1
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		return body
	}
	return strings.Join(lines[start:], "\n")
}

// -------- 序列化 --------

// Build 组装 v2 契约文本。
func Build(doc Doc) string {
	var sb strings.Builder
	// 基本信息
	sb.WriteString("# " + SecMeta + "\n")
	sb.WriteString("```yaml\n")
	sb.WriteString("name: " + strings.TrimSpace(doc.Name) + "\n")
	if strings.TrimSpace(doc.Title) != "" {
		sb.WriteString("title: " + strings.TrimSpace(doc.Title) + "\n")
	}
	keys := make([]string, 0, len(doc.Meta))
	for k := range doc.Meta {
		if k == "name" || k == "title" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.TrimSpace(doc.Meta[k])
		if v != "" {
			sb.WriteString(k + ": " + v + "\n")
		}
	}
	sb.WriteString("```\n\n")

	if s := strings.TrimSpace(doc.Description); s != "" {
		sb.WriteString("# " + SecSummary + "\n")
		sb.WriteString(s + "\n\n")
	}
	if s := strings.TrimSpace(doc.Parameters); s != "" {
		sb.WriteString("# " + SecParams + "\n")
		sb.WriteString("```json\n")
		sb.WriteString(s + "\n")
		sb.WriteString("```\n\n")
	}
	if len(doc.Refs) > 0 {
		sb.WriteString("# " + SecRefs + "\n")
		sb.WriteString("```yaml\n")
		rk := make([]string, 0, len(doc.Refs))
		for k := range doc.Refs {
			rk = append(rk, k)
		}
		sort.Strings(rk)
		for _, k := range rk {
			items := doc.Refs[k]
			if len(items) == 0 {
				continue
			}
			sb.WriteString(k + ":\n")
			for _, it := range items {
				if strings.TrimSpace(it) != "" {
					sb.WriteString("  - " + strings.TrimSpace(it) + "\n")
				}
			}
		}
		sb.WriteString("```\n\n")
	}
	if s := strings.TrimSpace(doc.Delegate); s != "" {
		sb.WriteString("# " + SecDelegate + "\n")
		sb.WriteString(s + "\n\n")
	}
	if s := strings.TrimSpace(doc.Content); s != "" {
		sb.WriteString("# " + SecContent + "\n")
		sb.WriteString(s + "\n")
	}
	return sb.String()
}

// outlines 各类型「正文」的默认大纲（骨架，新建即预填为正文，指导用户逐段填写；
// 亦作为前端「AI 生成 / AI 优化」的依据）。与前端 utils/primitive.js 的 PRIMITIVE_OUTLINES 对齐。
var outlines = map[Kind][]string{
	KindTool:     {"## 用途", "## 适用场景", "## 用法示例", "## 注意事项"},
	KindSkill:    {"## 目标", "## 步骤", "## 检查点", "## 反例"},
	KindPrompt:   {"## 角色", "## 任务", "## 输入", "## 输出要求"},
	KindResource: {"## 概述", "## 要点", "## 参考"},
	KindAgent:    {"## 职责", "## 工作方式", "## 协作", "## 输出"},
}

// Outline 返回该类型正文默认大纲（骨架行；空 = 无）。
func Outline(kind Kind) []string { return outlines[kind] }

// Template 按类型生成新建模板 doc（仅含该类型应显示的默认键）。
func Template(kind Kind, name string) Doc {
	doc := Doc{Kind: kind, Name: name, Title: name, Meta: map[string]string{}}
	// 默认摘要：LLM/tool 注册均要求「摘要」非空（mcp-server/llm-server 解析契约时校验），
	// 故新建即给占位摘要，避免用户未填时该资产注册失败（可由「从正文提取」/手填覆盖）。
	doc.Description = name + " 描述"
	// 默认正文 = 该类型大纲（骨架）：新建即有可填写结构（2026-10-15 需求，替代原「大纲按钮」）。
	if o := outlines[kind]; len(o) > 0 {
		doc.Content = strings.Join(o, "\n\n")
	}
	switch kind {
	case KindTool:
		doc.Meta["runtime"] = ""
		doc.Meta["args"] = ""
		doc.Meta["output"] = "stdout"
		doc.Parameters = "{\n  \"type\": \"object\",\n  \"properties\": {},\n  \"required\": []\n}"
	case KindPrompt:
		doc.Parameters = "{\n  \"type\": \"object\",\n  \"properties\": {},\n  \"required\": []\n}"
	default: // skill / resource / agent：仅 name（uri/mimetype 隐式）
	}
	return doc
}
