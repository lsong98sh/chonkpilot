package main

import (
	"flag"
	"io"
	"path/filepath"
	"testing"
)

// parseForTest 用独立的 FlagSet（ContinueOnError，静默）解析，避免污染全局 flag.CommandLine。
func parseForTest(t *testing.T, args []string) (*cliOpts, error) {
	t.Helper()
	fs := flag.NewFlagSet("cli-test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return parseFlags(fs, args)
}

// TestParseFlagsDataDirThreeState 覆盖 `--data-dir` 三态判定的关键位：dataDirSet 区分
// 「未传」与「显式留空（--data-dir=）」——前者临时隔离（默认）、后者真实根
// （语义反转见 D-45 (239)，判定实现在 chonkpilot-cli 包 PrepareDataDir）。
func TestParseFlagsDataDirThreeState(t *testing.T) {
	// ① 未传：dataDirSet=false，dataDir=""（→ 临时隔离语义，默认）
	o, err := parseForTest(t, []string{"--prompt=x"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if o.dataDirSet || o.dataDir != "" {
		t.Fatalf("未传 --data-dir 应 (false, \"\")，得 (%v, %q)", o.dataDirSet, o.dataDir)
	}

	// ② 显式留空：dataDirSet=true，dataDir=""（→ 真实根）
	o, err = parseForTest(t, []string{"--data-dir="})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !o.dataDirSet || o.dataDir != "" {
		t.Fatalf("`--data-dir=` 应 (true, \"\")，得 (%v, %q)", o.dataDirSet, o.dataDir)
	}

	// ③ 给路径：dataDirSet=true，dataDir=该路径（→ 该路径作数据根）
	o, err = parseForTest(t, []string{"--data-dir=C:/ck-root"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !o.dataDirSet || o.dataDir != "C:/ck-root" {
		t.Fatalf("`--data-dir=C:/ck-root` 应 (true, \"C:/ck-root\")，得 (%v, %q)", o.dataDirSet, o.dataDir)
	}
}

// TestParseFlagsOutputValidation 输出模式仅 sse/final/verbose 合法；非法即返回错误（main 据以退出 1）。
func TestParseFlagsOutputValidation(t *testing.T) {
	for _, mode := range []string{"sse", "final", "verbose", ""} {
		args := []string{}
		if mode != "" {
			args = append(args, "--output="+mode)
		}
		o, err := parseForTest(t, args)
		if err != nil {
			t.Fatalf("output=%q 应合法：%v", mode, err)
		}
		want := mode
		if want == "" {
			want = "sse" // 缺省
		}
		if o.output != want {
			t.Fatalf("output 缺省/赋值异常：得 %q，期望 %q", o.output, want)
		}
	}
	if o, err := parseForTest(t, []string{"--output=bogus"}); err == nil {
		t.Fatalf("非法输出模式应报错，得 %+v", o)
	}
}

// TestParseFlagsValues 其余参数按名绑定（原样透传）。
func TestParseFlagsValues(t *testing.T) {
	o, err := parseForTest(t, []string{
		"--prompt=帮我", "--prompt-file=p.txt", "--scenario=dev", "--work-dir=/w",
		"--llm=deepseek", "--think=high", "--effort=low",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if o.prompt != "帮我" || o.promptFile != "p.txt" || o.scenario != "dev" ||
		o.workDir != "/w" || o.llmModel != "deepseek" || o.think != "high" || o.effort != "low" {
		t.Fatalf("参数绑定异常：%+v", o)
	}
}

// TestParseFlagsUnknownFlag flag 语法错误经 ContinueOnError 上报（生产 ExitOnError 会退出进程）。
func TestParseFlagsUnknownFlag(t *testing.T) {
	if _, err := parseForTest(t, []string{"--nope=1"}); err == nil {
		t.Fatal("未知参数应报错")
	}
}

// TestResolveDir 相对路径 → 绝对；绝对路径保持不变。
func TestResolveDir(t *testing.T) {
	got := resolveDir(".")
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if got != abs {
		t.Fatalf("resolveDir(\".\") = %q，期望 %q", got, abs)
	}
	dir := t.TempDir()
	if resolveDir(dir) != dir {
		t.Fatalf("resolveDir(绝对) 应保持不变：%q", resolveDir(dir))
	}
}

// TestNewUUID 生成非空、互不相同、无 `.uuid` 后缀的临时 id。
func TestNewUUID(t *testing.T) {
	a, b := newUUID(), newUUID()
	if a == "" || b == "" {
		t.Fatalf("newUUID 不应为空：%q %q", a, b)
	}
	if a == b {
		t.Fatalf("两次 newUUID 不应相同：%q", a)
	}
	if filepath.Ext(a) == ".uuid" {
		t.Fatalf("newUUID 不应保留 .uuid 后缀：%q", a)
	}
}
