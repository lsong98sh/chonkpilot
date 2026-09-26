// mcp 四原语契约解析/组装 + 文件名规范化（原 persist/persist_knowledge.go 的契约段逐字下移）。
//
// 文件格式 = mcp 四原语统一契约（# 标题 + [meta] k=v + [description] + [parameters]|[arguments]
// + [content]，与 chonkpilot-mcp-server/server/contract.go 一致）；根下四类分类目录
// tools/skills/prompts/resources（知识库 = 整个 capability 树，见 12-数据层）。
package capfs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Type 四类原语（目录名复数 ↔ 文件后缀单数 ↔ 显示名）。
type Type struct {
	Dir   string // 目录名（tools/skills/prompts/resources）
	Token string // 文件后缀 token（tool/skill/prompt/resource）
	Label string // 显示名（工具/技能/提示词/资源）
}

// Types 是四类原语的规范清单（顺序即知识库根下的展示顺序）。
var Types = []Type{
	{Dir: "tools", Token: "tool", Label: "工具"},
	{Dir: "skills", Token: "skill", Label: "技能"},
	{Dir: "prompts", Token: "prompt", Label: "提示词"},
	{Dir: "resources", Token: "resource", Label: "资源"},
}

func typeByDir(dir string) *Type {
	for i := range Types {
		if Types[i].Dir == dir {
			return &Types[i]
		}
	}
	return nil
}

func typeByToken(tok string) *Type {
	for i := range Types {
		if Types[i].Token == tok {
			return &Types[i]
		}
	}
	return nil
}

// Doc 解析后的原语文档（前端「编辑」表单模型）。
type Doc struct {
	Title         string            `json:"title"`
	Meta          map[string]string `json:"meta"`
	Description   string            `json:"description"`
	Parameters    string            `json:"parameters"`
	Content       string            `json:"content"`
	ParamsSection string            `json:"params_section,omitempty"` // [parameters]|[arguments]（保存回写同款分区名）
}

// sectionKey 识别一行是否为契约分区头；返回小写 key（meta/description/parameters/arguments/content）。
func sectionKey(ln string) (string, bool) {
	t := strings.TrimSpace(ln)
	if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
		return "", false
	}
	k := strings.ToLower(strings.TrimSpace(t[1 : len(t)-1]))
	switch k {
	case "meta", "description", "parameters", "arguments", "content":
		return k, true
	}
	return "", false
}

// ParseDoc 按 mcp 契约分区解析：首行 "# 标题" + [meta]/[description]/
// [parameters]|[arguments]/[content]。未知分区与其内容并入 content 前的非分区行一律忽略
// （契约加载器只认上述分区；此处取各分区原文，保证再保存不丢分区结构）。
func ParseDoc(raw string) Doc {
	doc := Doc{Meta: map[string]string{}}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	section := ""
	var sectionLines []string
	flush := func() {
		body := strings.TrimSpace(strings.Join(sectionLines, "\n"))
		switch section {
		case "meta":
			for _, ln := range sectionLines {
				if i := strings.Index(ln, "="); i > 0 {
					doc.Meta[strings.TrimSpace(ln[:i])] = strings.TrimSpace(ln[i+1:])
				}
			}
		case "description":
			doc.Description = body
		case "parameters", "arguments":
			doc.Parameters = body
			doc.ParamsSection = "[" + section + "]"
		case "content":
			doc.Content = body
		}
		sectionLines = nil
	}
	head := true
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if head && strings.HasPrefix(t, "# ") && section == "" {
			doc.Title = strings.TrimSpace(t[2:])
			head = false
			continue
		}
		head = false
		if k, ok := sectionKey(t); ok {
			flush()
			section = k
			continue
		}
		if section != "" {
			sectionLines = append(sectionLines, ln)
		}
	}
	flush()
	return doc
}

// BuildDoc 组装 mcp 契约分区 markdown（仅写出非空分区；标题保留首行）。
func BuildDoc(doc Doc) string {
	var sb strings.Builder
	if strings.TrimSpace(doc.Title) != "" {
		sb.WriteString("# " + strings.TrimSpace(doc.Title) + "\n\n")
	}
	if len(doc.Meta) > 0 {
		sb.WriteString("[meta]\n")
		keys := make([]string, 0, len(doc.Meta))
		for k := range doc.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := strings.TrimSpace(doc.Meta[k])
			if v != "" {
				sb.WriteString(k + "=" + v + "\n")
			}
		}
		sb.WriteString("\n")
	}
	if strings.TrimSpace(doc.Description) != "" {
		sb.WriteString("[description]\n")
		sb.WriteString(strings.TrimSpace(doc.Description) + "\n\n")
	}
	if strings.TrimSpace(doc.Parameters) != "" {
		sb.WriteString(parametersSectionName(doc) + "\n")
		sb.WriteString(strings.TrimSpace(doc.Parameters) + "\n\n")
	}
	if strings.TrimSpace(doc.Content) != "" {
		sb.WriteString("[content]\n")
		sb.WriteString(strings.TrimSpace(doc.Content) + "\n")
	}
	return sb.String()
}

// parametersSectionName 参数区标题：优先 doc.ParamsSection（解析时保留的 [arguments]/[parameters]），
// 缺省 [parameters]（prompt 类型模板创建时显式传 [arguments]）。
func parametersSectionName(doc Doc) string {
	if doc.ParamsSection == "[arguments]" || doc.ParamsSection == "[parameters]" {
		return doc.ParamsSection
	}
	return "[parameters]"
}

// Template 按类型生成新建模板 doc。
func Template(token, name string) Doc {
	doc := Doc{Meta: map[string]string{}, Title: name}
	doc.Description = name + " 描述"
	switch token {
	case "tool":
		doc.Meta["runtime"] = ""
		doc.Meta["entry"] = ""
		doc.Meta["args"] = ""
		doc.Meta["output"] = ""
		doc.ParamsSection = "[parameters]"
		doc.Parameters = "properties:\n    arg1:\n        type: string\n        description: 参数描述\nrequired:\n    - arg1"
	case "prompt":
		doc.Meta["arguments"] = ""
		doc.ParamsSection = "[arguments]"
		doc.Parameters = "properties:\n    arg1:\n        type: string\n        description: 参数描述\nrequired:\n    - arg1"
	case "resource":
		doc.Meta["uri"] = ""
		doc.Meta["mimetype"] = ""
	default: // skill
	}
	return doc
}

// TypeOfArg 归一化创建原语的 type：接受 token（tool/…）或目录名（tools/…）。
func TypeOfArg(t string) *Type {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return nil
	}
	if p := typeByToken(t); p != nil {
		return p
	}
	if p := typeByDir(t); p != nil {
		return p
	}
	return nil
}

// TypeOfFile 由文件名（优先）或父目录推导原语类型 token：
// xxx.tool.md → tool；裸 xxx.md 在类型目录下 → 该目录 token；否则 file。
func TypeOfFile(fileName, dir string) string {
	if p := typeByToken(typeTokenOfFile(fileName)); p != nil {
		return p.Token
	}
	dirBase := filepath.Base(filepath.ToSlash(dir))
	if p := typeByDir(dirBase); p != nil {
		return p.Token
	}
	return "file"
}

// typeTokenOfFile 提取文件名中的类型 token：`*.tool.md` 等 → tool；否则空。
func typeTokenOfFile(name string) string {
	low := strings.ToLower(name)
	for _, t := range Types {
		if strings.HasSuffix(low, "."+t.Token+".md") {
			return t.Token
		}
	}
	return ""
}

// SanitizeName 文件名净化：去路径分隔符与非法字符。
func SanitizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, name)
	return name
}

// NormalizeFileName 规范化原语文件名：净化 + 确保 .md 后缀（不产生双扩展）。
func NormalizeFileName(name string) string {
	name = SanitizeName(name)
	if strings.ToLower(filepath.Ext(name)) != ".md" {
		name += ".md"
	}
	return name
}

// FileName 生成 <净化名>.<token>.md（新建）；name 可带或不带 .md/类型后缀。
func FileName(name, token string) string {
	base := SanitizeName(name)
	low := strings.ToLower(base)
	// 去掉已带类型后缀/纯 .md
	if p := typeByToken(typeTokenOfFile(base)); p != nil {
		base = strings.TrimSuffix(base, "."+p.Token+".md")
	} else if strings.HasSuffix(low, ".md") {
		base = base[:len(base)-3]
	}
	base = strings.TrimSuffix(base, ".")
	return base + "." + token + ".md"
}

// PreviewDescription 读文件取 [description] 首行（列表描述预览，截断 60 字符）。
func PreviewDescription(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	doc := ParseDoc(string(raw))
	lines := strings.Split(doc.Description, "\n")
	if len(lines) == 0 {
		return "", nil
	}
	first := strings.TrimSpace(lines[0])
	if len(first) > 60 {
		first = first[:60] + "…"
	}
	return first, nil
}

// SafeJoin 拼接路径（相对 rel 在 root 下；绝对 rel 原样——归属已由调用方校验）。
func SafeJoin(root, rel string) string {
	if rel == "" {
		return root
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// RelOf 取 dir 相对 root 的相对路径（无法计算 → 空串）。
func RelOf(root, dir string) string {
	rel, _ := filepath.Rel(root, dir)
	return rel
}

// StrictlyWithin 判定 child 是否**严格**落在 root 内（root 自身不算；纯字符串前缀判定，
// 目标路径可以尚不存在）。用于移动/改名的目标作用域复检（G-26）。
func StrictlyWithin(root, child string) bool {
	r := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(root)), "/")
	c := filepath.ToSlash(filepath.Clean(child))
	return strings.HasPrefix(c, r+"/")
}
