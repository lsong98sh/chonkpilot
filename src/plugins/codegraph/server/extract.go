package server

import (
	"fmt"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// defKinds: 每种语言「节点类型 → 符号类别」
// nameKinds: 可充当"名字"的叶子节点类型（无 name 字段时回退查找）
// decisionKinds: 计入圈复杂度的分支/循环节点类型
var defKinds = map[string]map[string]string{
	"go":         {"function_declaration": "func", "method_declaration": "method", "type_declaration": "type", "type_spec": "type"},
	"javascript": {"function_declaration": "func", "generator_function_declaration": "func", "class_declaration": "class", "method_definition": "method"},
	"typescript": {"function_declaration": "func", "generator_function_declaration": "func", "class_declaration": "class", "abstract_class_declaration": "class", "method_definition": "method", "interface_declaration": "interface", "type_alias_declaration": "type", "enum_declaration": "enum"},
	"tsx":        {"function_declaration": "func", "generator_function_declaration": "func", "class_declaration": "class", "abstract_class_declaration": "class", "method_definition": "method", "interface_declaration": "interface", "type_alias_declaration": "type", "enum_declaration": "enum"},
	"python":     {"function_definition": "func", "class_definition": "class"},
	"rust":       {"function_item": "func", "struct_item": "type", "enum_item": "enum", "trait_item": "trait", "type_item": "type"},
	"java":       {"class_declaration": "class", "interface_declaration": "interface", "enum_declaration": "enum", "record_declaration": "class", "method_declaration": "method", "constructor_declaration": "constructor"},
}

var nameKinds = map[string]map[string]bool{
	"go":         {"identifier": true, "type_identifier": true},
	"javascript": {"identifier": true, "property_identifier": true, "type_identifier": true},
	"typescript": {"identifier": true, "property_identifier": true, "type_identifier": true},
	"tsx":        {"identifier": true, "property_identifier": true, "type_identifier": true},
	"python":     {"identifier": true},
	"rust":       {"identifier": true, "type_identifier": true},
	"java":       {"identifier": true},
}

var decisionKinds = map[string]map[string]bool{
	"go":         {"if_statement": true, "for_statement": true, "expression_switch_statement": true, "expression_case": true, "default_case": true, "type_switch_statement": true, "type_case": true, "select_statement": true, "communication_case": true},
	"javascript": {"if_statement": true, "for_statement": true, "for_in_statement": true, "while_statement": true, "do_statement": true, "switch_statement": true, "switch_case": true, "catch_clause": true, "ternary_expression": true},
	"typescript": {"if_statement": true, "for_statement": true, "for_in_statement": true, "while_statement": true, "do_statement": true, "switch_statement": true, "switch_case": true, "catch_clause": true, "ternary_expression": true},
	"tsx":        {"if_statement": true, "for_statement": true, "for_in_statement": true, "while_statement": true, "do_statement": true, "switch_statement": true, "switch_case": true, "catch_clause": true, "ternary_expression": true},
	"python":     {"if_statement": true, "elif_clause": true, "for_statement": true, "while_statement": true, "try_statement": true, "except_clause": true},
	"rust":       {"if_expression": true, "if_let_expression": true, "for_expression": true, "while_expression": true, "loop_expression": true, "match_expression": true, "match_arm": true},
	"java":       {"if_statement": true, "for_statement": true, "enhanced_for_statement": true, "while_statement": true, "do_statement": true, "switch_statement": true, "switch_block_statement_group": true, "catch_clause": true, "ternary_expression": true},
}

// importKinds: 产生依赖边的导入节点类型
var importKinds = map[string]map[string]bool{
	"go":         {"import_declaration": true},
	"javascript": {"import_statement": true, "export_statement": true},
	"typescript": {"import_statement": true, "export_statement": true},
	"tsx":        {"import_statement": true, "export_statement": true},
	"python":     {"import_statement": true, "import_from_statement": true},
	"rust":       {"use_declaration": true},
	"java":       {"import_declaration": true},
}

// parseFile 解析单个文件并提取符号/导入/复杂度。langName 必须是 langs 里的逻辑名。
func parseFile(langName, path string, src []byte) (*FileInfo, error) {
	spec, ok := extSpecFor(langName)
	if !ok {
		return nil, fmt.Errorf("unsupported lang %q", langName)
	}
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(spec.make()); err != nil {
		return nil, fmt.Errorf("set language %s: %w", langName, err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse returned nil tree")
	}
	defer tree.Close()

	fi := &FileInfo{Path: path, Lang: langName}
	root := tree.RootNode()
	fi.HasErr = root.HasError()

	dec := decisionKinds[langName]
	imports := importKinds[langName]
	df := defKinds[langName]
	nk := nameKinds[langName]

	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		k := n.Kind()
		if imports[k] {
			fi.Imports = append(fi.Imports, extractImports(langName, n, src)...)
		}
		if sk, isDef := df[k]; isDef {
			name := defName(n, nk, src)
			if name != "" {
				kind := sk
				if sk == "func" && isInsideKind(langName, n) {
					kind = "method"
				}
				cc := 1
				if kind == "func" || kind == "method" || kind == "constructor" {
					cc = complexityOf(dec, n)
				}
				fi.Symbols = append(fi.Symbols, Symbol{
					File: path, Kind: kind, Lang: langName, Name: name,
					Line: int(n.StartPosition().Row) + 1, EndLine: int(n.EndPosition().Row) + 1,
					Complexity: cc,
					Signature:  firstLine(src, n),
				})
			}
			// 类/结构体内的定义继续下钻（如 python 类内 def → method）
			walkChildren(n, walk)
			return
		}
		walkChildren(n, walk)
	}
	walk(root)
	return fi, nil
}

func walkChildren(n *ts.Node, walk func(*ts.Node)) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walk(n.NamedChild(uint(i)))
	}
}

// isInsideKind 函数定义是否位于"使其成为方法"的容器内（python class / rust impl,trait）。
func isInsideKind(lang string, n *ts.Node) bool {
	var parents []string
	switch lang {
	case "python":
		parents = []string{"class_definition"}
	case "rust":
		parents = []string{"impl_item", "trait_item"}
	default:
		return false
	}
	for p := n.Parent(); p != nil; p = p.Parent() {
		for _, k := range parents {
			if p.Kind() == k {
				return true
			}
		}
	}
	return false
}

// defName 取定义节点名字：优先 field "name"，否则在 named children 中找名字类节点。
func defName(n *ts.Node, nk map[string]bool, src []byte) string {
	if c := n.ChildByFieldName("name"); c != nil {
		return strings.TrimSpace(nodeText(src, c))
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(uint(i))
		if nk[c.Kind()] {
			return strings.TrimSpace(nodeText(src, c))
		}
	}
	return ""
}

func nodeText(src []byte, n *ts.Node) string {
	if src == nil || n == nil {
		return ""
	}
	b, e := int(n.StartByte()), int(n.EndByte())
	if b < 0 || b > len(src) || e < b || e > len(src) {
		return ""
	}
	return string(src[b:e])
}

// complexityOf 圈复杂度：1 + 子树内决策点计数（启发式，按语言决策节点集）。
func complexityOf(dec map[string]bool, n *ts.Node) int {
	count := 1
	var walk func(x *ts.Node)
	walk = func(x *ts.Node) {
		if x != n && dec[x.Kind()] {
			count++
		}
		walkChildren(x, walk)
	}
	walk(n)
	return count
}

// firstLine 定义首行文本（签名预览，截断 200）。
func firstLine(src []byte, n *ts.Node) string {
	b := int(n.StartByte())
	if b < 0 || b > len(src) {
		return ""
	}
	rest := src[b:]
	i := strings.IndexByte(string(rest), '\n')
	if i >= 0 {
		rest = rest[:i]
	}
	s := strings.TrimSpace(string(rest))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// extractImports 按语言提取 import 原语目标（返回原始串，解析交给 resolveImport）。
func extractImports(lang string, n *ts.Node, src []byte) []string {
	var out []string
	switch lang {
	case "go":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(uint(i))
			if c.Kind() != "import_spec" {
				continue
			}
			p := c.ChildByFieldName("path")
			if p == nil {
				continue
			}
			t := unquote(strings.TrimSpace(nodeText(src, p)))
			if t != "" && t != "_" && t != "." {
				out = append(out, t)
			}
		}
	case "javascript", "typescript", "tsx":
		if srcN := n.ChildByFieldName("source"); srcN != nil {
			if t := unquote(strings.TrimSpace(nodeText(src, srcN))); t != "" {
				out = append(out, t)
			}
		}
	case "python":
		if n.Kind() == "import_statement" {
			for i := 0; i < int(n.NamedChildCount()); i++ {
				c := n.NamedChild(uint(i))
				if c.Kind() == "dotted_name" {
					out = append(out, strings.TrimSpace(nodeText(src, c)))
				} else if c.Kind() == "aliased_import" {
					if d := firstNamed(c, "dotted_name"); d != nil {
						out = append(out, strings.TrimSpace(nodeText(src, d)))
					}
				}
			}
		} else { // import_from_statement
			if m := n.ChildByFieldName("module"); m != nil {
				if t := strings.TrimSpace(nodeText(src, m)); t != "" {
					out = append(out, t)
				}
			} else if d := firstNamed(n, "dotted_name"); d != nil { // 部分 grammar 无 module 字段
				if t := strings.TrimSpace(nodeText(src, d)); t != "" {
					out = append(out, t)
				}
			} else if r := firstNamed(n, "relative_import"); r != nil {
				out = append(out, strings.TrimSpace(nodeText(src, r)))
			}
		}
	case "rust":
		t := strings.TrimSpace(nodeText(src, n))
		t = strings.TrimSpace(strings.TrimPrefix(t, "use"))
		t = strings.TrimSuffix(strings.TrimSpace(t), ";")
		t = strings.Join(strings.Fields(t), "")
		if t != "" && t != "crate" && t != "self" && t != "super" {
			out = append(out, t)
		}
	case "java":
		t := strings.TrimSpace(nodeText(src, n))
		t = strings.TrimPrefix(t, "import")
		t = strings.TrimPrefix(strings.TrimSpace(t), "static")
		t = strings.TrimSuffix(strings.TrimSpace(t), ";")
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 {
		switch s[0] {
		case '"', '\'', '`':
			return s[1 : len(s)-1]
		}
	}
	return s
}

func firstNamed(n *ts.Node, kind string) *ts.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(uint(i)); c.Kind() == kind {
			return c
		}
	}
	return nil
}

// extSpecFor 由逻辑名反查 spec。
func extSpecFor(name string) (langSpec, bool) {
	for _, l := range langs {
		if l.name == name {
			return l, true
		}
	}
	return langSpec{}, false
}
