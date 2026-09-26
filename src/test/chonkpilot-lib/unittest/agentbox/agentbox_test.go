// Package agentboxtest — chonkpilot-lib/agentbox 黑盒单测（L2，工程目录
// chonkpilot-test/chonkpilot-lib/unittest/agentbox）。
//
// 覆盖（对齐 42-决策记录 §2 (104)/(109) 与 14-安全域-agentbox）：
//   - 策略换算：security-* 条目（{dir, writable}）→ 允许读写集合（递归），含归一化/坏条目丢弃；
//   - 判定：允许目录内放行、目录外拒绝（读/写分别）、`..`/盘符/大小写/兄弟目录边界；
//   - 未配置（空串/未设置）→ **不启用**（放行一切，行为等价）。
package agentboxtest

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
)

// TestPolicyConversionFromSecurityEntries：security-* 条目 → 允许集（递归）换算。
// 断言：归一化为绝对 Clean 路径、空目录条目丢弃、writable 透传、保序。
func TestPolicyConversionFromSecurityEntries(t *testing.T) {
	base := t.TempDir()
	rw := filepath.Join(base, "proj")
	ro := filepath.Join(base, "ro")

	p := agentbox.New([]agentbox.Rule{
		{Dir: rw, Writable: true},
		{Dir: "   "},                          // 空目录 → 丢弃
		{Dir: filepath.Join(rw, "sub", "..")}, // 含 .. → Clean 归一回 proj（去重前，规则仍保留）
		{Dir: ro},                             // 默认只读
	})
	rules := p.Rules()
	if len(rules) != 3 {
		t.Fatalf("换算后条目数应为 3（空目录丢弃），got %d: %+v", len(rules), rules)
	}
	for _, r := range rules {
		if !filepath.IsAbs(r.Dir) || r.Dir != filepath.Clean(r.Dir) {
			t.Fatalf("目录未归一化为绝对 Clean 形态: %q", r.Dir)
		}
	}
	if !rules[0].Writable || rules[1].Writable || rules[2].Writable {
		t.Fatalf("writable 未按条目透传: %+v", rules)
	}
	if rules[1].Dir != rw {
		t.Fatalf("含 .. 的目录应归一回 %q，got %q", rw, rules[1].Dir)
	}
}

// TestAllowedRecursiveAndReadWrite：允许目录内（含子路径）放行，目录外拒绝；读写分别判定。
func TestAllowedRecursiveAndReadWrite(t *testing.T) {
	base := t.TempDir()
	rw := filepath.Join(base, "rw")
	ro := filepath.Join(base, "ro")
	p := agentbox.New([]agentbox.Rule{{Dir: rw, Writable: true}, {Dir: ro}})

	// 允许目录内（含深层子路径、目录自身）读 + 写
	for _, target := range []string{
		rw,
		filepath.Join(rw, "a.txt"),
		filepath.Join(rw, "deep", "nested", "b.txt"),
	} {
		if err := p.Check(target, false); err != nil {
			t.Fatalf("允许目录内读应放行 %q: %v", target, err)
		}
		if err := p.Check(target, true); err != nil {
			t.Fatalf("可写目录内写应放行 %q: %v", target, err)
		}
	}
	// 只读目录：读放行、写拒绝
	if err := p.Check(filepath.Join(ro, "x.txt"), false); err != nil {
		t.Fatalf("只读目录读应放行: %v", err)
	}
	err := p.Check(filepath.Join(ro, "x.txt"), true)
	if err == nil {
		t.Fatal("只读目录写应拒绝")
	}
	var denied *agentbox.DeniedError
	if !errors.As(err, &denied) || !errors.Is(err, agentbox.ErrDenied) {
		t.Fatalf("拒绝错误应为 *DeniedError 且 Is(ErrDenied): %T %v", err, err)
	}
	if denied.Write != true || denied.Path == "" || len(denied.Allowed) != 2 {
		t.Fatalf("DeniedError 诊断信息不全: %+v", denied)
	}
	if !strings.Contains(err.Error(), "agentbox") {
		t.Fatalf("拒绝消息应带 agentbox 标识（可诊断）: %v", err)
	}
	// 目录外（兄弟目录 / 父目录 / 同前缀但非子路径）
	for _, target := range []string{
		base,
		filepath.Join(base, "other", "x.txt"),
		rw + "-sibling",
		filepath.Join(rw+"-sibling", "x.txt"),
	} {
		if err := p.Check(target, false); err == nil {
			t.Fatalf("目录外读应拒绝 %q", target)
		}
		if err := p.Check(target, true); err == nil {
			t.Fatalf("目录外写应拒绝 %q", target)
		}
	}
}

// TestBoundaryDotDotDriveAndCase：`..` 逃逸、盘符/大小写边界。
// 断言 `..` 逃逸被 Clean 收敛后仍判为越界；Windows 下大小写不敏感（同一目录不同大小写视为命中）。
func TestBoundaryDotDotDriveAndCase(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allow")
	p := agentbox.New([]agentbox.Rule{{Dir: allowed, Writable: true}})

	// 允许目录内 + `..` 绕一圈回到原处 → 放行（Clean 收敛后仍在目录内）
	if err := p.Check(filepath.Join(allowed, "sub", "..", "f.txt"), true); err != nil {
		t.Fatalf("目录内 .. 收敛后应放行: %v", err)
	}
	// 越出允许目录的 `..` → 拒绝
	if err := p.Check(filepath.Join(allowed, "..", "escape.txt"), true); err == nil {
		t.Fatal("`..` 逃逸出允许目录应拒绝")
	}
	if runtime.GOOS == "windows" {
		if err := p.Check(strings.ToUpper(allowed)+`\UP.TXT`, true); err != nil {
			t.Fatalf("Windows 大小写不敏感：同目录不同大小写应放行: %v", err)
		}
		// 不同盘符（若存在非当前盘）→ 天然拒绝；此处用不存在的盘符验证不 panic 且拒绝
		if err := p.Check(`Z:\no\such\file.txt`, true); err == nil {
			t.Fatal("允许目录在另一盘时应拒绝")
		}
	}
}

// TestDisabledPolicyAllowsAll：未配置（nil 策略）→ 一律放行（行为等价，默认兼容）。
func TestDisabledPolicyAllowsAll(t *testing.T) {
	var p *agentbox.Policy // nil = 未启用
	abs := filepath.Join(t.TempDir(), "any.txt")
	if !p.Allowed(abs, true) || !p.Allowed(abs, false) {
		t.Fatal("nil 策略应放行一切")
	}
	if err := p.Check(abs, true); err != nil {
		t.Fatalf("nil 策略 Check 应为 nil: %v", err)
	}
	if p.Enabled() {
		t.Fatal("nil 策略 Enabled() 应为 false")
	}
	// Parse("") → nil（不启用）；Marshal(nil) → ""（不注入环境变量）
	if q, err := agentbox.Parse("  "); err != nil || q != nil {
		t.Fatalf("空串应解析为未启用: q=%v err=%v", q, err)
	}
	if s := p.Marshal(); s != "" {
		t.Fatalf("nil 策略 Marshal 应为空串，got %q", s)
	}
}

// TestParseMarshalAndEnv：策略 JSON 编解码 + 进程级策略（环境变量装载语义）。
func TestParseMarshalAndEnv(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "a")
	raw := `[{"dir":` + strconvQuote(allowed) + `,"writable":true}]`

	p, err := agentbox.Parse(raw)
	if err != nil || p == nil || len(p.Rules()) != 1 {
		t.Fatalf("合法 JSON 应解析为 1 条策略: p=%v err=%v", p, err)
	}
	// Marshal → Parse 往返等值
	p2, err := agentbox.Parse(p.Marshal())
	if err != nil || p2 == nil || p2.Rules()[0] != p.Rules()[0] {
		t.Fatalf("Marshal/Parse 往返不一致: %+v", p2)
	}
	if _, err := agentbox.Parse(`{"dir":"x"}`); err == nil {
		t.Fatal("非法 JSON（对象而非数组）应报错")
	}
	if _, err := agentbox.Parse(`[{"dir":`); err == nil {
		t.Fatal("截断 JSON 应报错")
	}

	// 进程级：Set 后生效，Set(nil) 后回到全放行
	agentbox.Set(p)
	if err := agentbox.Check(filepath.Join(base, "other", "x.txt"), true); err == nil {
		t.Fatal("启用后目录外应拒绝")
	}
	if err := agentbox.Check(filepath.Join(allowed, "x.txt"), true); err != nil {
		t.Fatalf("启用后目录内应放行: %v", err)
	}
	agentbox.Set(nil)
	if err := agentbox.Check(filepath.Join(base, "other", "x.txt"), true); err != nil {
		t.Fatalf("Set(nil) 后应全放行: %v", err)
	}

	// InitFromEnv：环境变量空 → 不启用；合法 JSON → 启用
	t.Setenv(agentbox.EnvSandbox, "")
	if agentbox.InitFromEnv() != nil {
		t.Fatal("环境变量为空应不启用")
	}
	t.Setenv(agentbox.EnvSandbox, raw)
	if p3 := agentbox.InitFromEnv(); p3 == nil || len(p3.Rules()) != 1 {
		t.Fatalf("环境变量合法 JSON 应启用: %v", p3)
	}
	if err := agentbox.Check(filepath.Join(base, "other", "x.txt"), false); err == nil {
		t.Fatal("由环境变量启用的策略应立即生效")
	}
	agentbox.Set(nil)
}

// TestEmptyAllowListDeniesAll：显式启用但允许集为空 → 全拒（严格语义，仅开关开启时出现）。
func TestEmptyAllowListDeniesAll(t *testing.T) {
	p := agentbox.New(nil)
	if !p.Enabled() {
		t.Fatal("New(nil) 应为已启用（空集 = 全拒）")
	}
	if err := p.Check(filepath.Join(t.TempDir(), "x.txt"), false); err == nil {
		t.Fatal("空允许集应拒绝读")
	}
	if err := p.Check(filepath.Join(t.TempDir(), "x.txt"), true); err == nil {
		t.Fatal("空允许集应拒绝写")
	}
}

// strconvQuote 用 JSON 编码字符串字面量（避免手写转义陷阱；Windows 路径含反斜杠）。
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
