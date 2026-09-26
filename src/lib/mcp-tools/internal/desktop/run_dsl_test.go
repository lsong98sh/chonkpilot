package desktop

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// TestDesktopScriptParse：desktop 动词（Raw 原文）+ 核心流控混排可解析；
// 坐标/锚点/表达式等自由文法经原文透传不丢字符。
func TestDesktopScriptParse(t *testing.T) {
	// 文件/目录引用一律绝对路径（R-11）：数据源（LOOP）、IF exist 示例为绝对
	rows := filepath.ToSlash(filepath.Join(t.TempDir(), "rows.csv"))
	flag := filepath.ToSlash(filepath.Join(t.TempDir(), "flag.txt"))
	script := `
### 打开窗口并操作
WIN "记事本" max
WIN "notepad" focus
MOV 500,300
CLK D100,200
CLKR @30%,40%
DRG 10,10 -> 20,20 speed=8 jitter=2
INP "hello {{1}}"
SLP 100
LOOP row=#"` + rows + `".lines.range(1,-1)
   CLK @center
   INP "行 {{row}}"
END
IF exist "` + flag + `"
   MOV $X+10,$Y
END
`
	ctx := &runCtx{vars: map[string]float64{}}
	actions := desktopActions(ctx)
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
	// WIN×2 / MOV / CLK / CLKR / DRG / INP / SLP / 循环内 CLK+INP / IF 内 MOV = 11 条动作
	if rawCount != 11 {
		t.Fatalf("raw 动作数 = %d, want 11", rawCount)
	}
}

// TestDesktopCoreOnly：纯核心语法（无真实输入副作用）执行不报错。
func TestDesktopCoreOnly(t *testing.T) {
	ctx := &runCtx{vars: map[string]float64{}}
	actions := desktopActions(ctx)
	eng := dsl.NewEngine(dsl.Options{Files: scriptFS{}, Actions: actions, StopOnError: true})
	ast, err := dsl.Parse("SET \"a\" => x\nIF x == \"a\"\n   SET \"b\" => x\nEND\n", actions)
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Execute(ast)
	res := eng.Result()
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
}

// TestDesktopUnknownVerb：未注册动词解析报错。
func TestDesktopUnknownVerb(t *testing.T) {
	ctx := &runCtx{vars: map[string]float64{}}
	actions := desktopActions(ctx)
	if _, err := dsl.Parse("FLY 1,2\n", actions); err == nil {
		t.Fatal("未知动词应报错")
	}
}

// TestDesktopRawActionRedirectParse：Raw 动作行尾 `=> #"file"` 由引擎分离为目标，
// RawArgs 不含 => 段；动词原文保留（WIN list => 后参数为 list）。目标用绝对路径（R-11）。
func TestDesktopRawActionRedirectParse(t *testing.T) {
	ctx := &runCtx{vars: map[string]float64{}}
	actions := desktopActions(ctx)
	win := filepath.ToSlash(filepath.Join(t.TempDir(), "wins.txt"))
	ast, err := dsl.Parse("WIN list => #\""+win+"\"\n", actions)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(ast.Stmts) != 1 {
		t.Fatalf("语句数 = %d, want 1", len(ast.Stmts))
	}
	ac, ok := ast.Stmts[0].(*dsl.ActionStmt)
	if !ok {
		t.Fatal("应为 ActionStmt")
	}
	if ac.Verb != "WIN" || ac.RawArgs != "list" {
		t.Fatalf("verb/rawArgs = %s/%q, want WIN/list", ac.Verb, ac.RawArgs)
	}
	if ac.Target == nil || ac.Target.Rune != '#' || ac.Target.Path != win {
		t.Fatalf("target 未正确解析: %+v", ac.Target)
	}
}
