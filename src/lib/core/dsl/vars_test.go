package dsl

import (
	"strings"
	"testing"
)

// capAction 返回插值后的参数（Raw：原文透传），用于断言 {{env.*}} 插值结果。
func capAction() Action {
	return Action{Name: "CAP", Raw: true, Run: func(sc *Scope, raw string) (string, error) {
		return sc.Interp(strings.Trim(raw, `"`))
	}}
}

// TestVarsEnvInjection Options.Vars 预置根作用域（只读 env）+ {{env.X}} 插值可用。
func TestVarsEnvInjection(t *testing.T) {
	actions := []Action{capAction()}
	eng := NewEngine(Options{
		Vars: map[string]any{"env": map[string]any{
			"CHONKPILOT_WORKDIR": `C:\work\proj`,
			"CHONKPILOT_TEMPDIR": `C:\temp\chonkpilot\ins`,
		}},
		Actions: actions,
	})
	script := "CAP \"{{env.CHONKPILOT_WORKDIR}}/src/main.py\"\nCAP \"{{env.CHONKPILOT_TEMPDIR}}/x.txt\"\n"
	ast, err := Parse(script, actions)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	res := eng.Result()
	if len(res.Errors) > 0 {
		t.Fatalf("不应有错误: %v", res.Errors)
	}
	summary := strings.Join(res.Summary, "\n")
	for _, want := range []string{`C:\work\proj/src/main.py`, `C:\temp\chonkpilot\ins/x.txt`} {
		if !strings.Contains(summary, want) {
			t.Fatalf("汇总缺插值结果 %q，got %q", want, summary)
		}
	}
}

// TestSetReservedVarRejected 宿主注入的保留变量只读：SET 赋值 → 报错；未注入变量可正常赋值。
func TestSetReservedVarRejected(t *testing.T) {
	eng := NewEngine(Options{
		Vars:    map[string]any{"env": map[string]any{"CHONKPILOT_WORKDIR": `C:\work`}},
		Actions: []Action{{Name: "NOP", Run: func(*Scope, string) (string, error) { return "", nil }}},
	})
	ast, err := Parse("SET \"x\" => env\n", nil)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	errs := eng.Result().Errors
	if len(errs) != 1 || !strings.Contains(errs[0].Msg, "env 是保留字（宿主注入的只读上下文），不能作为变量名") {
		t.Fatalf("SET env 应被拒绝，got %v", errs)
	}

	eng2 := NewEngine(Options{
		Vars:    map[string]any{"env": map[string]any{"X": "1"}},
		Actions: []Action{{Name: "NOP", Run: func(*Scope, string) (string, error) { return "", nil }}},
	})
	ast2, err := Parse("SET \"ok\" => foo\n", nil)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := eng2.Execute(ast2); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if errs := eng2.Result().Errors; len(errs) != 0 {
		t.Fatalf("普通变量赋值不应报错，got %v", errs)
	}
}

// TestReservedEnvUnconditional env 是保留字（不依赖 Options.Vars 注入）：
// SET / SET 字段链 / SET 下标 / 动作 => 目标 / TYPEOF / ENTRY / SPLIT / JOIN / PUSH 一律报错。
func TestReservedEnvUnconditional(t *testing.T) {
	actions := []Action{
		capAction(),
		{Name: "NOP", Run: func(*Scope, string) (string, error) { return "", nil }},
	}
	cases := []struct {
		name   string
		script string
	}{
		{"SET 裸名", "SET \"x\" => env\n"},
		{"SET 字段链", "SET \"x\" => env.CHONKPILOT_WORKDIR\n"},
		{"SET 下标", "SET \"x\" => env[0]\n"},
		{"动作 => 目标", "NOP => env\n"},
		{"TYPEOF 目标", "TYPEOF \"x\" => env\n"},
		{"ENTRY 目标", `ENTRY {"a":1} => env` + "\n"},
		{"SPLIT 目标", `SPLIT "a,b", "," => env` + "\n"},
		{"JOIN 目标", `JOIN ["a","b"], "," => env` + "\n"},
		{"PUSH 目标", `PUSH "x" => env` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 刻意不注入 Options.Vars：env 仍必须是保留字。
			eng := NewEngine(Options{Actions: actions})
			ast, err := Parse(tc.script, actions)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			_ = eng.Execute(ast)
			errs := eng.Result().Errors
			if len(errs) == 0 {
				t.Fatalf("%s 应报保留字错误，got 无错误", tc.name)
			}
			if !strings.Contains(errs[0].Msg, "env 是保留字（宿主注入的只读上下文），不能作为变量名") {
				t.Fatalf("%s 错误文案不符：%v", tc.name, errs)
			}
		})
	}
}

// TestReservedEnvLoopBinding LOOP env=... 不得遮蔽宿主注入上下文（报错）；
// 普通 LOOP 变量名正常。
func TestReservedEnvLoopBinding(t *testing.T) {
	actions := []Action{capAction(), {Name: "NOP", Run: func(*Scope, string) (string, error) { return "", nil }}}
	eng := NewEngine(Options{
		Vars:    map[string]any{"env": map[string]any{"CHONKPILOT_WORKDIR": `C:\work`}},
		Actions: actions,
	})
	ast, err := Parse("LOOP env=[\"a\",\"b\"]\n   NOP\nEND\n", actions)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	errs := eng.Result().Errors
	if len(errs) != 1 || !strings.Contains(errs[0].Msg, "env 是保留字（宿主注入的只读上下文），不能作为变量名") {
		t.Fatalf("LOOP env= 应被拒绝，got %v", errs)
	}

	// 对照：普通变量名正常迭代。
	eng2 := NewEngine(Options{Actions: actions, Vars: map[string]any{"env": map[string]any{"X": "1"}}})
	ast2, err := Parse("LOOP it=[\"a\",\"b\"]\n   CAP \"{{it}}\"\nEND\n", actions)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := eng2.Execute(ast2); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if errs := eng2.Result().Errors; len(errs) != 0 {
		t.Fatalf("普通 LOOP 变量不应报错，got %v", errs)
	}
	if got := strings.Join(eng2.Result().Summary, ","); got != "a,b" {
		t.Fatalf("普通 LOOP 迭代结果 = %q, want a,b", got)
	}
}

// TestInjectedVarFieldWriteRejected 宿主注入变量经**访问器链字段写**同样只读
// （与裸名 / 下标写口径一致，避免就地改写宿主注入的共享对象）。
func TestInjectedVarFieldWriteRejected(t *testing.T) {
	actions := []Action{{Name: "NOP", Run: func(*Scope, string) (string, error) { return "", nil }}}
	eng := NewEngine(Options{
		Vars: map[string]any{
			"env": map[string]any{"CHONKPILOT_WORKDIR": `C:\work`},
			"ctx": map[string]any{"y": 0},
		},
		Actions: actions,
	})
	ast, err := Parse("SET 1 => ctx.y\n", actions)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	errs := eng.Result().Errors
	if len(errs) != 1 || !strings.Contains(errs[0].Msg, "ctx 是宿主注入的保留变量，只读") {
		t.Fatalf("访问器链写注入变量应被拒绝，got %v", errs)
	}
}

// TestReservedEnvReadOK {{env.X}} 读取仍合法（唯一受支持用法）。
func TestReservedEnvReadOK(t *testing.T) {
	actions := []Action{capAction()}
	eng := NewEngine(Options{
		Vars:    map[string]any{"env": map[string]any{"CHONKPILOT_WORKDIR": `C:\work`}},
		Actions: actions,
	})
	ast, err := Parse("CAP \"{{env.CHONKPILOT_WORKDIR}}/a.txt\"\n", actions)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if err := eng.Execute(ast); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if errs := eng.Result().Errors; len(errs) != 0 {
		t.Fatalf("{{env.X}} 读取不应报错，got %v", errs)
	}
	if got := strings.Join(eng.Result().Summary, "\n"); !strings.Contains(got, `C:\work/a.txt`) {
		t.Fatalf("插值结果 = %q", got)
	}
}
