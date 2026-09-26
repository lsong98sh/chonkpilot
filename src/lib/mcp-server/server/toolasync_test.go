// 工具级异步配置（usr 键 `tool_async`）白盒：覆盖生效 / 优先级 / 未配置不回归 /
// 孤儿键不炸 / 非法值忽略 / hard_timeout 真杀（64-配置项一览 §3 · 36-配置）。
// 覆盖只改写**契约默认**（暴露 _meta），不进入调用级 args（gateway doCall 仍最高优先级）。
package server

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// toolDocForTest 造一个契约文档（不落盘；buildTool/resolveExecTimeout 纯读取该结构）。
func toolDocForTest(name, async string, asyncTh, timeout int) *ToolDoc {
	return &ToolDoc{
		Name:    name,
		Async:   async,
		AsyncTh: asyncTh,
		Timeout: timeout,
		Schema:  map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}},
	}
}

// TestToolAsyncOverrideMeta：mode/threshold → 暴露 _meta.async / async-threshold 覆盖。
func TestToolAsyncOverrideMeta(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SetToolAsync(map[string]ToolAsyncOverride{
		"self_core_file_read": {Mode: "always", Threshold: 5},
	})

	tool, err := buildTool(toolDocForTest("core_file_read", "auto", 30, 60), cfg)
	if err != nil {
		t.Fatalf("buildTool: %v", err)
	}
	if got := tool.Meta["async"]; got != "always" {
		t.Fatalf("_meta.async = %v，期望 always（键 = 暴露名 self_core_file_read）", got)
	}
	if got := tool.Meta["async-threshold"]; got != 5 {
		t.Fatalf("_meta.async-threshold = %v，期望 5（覆盖契约 30）", got)
	}
	if got := tool.Meta["timeout"]; got != 60 {
		t.Fatalf("_meta.timeout = %v，期望 60（契约值，不被覆盖改动）", got)
	}
}

// TestToolAsyncOverrideModeMatrix：四档语义 + 「auto 不显式透出」口径。
func TestToolAsyncOverrideModeMatrix(t *testing.T) {
	cases := []struct {
		name     string
		async    string
		override string
		wantKey  bool // _meta 是否出现 async 键
		want     any  // 期望值
	}{
		// 契约 auto + 覆盖 auto → 维持现状（不显式透出）
		{"auto 覆盖 auto：不显式透出", "auto", "auto", false, nil},
		// 契约未声明 async + 覆盖 auto → 同上
		{"未声明覆盖 auto：不显式透出", "", "auto", false, nil},
		// 契约 manual（script_run）+ 覆盖 auto → 须显式写 auto 才能压回自动档
		{"manual 覆盖 auto：显式透出", "manual", "auto", true, "auto"},
		// 四档直接覆盖
		{"auto 覆盖 always", "auto", "always", true, "always"},
		{"auto 覆盖 never", "auto", "never", true, "never"},
		{"auto 覆盖 manual", "auto", "manual", true, "manual"},
		// 仅异步：契约 never 覆盖 always
		{"never 覆盖 always", "never", "always", true, "always"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.SetToolAsync(map[string]ToolAsyncOverride{"core_file_read": {Mode: tc.override}})
			tool, err := buildTool(toolDocForTest("core_file_read", tc.async, 30, 60), cfg)
			if err != nil {
				t.Fatalf("buildTool: %v", err)
			}
			got, ok := tool.Meta["async"]
			if ok != tc.wantKey {
				t.Fatalf("_meta.async 存在性 = %v（值 %v），期望 %v", ok, got, tc.wantKey)
			}
			if tc.wantKey && got != tc.want {
				t.Fatalf("_meta.async = %v，期望 %v", got, tc.want)
			}
			// 契约 threshold 未被 threshold 覆盖时保持原值
			if tool.Meta["async-threshold"] != 30 {
				t.Fatalf("_meta.async-threshold = %v，期望保持契约 30", tool.Meta["async-threshold"])
			}
		})
	}
}

// TestToolAsyncUnconfiguredKeepsContract：未配置（空表 / 孤儿键 / 第三方键名 / 其它工具键）
// 一律保持契约现值——既有语义不被本功能改动。
func TestToolAsyncUnconfiguredKeepsContract(t *testing.T) {
	for _, keys := range []map[string]ToolAsyncOverride{
		nil,
		{"self_no_such_tool": {Mode: "always", Threshold: 1}},   // 孤儿键（配置引用的工具不存在）
		{"slow3p_fast_echo": {Mode: "always"}},                  // 第三方暴露名 → 内嵌 mcp-server 无此工具
		{"self_core_file_find": {Mode: "always", Threshold: 1}}, // 其它工具的键
	} {
		cfg := DefaultConfig()
		if keys != nil {
			cfg.SetToolAsync(keys)
		}
		tool, err := buildTool(toolDocForTest("core_file_read", "auto", 30, 60), cfg)
		if err != nil {
			t.Fatalf("buildTool: %v", err)
		}
		if _, ok := tool.Meta["async"]; ok {
			t.Fatalf("未命中覆盖却写入了 _meta.async=%v（keys=%v）", tool.Meta["async"], keys)
		}
		if tool.Meta["async-threshold"] != 30 {
			t.Fatalf("未命中覆盖却改写了 async-threshold=%v（keys=%v）", tool.Meta["async-threshold"], keys)
		}
	}
}

// TestToolAsyncInvalidValuesIgnored：非法 mode / 负数 → 丢该字段（不整体拒绝、不炸）。
func TestToolAsyncInvalidValuesIgnored(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.SetToolAsync(map[string]ToolAsyncOverride{
		"self_core_file_read": {Mode: "sometimes", Threshold: -1, HardTimeout: -5},
	}) {
		t.Fatal("全字段非法应归一为无覆盖（表未变化）")
	}
	if len(cfg.ToolAsync) != 0 {
		t.Fatalf("非法值应被丢弃，ToolAsync = %v", cfg.ToolAsync)
	}

	// 非法 mode 与合法 threshold 混用 → 只丢 mode
	cfg2 := DefaultConfig()
	cfg2.SetToolAsync(map[string]ToolAsyncOverride{
		"self_core_file_read": {Mode: "  Always ", Threshold: 7, HardTimeout: 0},
	})
	if ov := cfg2.ToolAsync["self_core_file_read"]; ov.Mode != "always" || ov.Threshold != 7 {
		t.Fatalf("mode 应去空白并小写、threshold 保留：%+v", ov)
	}
	cfg3 := DefaultConfig()
	cfg3.SetToolAsync(map[string]ToolAsyncOverride{"self_core_file_read": {Mode: "bogus", Threshold: 7}})
	if ov := cfg3.ToolAsync["self_core_file_read"]; ov.Mode != "" || ov.Threshold != 7 {
		t.Fatalf("非法 mode 应丢字段且不影响 threshold：%+v", ov)
	}
	tool, err := buildTool(toolDocForTest("core_file_read", "auto", 30, 60), cfg3)
	if err != nil {
		t.Fatalf("buildTool: %v", err)
	}
	if _, ok := tool.Meta["async"]; ok {
		t.Fatalf("非法 mode 不应写入 _meta.async（got %v）", tool.Meta["async"])
	}

	// 空表 = 清空全部覆盖（前端「恢复默认」= 删该工具键项）
	cfg4 := DefaultConfig()
	cfg4.SetToolAsync(map[string]ToolAsyncOverride{"self_core_file_read": {Mode: "always"}})
	if !cfg4.SetToolAsync(nil) {
		t.Fatal("清空覆盖应报告变化")
	}
	if len(cfg4.ToolAsync) != 0 {
		t.Fatalf("清空后 ToolAsync 应为空：%v", cfg4.ToolAsync)
	}
}

// TestResolveExecTimeoutPriority：执行硬上限优先级 = hard_timeout（用户配置）> 契约 timeout
// > cfg.execTimeout()（prj timeout_sec / 内置默认）；未配置 hard_timeout 时既有语义不变。
func TestResolveExecTimeoutPriority(t *testing.T) {
	cfg := DefaultConfig() // 内置默认 300s
	if got := resolveExecTimeout(cfg, toolDocForTest("t", "auto", 0, 0)); got != 300 {
		t.Fatalf("未配置 → 内置默认，got %d want 300", got)
	}
	if got := resolveExecTimeout(cfg, toolDocForTest("t", "auto", 0, 60)); got != 60 {
		t.Fatalf("契约 timeout 优先于 cfg.execTimeout，got %d want 60", got)
	}
	cfg.SetRuntime(120, 0, nil, "", nil) // prj timeout_sec=120
	if got := resolveExecTimeout(cfg, toolDocForTest("t", "auto", 0, 0)); got != 120 {
		t.Fatalf("prj timeout_sec 对未声明 timeout 的契约生效，got %d want 120", got)
	}
	if got := resolveExecTimeout(cfg, toolDocForTest("t", "auto", 0, 60)); got != 60 {
		t.Fatalf("prj timeout_sec 不改「契约优先」语义，got %d want 60（I-79 口径保持）", got)
	}
	cfg.SetToolAsync(map[string]ToolAsyncOverride{
		"self_t":      {HardTimeout: 5}, // 用户显式硬上限 → 最高
		"self_t_deep": {HardTimeout: 9}, // 孤儿键不影响 t
	})
	if got := resolveExecTimeout(cfg, toolDocForTest("t", "auto", 0, 60)); got != 5 {
		t.Fatalf("hard_timeout 应压过契约 timeout，got %d want 5", got)
	}
	if got := resolveExecTimeout(cfg, toolDocForTest("t2", "auto", 0, 0)); got != 120 {
		t.Fatalf("孤儿键不得生效，got %d want 120", got)
	}
}

// TestHelperSlowProcess 是 hard_timeout 用例的**子进程夹具**（测试二进制自 re-exec）：
// 仅当环境变量 CK_SLEEP_MS > 0 时阻塞指定毫秒（单进程、无子进程 → 被杀即释放管道）。
func TestHelperSlowProcess(t *testing.T) {
	ms, _ := strconv.Atoi(os.Getenv("CK_SLEEP_MS"))
	if ms <= 0 {
		t.Skip("子进程夹具（仅由 TestToolAsyncHardTimeoutKills 经 CK_SLEEP_MS 触发）")
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// TestToolAsyncHardTimeoutKills：hard_timeout 真实生效——把长跑脚本在 1s 处杀掉
// （断言错误文案的秒数 + 实际耗时远小于脚本自身时长）；未配置 hard_timeout 的对照**不被杀**。
func TestToolAsyncHardTimeoutKills(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	slow := &ToolDoc{Name: "core_file_read", Runtime: exe, Dir: dir,
		Args: "-test.run=TestHelperSlowProcess", Output: "stdout"}

	// ① 对照组：无覆盖（契约 timeout 未声明 → cfg.execTimeout 内置默认 300s）→ 1.5s 自然完成
	t.Setenv("CK_SLEEP_MS", "1500")
	cfg0 := DefaultConfig()
	if _, err := callTool(context.Background(), cfg0, slow, map[string]any{}, nil, CallContext{InstanceID: "ins-1"}); err != nil {
		t.Fatalf("对照（无 hard_timeout）不应被杀: %v", err)
	}

	// ② hard_timeout=1s → 子进程在 1s 被杀，调用在 5s 脚本结束前返回
	t.Setenv("CK_SLEEP_MS", "5000")
	cfg := DefaultConfig()
	cfg.SetToolAsync(map[string]ToolAsyncOverride{"self_core_file_read": {HardTimeout: 1}})
	start := time.Now()
	_, err = callTool(context.Background(), cfg, slow, map[string]any{}, nil, CallContext{InstanceID: "ins-1"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("hard_timeout=1 未生效：调用成功返回")
	}
	if !strings.Contains(err.Error(), "timeout after 1s") {
		t.Fatalf("超时文案未采用 hard_timeout=1s：%v", err)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("硬杀未按 hard_timeout 生效（耗时 %v，脚本自身 5s）", elapsed)
	}
	t.Logf("hard_timeout=1s 硬杀生效：调用耗时 %v，err=%v", elapsed, err)
}

// TestToolAsyncSetReturnsChangedOnDifference：覆盖表比较口径（无变化 → 不触发重注册）。
func TestToolAsyncSetReturnsChangedOnDifference(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.SetToolAsync(map[string]ToolAsyncOverride{"a": {Mode: "always"}}) {
		t.Fatal("首次写入应报告变化")
	}
	if cfg.SetToolAsync(map[string]ToolAsyncOverride{"a": {Mode: "always"}}) {
		t.Fatal("同值重复写入不应报告变化")
	}
	if !cfg.SetToolAsync(map[string]ToolAsyncOverride{"a": {Mode: "never", HardTimeout: 3}}) {
		t.Fatal("取值变化应报告变化")
	}
	if !cfg.SetToolAsync(map[string]ToolAsyncOverride{"b": {Threshold: 3}}) {
		t.Fatal("键集合变化应报告变化")
	}
}
