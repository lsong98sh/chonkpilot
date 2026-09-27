// 域工具契约统一走 embed（server/contracts/tools/*.tool.md），替代代码内硬编码：对齐
// 「契约即文件、各工程自有 tools/skills/prompts/agents 放各自 contracts 目录」规范。
// 域工具无外部 executor，契约 embed 进 chonkpilot-server 二进制（执行它的进程）。
//
// 25 §8.1 #8（T6，2026-09-25）：**agent 不再 embed** —— 场景 agent 与出厂场景
// 统一为 **app 级场景**（`<exeDir>/scenarios/<场景>/`，与 `<exeDir>/capability/` 平级，
// 随发布只读资源），经数据层门面读取（见 domainmcp.go registerDomainAgents），
// 本文件只保留域**工具**契约 embed。
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed contracts/tools/*.tool.md
var domainToolsFS embed.FS

// AgentDef 是 app 级场景内的 agent 定义（来自 `<exeDir>/scenarios/*/` 内的 agent 文件：
// `# 名` + [description] + [content]，content = 该 agent 的提示词）。
type AgentDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// loadDomainTools 从 embed 契约加载全部域工具。
// 文件名（不含 .tool.md）= 工具名；排序保证注册顺序稳定。
func loadDomainTools() ([]ToolDef, error) {
	entries, err := domainToolsFS.ReadDir("contracts/tools")
	if err != nil {
		return nil, fmt.Errorf("读域工具契约目录: %w", err)
	}
	var defs []ToolDef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tool.md") {
			continue
		}
		md, err := domainToolsFS.ReadFile("contracts/tools/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("读 %s: %w", e.Name(), err)
		}
		desc, params, meta, err := parseToolMD(string(md))
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", e.Name(), err)
		}
		defs = append(defs, ToolDef{
			Name:        strings.TrimSuffix(e.Name(), ".tool.md"),
			Description: desc,
			Parameters:  params,
			Category:    meta["category"],
			Async:       meta["async"],
			AsyncTh:     atoiField(meta["async-threshold"]),
			Timeout:     atoiFieldSigned(meta["timeout"]),
			// timeout 键**显式声明**（非空）即标记（含 0/-1 = 无上限）；键缺失/空串 = 未设置（不写 _meta）。
			TimeoutSet: metaKeySet(meta, "timeout"),
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs, nil
}

// parseToolMD 解析域工具契约 md：取 [description] 段（LLM 视角描述）、
// [parameters] 段（JSON schema）与 [meta] 段（key=value；async/category/timeout 等透出用）。
func parseToolMD(md string) (desc string, params map[string]any, meta map[string]string, err error) {
	segs := parseSectionMD(md)
	desc = segs["description"]
	meta = parseMetaLines(segs["meta"])
	p := segs["parameters"]
	if p == "" {
		return desc, nil, meta, nil
	}
	if err := json.Unmarshal([]byte(p), &params); err != nil {
		return "", nil, nil, fmt.Errorf("parameters 段非合法 JSON: %w", err)
	}
	return desc, params, meta, nil
}

// parseMetaLines 解析 [meta] 段（每行 key=value，首个 `=` 分割）为键值映射。
func parseMetaLines(s string) map[string]string {
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

// atoiField 取契约 meta 的整数字段（空/非法 → 0；对齐 25-mcp-server atoiSafe 语义）。
func atoiField(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// atoiFieldSigned 同 atoiField 但**保留负值**（meta.timeout 的 -1 = 无上限）；空/非法 → 0。
func atoiFieldSigned(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// metaKeySet 报告 [meta] 段是否**显式声明**了该键（值非空才算；键缺失/空串 = 未设置）。
func metaKeySet(m map[string]string, key string) bool {
	v, ok := m[key]
	return ok && strings.TrimSpace(v) != ""
}

// parseSectionMD 通用 markdown 分段解析：取 [section] 标题与内容映射。
func parseSectionMD(md string) map[string]string {
	segs := map[string]string{}
	cur := ""
	var b strings.Builder
	for _, ln := range strings.Split(md, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") && len(t) > 2 {
			if cur != "" {
				segs[cur] = strings.TrimSpace(b.String())
				b.Reset()
			}
			cur = strings.Trim(t, "[]")
			continue
		}
		if cur != "" {
			b.WriteString(ln)
			b.WriteString("\n")
		}
	}
	if cur != "" {
		segs[cur] = strings.TrimSpace(b.String())
	}
	return segs
}
