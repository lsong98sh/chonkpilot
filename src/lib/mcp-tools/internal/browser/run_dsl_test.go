package browser

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// TestBrowserScriptParse：browser 动词（Raw）+ 核心流控 + EVL/EXP <<< 多行可解析。
func TestBrowserScriptParse(t *testing.T) {
	r := &Runner{}
	actions := browserActions(r)
	// 文件/目录引用一律绝对路径（R-11）：落盘（SHT）、数据源（LOOP）、IF exist 均示例为绝对
	shot := filepath.ToSlash(filepath.Join(t.TempDir(), "shot.png"))
	cases := filepath.ToSlash(filepath.Join(t.TempDir(), "cases.csv"))
	flag := filepath.ToSlash(filepath.Join(t.TempDir(), "done.flag"))
	script := `
### 打开与操作
OPN "https://example.com"
WAT 3s css:main
LOOP row=#"` + cases + `".lines.range(1,-1)
   FILL "css:#kw" "查询 {{row}}"
   CLK "css:#btn"
   SLP 200
END
EXP page title "Example Domain"
EVL "js <<<
document.querySelector('h1').textContent
>>> return document.title;"
IF exist "` + flag + `"
   SHT "` + shot + `"
END
`
	ast, err := dsl.Parse(script, actions)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rawCount := 0
	var walk func(sts []dsl.Stmt)
	walk = func(sts []dsl.Stmt) {
		for _, st := range sts {
			switch n := st.(type) {
			case *dsl.ActionStmt:
				if !n.RawArgsMode {
					t.Fatalf("动词 %s 应为 Raw 模式", n.Verb)
				}
				rawCount++
			case *dsl.IfStmt:
				walk(n.Block)
			case *dsl.LoopStmt:
				walk(n.Block)
			case *dsl.ParallelStmt:
				for _, b := range n.Block {
					walk([]dsl.Stmt{b})
				}
			}
		}
	}
	walk(ast.Stmts)
	// OPN/WAT/FILL/CLK/SLP/EXP/EVL/SHT = 8 条动作
	if rawCount != 8 {
		t.Fatalf("raw 动作数 = %d, want 8", rawCount)
	}
	// EVL 多行块提取
	if js, ok := blockJS(`"js <<<
document.querySelector('h1').textContent
>>> return document.title;"`); !ok || js == "" {
		t.Fatalf("blockJS 提取失败: %q", js)
	}
}

// TestBrowserUnknownVerb：未注册动词解析报错。
func TestBrowserUnknownVerb(t *testing.T) {
	r := &Runner{}
	if _, err := dsl.Parse("FLY xxx\n", browserActions(r)); err == nil {
		t.Fatal("未知动词应报错")
	}
}
