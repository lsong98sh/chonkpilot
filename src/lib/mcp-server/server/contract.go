// 契约加载（25-mcp-server，2026-08-30 定稿）：
// 递归扫描契约根，按文件后缀识别四原语（*.tool.md / *.skill.md / *.prompt.md / *.resource.md），
// 分区式解析（[meta] key=value / [description] 多行 / [parameters] YAML JSON Schema / [content]）。
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// ─── 契约后缀 ─────────────────────────────

const (
	suffixTool     = ".tool.md"
	suffixSkill    = ".skill.md"
	suffixPrompt   = ".prompt.md"
	suffixResource = ".resource.md"
)

// ─── 分区解析 ─────────────────────────────

// splitSections 按 `[section]` 头把文件分为多个分区（返回 分区名 → 内容）。
func splitSections(data []byte) map[string]string {
	m := map[string]string{}
	sec := ""
	var buf bytes.Buffer
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") && len(t) > 2 {
			if sec != "" {
				m[sec] = strings.TrimSpace(buf.String())
			}
			sec = t[1 : len(t)-1]
			buf.Reset()
			continue
		}
		if sec != "" {
			buf.WriteString(ln)
			buf.WriteString("\n")
		}
	}
	if sec != "" {
		m[sec] = strings.TrimSpace(buf.String())
	}
	return m
}

// firstHeading 取首行 `# xxx`（可读标题；不参与契约，原语名 = 文件名）。
func firstHeading(data []byte) (string, bool) {
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(t, "# ")), true
		}
		return "", false
	}
	return "", false
}

// parseMeta 解析 [meta] 分区（每行 key=value，首个 `=` 分割）。
func parseMeta(s string) map[string]string {
	m := map[string]string{}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.IndexByte(ln, '='); i > 0 {
			m[strings.TrimSpace(ln[:i])] = strings.TrimSpace(ln[i+1:])
		}
	}
	return m
}

// ─── 递归扫描 ─────────────────────────────

// scanFolder 递归扫描契约根，按后缀分组返回文件路径。
func scanFolder(root string) (tools, skills, prompts, resources []string, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		switch {
		case strings.HasSuffix(name, suffixTool):
			tools = append(tools, path)
		case strings.HasSuffix(name, suffixSkill):
			skills = append(skills, path)
		case strings.HasSuffix(name, suffixPrompt):
			prompts = append(prompts, path)
		case strings.HasSuffix(name, suffixResource):
			resources = append(resources, path)
		}
		return nil
	})
	return
}

// ─── Tool 契约（*.tool.md） ─────────────────────────

// ToolDoc 是 *.tool.md 契约（25-mcp-server；要素对齐 dist/scripts/markitdown.json）。
// 注意：category 由 meta 声明（缺省无分类）；分类目录归属不影响原语名（原语名 = 文件名）。
type ToolDoc struct {
	Name        string         // 原语名 = 文件名（不含 .tool.md）
	Title       string         // H1 可读标题
	Dir         string         // 契约文件所在目录（entry 相对解析基准）
	Runtime     string         // meta: runtime（执行器 exe/PATH 名，或解释器如 python/node）
	Entry       string         // meta: entry（已废弃；兼容读取，解释器类遗留）
	Hot         bool           // meta: hot（吸收原 llm；固定暴露标志）
	Category    string         // meta: category（分类声明；缺省 = 契约根目录名派生）
	TitleMeta   string         // meta: title（一句话标题；MCP Tool.title）
	Async       string         // meta: async（auto|always|never|manual，缺省 auto）
	AsyncTh     int            // meta: async-threshold（秒；async=auto 下超时自动转后台）
	Timeout     int            // meta: timeout（秒；fail 语义，仅 async=never 下生效；0 = 用 cfg.TimeoutSec）
	Args        string         // meta: args（命令行模板，{param}/{RAW-INPUT-FILE}/{RESULT-OUTPUT-FILE}）
	Output      string         // meta: output（stdout|code|file，缺省 stdout）
	Description string         // [description] 多行描述
	Schema      map[string]any // [parameters] YAML → JSON Schema
}

// loadTools 递归扫描 folder 加载全部 *.tool.md。
func loadTools(folder string) ([]*ToolDoc, error) {
	files, _, _, _, err := scanFolder(folder)
	if err != nil {
		return nil, err
	}
	var docs []*ToolDoc
	for _, f := range files {
		d, err := parseToolDoc(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, nil
}

// parseToolDoc 解析单个 *.tool.md（分区式）。
func parseToolDoc(path string) (*ToolDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sec := splitSections(data)
	meta := parseMeta(sec["meta"])

	doc := &ToolDoc{
		Name:        strings.TrimSuffix(filepath.Base(path), suffixTool),
		Dir:         filepath.Dir(path),
		Runtime:     meta["runtime"],
		Entry:       meta["entry"],
		Hot:         isTruthy(meta["hot"]) || isTruthy(meta["llm"]), // llm 已并入 hot（兼容旧契约）
		Category:    meta["category"],
		TitleMeta:   meta["title"],
		Async:       firstNonEmpty(meta["async"], "auto"),
		AsyncTh:     atoiSafe(meta["async-threshold"]),
		Args:        meta["args"],
		Description: sec["description"],
	}
	if v := meta["timeout"]; v != "" {
		if _, err := fmt.Sscanf(v, "%d", &doc.Timeout); err != nil {
			return nil, fmt.Errorf("bad timeout %q", v)
		}
	}
	doc.Output = meta["output"]
	if doc.Output == "" {
		doc.Output = "stdout"
	}
	if h, ok := firstHeading(data); ok {
		doc.Title = h
	}
	if p := sec["parameters"]; p != "" {
		var schema map[string]any
		if err := yaml.Unmarshal([]byte(p), &schema); err != nil {
			return nil, fmt.Errorf("parameters yaml: %w", err)
		}
		doc.Schema = schema
	}
	if doc.Name == "" || doc.Description == "" {
		return nil, fmt.Errorf("name/description required")
	}
	return doc, nil
}

// buildTool 把 ToolDoc 编译为 mcp.Tool（LLM 面：name/description/schema；可选 title 标注）。
// 官方 SDK 要求 InputSchema 非空且顶层 type=object（AddTool 校验），空 schema 补 {"type":"object"}。
// cfg 提供用户级工具异步覆盖（usr 键 tool_async）：**仅在配置了该项时**改写暴露 _meta，
// 未配置的工具一律保持契约现值（契约现行为不被改变）。
func buildTool(d *ToolDoc, cfg *Config) (*mcp.Tool, error) {
	tool := &mcp.Tool{
		Name:        d.Name,
		Description: d.Description,
	}
	if len(d.Schema) > 0 {
		if _, err := json.Marshal(d.Schema); err != nil {
			return nil, fmt.Errorf("marshal schema: %w", err)
		}
		// 顶层 type 校验/补齐（禁止裸传无 type 的 schema）
		switch typ, _ := d.Schema["type"].(string); typ {
		case "object":
			tool.InputSchema = d.Schema
		case "":
			schema := map[string]any{"type": "object"}
			for k, v := range d.Schema {
				schema[k] = v
			}
			tool.InputSchema = schema
		default:
			return nil, fmt.Errorf("tool %s: input schema must have type \"object\", got %q", d.Name, typ)
		}
	} else {
		tool.InputSchema = map[string]any{"type": "object"}
	}
	if d.Title != "" {
		tool.Title = d.Title
	}
	// ChonkPilot 扩展 meta（不进 LLM schema；_meta 透出供 gateway/server 消费，72-工具开发规范）
	ext := map[string]any{}
	if d.Hot {
		ext["hot"] = true
	}
	if d.Category != "" {
		ext["category"] = d.Category
	}
	if d.Async != "" && d.Async != "auto" {
		ext["async"] = d.Async
	}
	if d.AsyncTh > 0 {
		ext["async-threshold"] = d.AsyncTh
	}
	if d.Timeout > 0 {
		ext["timeout"] = d.Timeout
	}
	// 用户级工具异步配置覆盖（usr 键 tool_async，四档）——配置该项时才改写上面按契约写入的值；
	// 未配置 → 原样（契约现值语义不变）。hard_timeout 不进 _meta（仅 executor 执行硬上限）。
	if cfg != nil {
		if ov, ok := cfg.toolAsyncOverride(d.Name); ok {
			applyToolAsyncOverride(ext, d, ov)
		}
	}
	if len(ext) > 0 {
		tool.Meta = mcp.Meta(ext)
	}
	return tool, nil
}

// applyToolAsyncOverride 把用户级覆盖写入暴露 _meta（ext）：
//   - mode → _meta.async（沿用现状口径：**auto 不显式透出**；惟契约声明了非 auto
//     （never/always/manual）时须显式写 "auto" 才能把契约值压回自动档）；
//   - threshold → _meta.async-threshold（仅 >0；否则保持契约的 async-threshold）；
//   - hard_timeout **不写**（gateway 侧 _meta.timeout 语义 = 超时裁决点，不可被硬上限污染；
//     硬上限只在 executor 生效，见 resolveExecTimeout）。
//
// 优先级（用户口径）：调用级 args.async/args.timeout（gateway doCall，最高）> 本覆盖
// > 契约 _meta > 软缺省（第三方无声明 → never）。即覆盖只改写**契约默认**，不剥夺调用级覆盖。
func applyToolAsyncOverride(ext map[string]any, d *ToolDoc, ov ToolAsyncOverride) {
	switch {
	case ov.Mode == "auto":
		if d.Async != "" && d.Async != "auto" {
			ext["async"] = "auto"
		} else {
			delete(ext, "async") // 契约本身即 auto/未声明 → 维持「auto 不显式透出」
		}
	case ov.Mode != "":
		ext["async"] = ov.Mode
	}
	if ov.Threshold > 0 {
		ext["async-threshold"] = ov.Threshold
	}
}

// ─── Skill 契约（*.skill.md） ─────────────────────────

// SkillDoc 是 skill 契约：description + content（执行指引）。
// skill 注册为标准 prompt，_meta.type=skill（与 *.prompt.md 区分）。
type SkillDoc struct {
	Name        string // 原语名 = 文件名
	Description string
	Body        string // [content]
}

// loadSkills 递归扫描 folder 加载全部 *.skill.md。
func loadSkills(folder string) ([]*SkillDoc, error) {
	_, files, _, _, err := scanFolder(folder)
	if err != nil {
		return nil, err
	}
	var docs []*SkillDoc
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		sec := splitSections(data)
		d := &SkillDoc{
			Name:        strings.TrimSuffix(filepath.Base(f), suffixSkill),
			Description: sec["description"],
			Body:        sec["content"],
		}
		if d.Name == "" || d.Description == "" {
			return nil, fmt.Errorf("%s: name/description required", f)
		}
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, nil
}

// ─── Prompt 契约（*.prompt.md） ─────────────────────────

// PromptDoc 是 prompt 契约：meta（category）+ description + arguments（[arguments] JSON Schema）+ content（模板，{{arg}} 占位）。
type PromptDoc struct {
	Name        string
	Description string
	Arguments   []*PromptArg
	Body        string // [content]
}

// PromptArg 是 prompt 模板参数。
type PromptArg struct {
	Name        string
	Description string
	Required    bool
}

// loadPrompts 递归扫描 folder 加载全部 *.prompt.md。
func loadPrompts(folder string) ([]*PromptDoc, error) {
	_, _, files, _, err := scanFolder(folder)
	if err != nil {
		return nil, err
	}
	var docs []*PromptDoc
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		sec := splitSections(data)
		d := &PromptDoc{
			Name:        strings.TrimSuffix(filepath.Base(f), suffixPrompt),
			Description: sec["description"],
			Body:        sec["content"],
		}
		if args, err := parsePromptArgs(sec["arguments"]); err != nil {
			return nil, fmt.Errorf("%s: arguments yaml: %w", f, err)
		} else {
			d.Arguments = args
		}
		if d.Name == "" || d.Description == "" {
			return nil, fmt.Errorf("%s: name/description required", f)
		}
		docs = append(docs, d)
	}
	return docs, nil
}

// parsePromptArgs 解析 [arguments]（JSON Schema 形态）为参数列表（按名称排序）。
func parsePromptArgs(s string) ([]*PromptArg, error) {
	var schema map[string]any
	if err := yaml.Unmarshal([]byte(s), &schema); err != nil {
		return nil, err
	}
	required := map[string]bool{}
	if r, ok := schema["required"].([]any); ok {
		for _, v := range r {
			if name, ok := v.(string); ok {
				required[name] = true
			}
		}
	}
	var args []*PromptArg
	if props, ok := schema["properties"].(map[string]any); ok {
		names := make([]string, 0, len(props))
		for k := range props {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			pv, _ := props[k].(map[string]any)
			desc, _ := pv["description"].(string)
			args = append(args, &PromptArg{Name: k, Description: desc, Required: required[k]})
		}
	}
	return args, nil
}

// buildPrompt 把 PromptDoc 编译为 mcp.Prompt（含参数定义）。
func buildPrompt(d *PromptDoc) *mcp.Prompt {
	p := &mcp.Prompt{
		Name:        d.Name,
		Description: d.Description,
	}
	for _, a := range d.Arguments {
		p.Arguments = append(p.Arguments, &mcp.PromptArgument{
			Name:        a.Name,
			Description: a.Description,
			Required:    a.Required,
		})
	}
	return p
}

// ─── Resource 契约（*.resource.md） ─────────────────────────

// ResourceDoc 定义静态资源（uri/name/description/mimetype/path/content）。
type ResourceDoc struct {
	URI         string // meta: uri（缺省 file://<name>）
	Name        string // 原语名 = 文件名
	MIMEType    string // meta: mimetype
	Description string // [description]
	Path        string // RB-4 ①：来源契约文件路径（内容按需读盘，不驻留）
	Content     string // [content]（兼容兜底：仅无 Path 时使用）
}

// loadResources 递归扫描 folder 加载全部 *.resource.md。
func loadResources(folder string) ([]*ResourceDoc, error) {
	_, _, _, files, err := scanFolder(folder)
	if err != nil {
		return nil, err
	}
	var docs []*ResourceDoc
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		sec := splitSections(data)
		meta := parseMeta(sec["meta"])
		d := &ResourceDoc{
			URI:         meta["uri"],
			Name:        strings.TrimSuffix(filepath.Base(f), suffixResource),
			MIMEType:    meta["mimetype"],
			Description: sec["description"],
			Path:        f, // RB-4 ①：内容不驻留，makeResourceHandler 按 Path 实时读盘取 [content]
		}
		if d.Name == "" || d.Description == "" {
			return nil, fmt.Errorf("%s: name/description required", f)
		}
		docs = append(docs, d)
	}
	return docs, nil
}

// isTruthy 判定 meta 布尔值（true/1/yes）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func atoiSafe(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y":
		return true
	}
	return false
}
