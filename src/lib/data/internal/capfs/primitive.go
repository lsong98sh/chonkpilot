// mcp 四原语契约解析/组装 + 文件名规范化（原 persist/persist_knowledge.go 的契约段逐字下移）。
//
// 文件格式 = mcp 四原语统一契约（# 标题 + [meta] k=v + [description] + [parameters]|[arguments]
// + [content]，与 chonkpilot-mcp-server/server/contract.go 一致）；capability 根下 **6 个扁平
// 子目录**：`prompts/` `tools/` `resources/` `skills/` `agents/` `scenarios/`（**删除**旧
// `knowledge/**` 归并层），见 capfs 包注释。
package capfs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Type 一类能力面条目（目录名复数 ↔ 文件后缀单数 ↔ 显示名）。
type Type struct {
	Dir   string // 类型目录名（prompts/tools/resources/skills/agents/scenarios）
	Rel   string // 相对 capability 根的物理路径（= Dir；扁平子目录）
	Token string // 文件后缀 token（prompt/tool/resource/skill/agent/scenario）
	Label string // 显示名（提示词/工具/资源/技能/智能体/场景）
}

// Types 是 6 类能力面条目的规范清单（顺序即知识库根下的展示顺序）。
// Rel = 相对 capability 根的物理路径（**扁平**：Rel == Dir；旧 `knowledge/**` 归并层已删除）。
// 场景（scenario）为**目录**形态（`scenarios/<场景目录>/`，非 `*.scenario.md` 文件）——
// 目录/文件规则沿用现状（见 scenario.go）；此处仅提供类型名映射。
var Types = []Type{
	{Dir: DirPrompts, Rel: DirPrompts, Token: "prompt", Label: "提示词"},
	{Dir: DirTools, Rel: DirTools, Token: "tool", Label: "工具"},
	{Dir: DirResources, Rel: DirResources, Token: "resource", Label: "资源"},
	{Dir: DirSkills, Rel: DirSkills, Token: "skill", Label: "技能"},
	{Dir: DirAgents, Rel: DirAgents, Token: "agent", Label: "智能体"},
	{Dir: DirScenarios, Rel: DirScenarios, Token: "scenario", Label: "场景"},
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
	default: // skill / agent / scenario（最小 doc：仅标题 + 描述）
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

// memoryCategoryNameMaxRunes 记忆类别名长度上限（字符数，防超长文件名）。
const memoryCategoryNameMaxRunes = 64

// memoryReservedNames Windows 保留设备名（拼上 .md 后仍是保留设备，故整体拒绝）。
var memoryReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// ValidMemoryCategoryName 校验记忆类别名的文件名字符安全性（A-37 单源，memory 域 + config 域
// 记忆类别提示词文件复用）：非空、限长、无首尾空白、无空白/控制字符、禁路径分隔符与 Windows
// 保留字符（/ \ : * ? " < > |）、首字符非 '.'（避免 "."/".."/隐藏文件）、非保留设备名。
// 类别名将拼为 `<类别>.md` 与 `memory/<类别>.md`，故含 `..` / 分隔符即路径穿越 → 必须拒绝。
func ValidMemoryCategoryName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > memoryCategoryNameMaxRunes {
		return false
	}
	if name != strings.TrimSpace(name) || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return false
		}
	}
	return !memoryReservedNames[strings.ToUpper(name)]
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

// MaxDocReadBytes 是 capability 文本文件（契约文档 / 记忆类别文件 / 系统提示词文档）整读的
// **尺寸上限**（A-40，洪泛防护）：超限拒绝整读，避免单一超大文件把内存读爆。口径取 8MB ——
// 承载的是用户可编辑的系统文档 / 记忆全文，故比 filesys 的 512KB 文本预览口径更宽。
const MaxDocReadBytes = 8 << 20

// ReadFileCapped 读文件（读前 os.Stat 判尺寸上限）：超限或 stat 失败 → 报错，不整读入内存（A-40）。
func ReadFileCapped(path string, maxBytes int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxBytes {
		return nil, fmt.Errorf("file %s exceeds read limit (%d > %d bytes)", path, st.Size(), maxBytes)
	}
	return os.ReadFile(path)
}

// PreviewDescription 读文件取 [description] 首行（列表描述预览，截断 60 字符）。
func PreviewDescription(path string) (string, error) {
	raw, err := ReadFileCapped(path, MaxDocReadBytes)
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

// RealPathOrAncestor 解析 path 的符号链接**真实落点**：path 存在 → EvalSymlinks 自身；不存在
// （新建目标）→ 逐级上溯到**最近存在的祖先**解析后回拼尚不存在的路径段。已到根仍不可解析
// （异常）→ ok=false。用于写前 symlink 越界复验（A-29/A-32），各域共用避免重复实现。
func RealPathOrAncestor(path string) (string, bool) {
	p := filepath.Clean(path)
	var missing []string
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", false
		}
		missing = append(missing, filepath.Base(p))
		p = parent
	}
}

// RealPathWithin 写前越界复验（A-32）：StrictlyWithin 仅纯词法校验，root 内指向 root 外的 symlink
// （或 symlink 目标）会被 os.WriteFile / os.MkdirAll / os.RemoveAll 跟随越界。此处解析 path 的
// 真实落点（不存在则解析最近存在的祖先后回拼，见 RealPathOrAncestor）后复判仍**严格**落在 root
// 内；越界 / 不可解析 → false。
func RealPathWithin(root, path string) bool {
	real, ok := RealPathOrAncestor(path)
	if !ok {
		return false
	}
	return StrictlyWithin(root, real)
}

// realTrustRoot 解析信任根自身的真实落点（符号链接前缀先解析；失败回落字面）。
func realTrustRoot(root string) string {
	if r, ok := RealPathOrAncestor(root); ok {
		return r
	}
	return root
}

// RealPathInside 写/删前越界复验（A-36/A-38）：与 RealPathWithin 同法，但**先解析 root 自身**的
// 符号链接前缀再复判 path 严格落在 root 内。capfs 各写/删入口的 root 由调用方按级别根拼接而来
// （workdir / usrPath 可能带符号链接），故不能以字面 root 直接比对；解析 root 后再比对 → 既
// 不误判合法请求，又能拦下 root **内部**指向根外的 symlink（如 mcps/、system/ 被链接到根外）。
// 越界 / 不可解析 → false。
func RealPathInside(root, path string) bool {
	return RealPathWithin(realTrustRoot(root), path)
}
