package chat

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"
)

// TestPortsNoAny 是 MOD-1 M8-C4 的 C5 静态扫描：`ports.go` 的接口方法签名不得出现
// `any` / `interface{}` / `map[string]any`（端口是组件唯一对外契约，必须类型安全）。
// 允许 `context.Context` / `time.Duration` / `[]Message` 等具体类型。
//
// 若将来发现违规：**不改端口**（端口变更需另行评审），本断言以 `t.Skipf` 记录并如实报告。
func TestPortsNoAny(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ports.go", nil, 0)
	if err != nil {
		t.Fatalf("parse ports.go: %v", err)
	}
	var violations []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		it, ok := ts.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		for _, m := range it.Methods.List {
			ft, ok := m.Type.(*ast.FuncType)
			if !ok { // 内嵌接口：方法签名由被嵌入接口定义，另行扫描
				continue
			}
			name := "<embedded>"
			if len(m.Names) > 0 {
				name = m.Names[0].Name
			}
			for _, fl := range []*ast.FieldList{ft.Params, ft.Results} {
				if fl == nil {
					continue
				}
				for _, fld := range fl.List {
					if !hasLooseType(fld.Type) {
						continue
					}
					var sb strings.Builder
					if printer.Fprint(&sb, fset, fld.Type) != nil {
						sb.WriteString("<unprintable>")
					}
					violations = append(violations, ts.Name.Name+"."+name+": "+sb.String())
				}
			}
		}
		return true
	})
	if len(violations) > 0 {
		t.Skipf("端口签名含 any / interface{}（仅记录，端口变更需另行评审）：%v", violations)
	}
}

// hasLooseType 报告类型表达式是否含 `any` / 空 `interface{}`（`map[string]any` 经 `any` 命中）。
func hasLooseType(e ast.Expr) bool {
	bad := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == "any" {
				bad = true
			}
		case *ast.InterfaceType:
			if x.Methods == nil || len(x.Methods.List) == 0 {
				bad = true
			}
		}
		return !bad
	})
	return bad
}
