package dsl

import "testing"

// TestCollectHandleRefs 只读收集脚本内全部 `#"path"` 句柄引用（含 LOOP/SET 数据源、
// IF exist、=> 目标、访问器参数），供 mcp-tools 做 R-11 预校验；不改变引擎行为。
func TestCollectHandleRefs(t *testing.T) {
	script := `
SET #"~/data/a.json".object => cfg
LOOP row=#"~/data/rows.csv".lines
   IF exist #"~/data/flag"
      SET "x" => seen
   END
END
IF exist "~/data/other"
   SET "y" => seen
END
WIN list => #"~/out/wins.txt"
`
	actions := []Action{
		{Name: "WIN", Raw: true, Run: func(*Scope, string) (string, error) { return "", nil }},
	}
	ast, err := Parse(script, actions)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	refs := CollectHandleRefs(ast)
	got := map[string]string{} // path → desc
	for _, r := range refs {
		got[r.Path] = r.Desc
	}
	want := map[string]string{
		"~/data/a.json":   "SET 值",
		"~/data/rows.csv": "LOOP 数据源",
		"~/data/flag":     "IF exist",
		"~/data/other":    "IF exist",
		"~/out/wins.txt":  "=> 目标",
	}
	for p, desc := range want {
		if got[p] != desc {
			t.Fatalf("句柄 %q 的 desc = %q, want %q（refs=%v）", p, got[p], desc, refs)
		}
	}
}

// TestCollectHandleRefsRelativeCollected 相对路径同样被收集（校验交由消费方判定）。
func TestCollectHandleRefsRelativeCollected(t *testing.T) {
	ast, err := ParseScript(`LOOP row=#"rows.csv".lines` + "\n" + `   SET "a" => b` + "\nEND\n")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	refs := CollectHandleRefs(ast)
	if len(refs) != 1 || refs[0].Path != "rows.csv" {
		t.Fatalf("应收集到相对数据源 rows.csv，got %v", refs)
	}
}

// TestCollectHandleRefsNil 空脚本安全返回 nil。
func TestCollectHandleRefsNil(t *testing.T) {
	if refs := CollectHandleRefs(nil); refs != nil {
		t.Fatalf("nil 脚本应返回 nil，got %v", refs)
	}
}
