// 契约加载（25-mcp-server，v2 2026-10-14）：
// 递归扫描契约根，按文件后缀识别四原语（*.tool.md / *.skill.md / *.prompt.md / *.resource.md），
// 解析委托 **contractmd**（H1 分区 + yaml/json 代码块，唯一实现）；本包不再自持分区解析。
package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/contractmd"
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

// ─── 解析（委托 contractmd 单一实现） ─────────────────────────
// v2 起分区解析（H1 分区 + yaml/json 代码块）统一由
// github.com/chonkpilot/chonkpilot-lib/contractmd 提供；本包不再自持 splitSections/parseMeta/firstHeading。

// parseSchemaJSON 解析参数段（v2 为 JSON 文本；容错回落 YAML，便于手写契约）。
func parseSchemaJSON(s string) (map[string]any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		return m, nil
	}
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
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
	Async       string         // meta: async（auto|always|never|manual，缺省 auto）
	AsyncTh     int            // meta: async-threshold（秒；async=auto 下超时自动转后台）
	Idempotent  bool           // meta: idempotent（可重试性；缺省 false = 安全默认，见 G-20）
	Timeout     int            // meta: timeout（秒；执行硬上限。0/-1 = 无上限；仅 TimeoutSet 时有效）
	TimeoutSet  bool           // meta: timeout 键是否**显式声明**（区分「未设置=回落全局」与「0/-1=无上限」）
	Args        string         // meta: args（命令行模板，{param}/{RAW-INPUT-FILE}/{RESULT-OUTPUT-FILE}）
	Output      string         // meta: output（stdout|code|file，缺省 stdout）
	Description string         // [description] 多行描述
	Schema      map[string]any // [parameters] YAML → JSON Schema
}

// categoryServer 标记「由 server/gateway 自持（in-memory 节点）提供、无 executor runtime」的工具契约
// （如 capability/tools/core/dsl_run.tool.md —— 其运行体是 gateway 的 dsl 节点）。
// 此类契约**不作为 executor 工具注册**：mcp-server 侧无 runtime、不可用，热重扫时还会在 self 节点
// 复现为幽灵工具（self_dsl_run）。它们只作为域工具的**描述单源**（见 ServerTools）。
const categoryServer = "server"

// isServerTool 判定工具契约是否为 server 类别（category=server，大小写/空白不敏感）。
func isServerTool(d *ToolDoc) bool {
	return strings.EqualFold(strings.TrimSpace(d.Category), categoryServer)
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

// ServerTools 返回契约根 root 下全部 server 类别（category=server）工具，编译为 mcp.Tool
// （含契约 _meta 与 cfg 的用户级异步覆盖），按原语名索引。
//
// 这些契约**不注册为 executor 工具**（见 isServerTool）；本函数是它们唯一的读取入口，
// 供装配层把域工具（如 gateway 的 dsl_run）的定义**单源**交给其自持节点：gateway lib 不依赖本包
// （RB-2），故由装配层桥接注入。root 缺失 → 空 map（非错误）。
func ServerTools(root string, cfg *Config) (map[string]*mcp.Tool, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return map[string]*mcp.Tool{}, nil
		}
		return nil, err
	}
	if cfg == nil {
		cfg = DefaultConfig()
	}
	docs, err := loadTools(root)
	if err != nil {
		return nil, err
	}
	out := map[string]*mcp.Tool{}
	for _, d := range docs {
		if !isServerTool(d) {
			continue
		}
		t, err := buildTool(d, cfg)
		if err != nil {
			return nil, fmt.Errorf("build server tool %s: %w", d.Name, err)
		}
		out[d.Name] = t
	}
	return out, nil
}

// parseToolDoc 解析单个 *.tool.md（v2：H1 分区，委托 contractmd）。
func parseToolDoc(path string) (*ToolDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := contractmd.Parse(string(data))
	if err != nil {
		return nil, err
	}
	meta := c.Meta

	doc := &ToolDoc{
		Name:        strings.TrimSuffix(filepath.Base(path), suffixTool),
		Title:       c.Title,
		Dir:         filepath.Dir(path),
		Runtime:     meta["runtime"],
		Entry:       meta["entry"],
		Hot:         isTruthy(meta["hot"]) || isTruthy(meta["llm"]), // llm 已并入 hot（兼容旧契约）
		Category:    meta["category"],
		Async:       firstNonEmpty(meta["async"], "auto"),
		AsyncTh:     atoiSafe(meta["async-threshold"]),
		Idempotent:  isTruthy(meta["idempotent"]),
		Args:        meta["args"],
		Description: c.Description,
	}
	if v, ok := meta["timeout"]; ok && v != "" {
		if _, err := fmt.Sscanf(v, "%d", &doc.Timeout); err != nil {
			return nil, fmt.Errorf("bad timeout %q", v)
		}
		doc.TimeoutSet = true // 键显式声明（含 0/-1）；空串/键缺失 = 未设置
	}
	doc.Output = meta["output"]
	if doc.Output == "" {
		doc.Output = "stdout"
	}
	doc.Schema, err = parseSchemaJSON(c.Parameters)
	if err != nil {
		return nil, fmt.Errorf("parameters: %w", err)
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
	// 执行硬上限：键显式声明即透出（含 **0/-1 = 无上限**）；键缺失 = 不写（回落全局）。
	// 注意：此处 >0 之外的 0/-1 亦透出，供 UI/gateway 区分「未设置」与「无上限」。
	if d.TimeoutSet {
		ext["timeout"] = d.Timeout
	}
	// 可重试性（G-20，缺省 false = 安全默认）：仅**声明为 true** 时透出；
	// 缺失 = false（不可安全自动重试）——将来若要加重试，判据 = `_meta.idempotent == true`。
	if d.Idempotent {
		ext["idempotent"] = true
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
//   - threshold → _meta.async-threshold（仅 >0 写出；显式 0/-1 = 无阈值，不写出、保持契约现值）；
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
		c, err := contractmd.Parse(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		d := &SkillDoc{
			Name:        strings.TrimSuffix(filepath.Base(f), suffixSkill),
			Description: c.Description,
			Body:        c.Content,
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
		c, err := contractmd.Parse(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		d := &PromptDoc{
			Name:        strings.TrimSuffix(filepath.Base(f), suffixPrompt),
			Description: c.Description,
			Body:        c.Content,
		}
		if args, err := parsePromptArgs(c.Parameters); err != nil {
			return nil, fmt.Errorf("%s: arguments: %w", f, err)
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

// parsePromptArgs 解析参数段（JSON Schema 形态）为参数列表（按名称排序）。
func parsePromptArgs(s string) ([]*PromptArg, error) {
	schema, err := parseSchemaJSON(s)
	if err != nil {
		return nil, err
	}
	if schema == nil {
		return nil, nil
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
	MIMEType    string // 内容 MIME：meta `path` 有则按该文件后缀识别，否则 text/markdown
	Description string // [description]
	File        string // meta: path → 外部内容文件（非空 = 资源内容取自该文件、「无正文」）
	Path        string // RB-4 ①：来源契约文件路径（无 File 时按需读盘取「正文」，不驻留）
	Content     string // [content]（兼容兜底：仅无 Path/File 时使用）
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
		c, err := contractmd.Parse(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		name := strings.TrimSuffix(filepath.Base(f), suffixResource)
		// 外部内容文件（meta `path`，2026-10-14）：资源内容取自该文件（**无「正文」**），
		// mimetype 按其后缀识别；相对路径按契约文件所在目录解析。无 `path` → 契约「正文」
		// markdown，mimetype 恒 text/markdown。
		file := strings.TrimSpace(c.Meta["path"])
		if file != "" && !filepath.IsAbs(file) {
			file = filepath.Join(filepath.Dir(f), filepath.FromSlash(file))
		}
		mime := contractmd.DefaultMimetype
		if file != "" {
			mime = contractmd.MimetypeOfFile(file)
		}
		d := &ResourceDoc{
			URI:         contractmd.InferURI(name), // 隐式 uri（不落盘，注册时派生）
			Name:        name,
			MIMEType:    mime,
			Description: c.Description,
			File:        file,
			Path:        f, // RB-4 ①：内容不驻留，makeResourceHandler 按 Path 实时读盘取正文
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
