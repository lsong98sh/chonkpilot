// contract 把 `*.tool.md` 契约直接变成 MCP 工具（插件复用层）。
//
// 契约仍是唯一源（`# 基本信息` YAML + `# 摘要` + `# 参数` JSON Schema），
// 本包只做「解析 + 注册 + 分发」，不关心具体怎么执行：
//
//	tools, _ := contract.LoadDir(filepath.Join(root, "tools", "file"))
//	contract.Register(srv, tools, func(ctx, name, args) (string, bool, error) { ... })
package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool 是一条已解析的工具契约。
type Tool struct {
	Name        string
	Title       string
	Description string
	Hot         bool
	Schema      map[string]any
}

// Handler 执行一次工具调用：返回文本 + 是否错误。
type Handler func(ctx context.Context, name string, args map[string]any) (text string, isError bool, err error)

// LoadDir 读取目录下所有 `*.tool.md`（按名称排序）。
func LoadDir(dir string) ([]Tool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取契约目录 %s: %w", dir, err)
	}
	out := make([]Tool, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tool.md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		t, err := Parse(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if t.Name == "" {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Parse 解析单个契约文本。
func Parse(text string) (Tool, error) {
	var t Tool
	if head := blockAfter(text, "```yaml"); head != "" {
		for _, line := range strings.Split(head, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			switch k {
			case "name":
				t.Name = v
			case "title":
				t.Title = v
			case "hot":
				t.Hot = strings.EqualFold(v, "true")
			}
		}
	}
	if js := blockAfter(text, "```json"); js != "" {
		if err := json.Unmarshal([]byte(js), &t.Schema); err != nil {
			return t, fmt.Errorf("参数 JSON 解析失败: %w", err)
		}
	}
	if t.Schema == nil {
		t.Schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	t.Description = describe(text, t.Title, t.Name)
	return t, nil
}

// Register 把契约注册成 MCP 工具（统一参数解析 / 错误封装 / hot 标记）。
func Register(srv *mcp.Server, tools []Tool, h Handler) error {
	if srv == nil {
		return fmt.Errorf("contract.Register: server is required")
	}
	for _, tool := range tools {
		tool := tool
		mt := &mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Schema}
		mt.Meta = mcp.Meta{}
		if tool.Hot {
			mt.Meta["hot"] = true
		}
		srv.AddTool(mt, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return Error("参数解析失败: " + err.Error()), nil
				}
			}
			text, isErr, err := h(ctx, tool.Name, args)
			if err != nil {
				return Error(err.Error()), nil
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: text}},
				IsError: isErr,
			}, nil
		})
	}
	return nil
}

// HotNames 返回需要注入 LLM 的工具名。
func HotNames(tools []Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Hot {
			out = append(out, t.Name)
		}
	}
	return out
}

// Error 构造一个 isError 的工具结果。
func Error(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}, IsError: true}
}

func blockAfter(text, lang string) string {
	i := strings.Index(text, lang)
	if i < 0 {
		return ""
	}
	rest := text[i+len(lang):]
	j := strings.Index(rest, "```")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func describe(text, title, name string) string {
	label := title
	if label == "" {
		label = name
	}
	i := strings.Index(text, "# 摘要")
	if i < 0 {
		return label
	}
	rest := text[i+len("# 摘要"):]
	if j := strings.Index(rest, "# "); j >= 0 {
		rest = rest[:j]
	}
	para := strings.TrimSpace(strings.SplitN(strings.TrimSpace(rest), "\n\n", 2)[0])
	para = strings.ReplaceAll(para, "\n", " ")
	if para == "" {
		return label
	}
	return label + "：" + para
}
