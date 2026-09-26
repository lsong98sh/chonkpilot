// 工具级异步配置（usr 键 `tool_async`）端到端白盒（64 §3 · 36-配置）：
// 保存/删除 usr 配置 → data-user-config-refresh（既有主题）→ loadExecConfig/reloadToolAsyncOverrides
// → Config.SetToolAsync → reloadAppContracts（契约重注册 + 既有 gateway/reload）→ tools/list 的
// _meta.async / _meta.async-threshold **秒级**变化（无需重启）。零新增主题。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeAsyncToolContract 在 <root>/tools 写一个带异步 meta 的 *.tool.md 契约。
func writeAsyncToolContract(t *testing.T, root, name string, metaLines []string) {
	t.Helper()
	dir := filepath.Join(root, "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := "# " + name + "\n\n[meta]\nhot=true\n" + strings.Join(metaLines, "\n") +
		"\n\n[description]\n工具异步配置用例\n\n[parameters]\n{\"type\":\"object\",\"properties\":{\"x\":{\"type\":\"string\"}}}\n"
	if err := os.WriteFile(filepath.Join(dir, name+".tool.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
}

// toolMetaOf 取 tools/list 中某工具（暴露名）的 _meta 原文。
func toolMetaOf(t *testing.T, s *Server, name string) map[string]any {
	t.Helper()
	defs, err := s.gc.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, d := range defs {
		if d.Name == name {
			return d.Meta
		}
	}
	return nil
}

// waitToolMeta 轮询 tools/list 直到 name 的 _meta 满足 want（返回耗时）。
func waitToolMeta(t *testing.T, s *Server, name string, want func(map[string]any) bool, timeout time.Duration) (time.Duration, bool) {
	t.Helper()
	start := time.Now()
	for {
		if want(toolMetaOf(t, s, name)) {
			return time.Since(start), true
		}
		if time.Since(start) > timeout {
			return time.Since(start), false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestToolAsyncOverrideUserConfigHotReload：usr `tool_async` 四档/阈值保存即生效（工具面 _meta
// 变化 + 网关判定源同步）、hard_timeout 不透出、非法值忽略、「恢复默认」回落契约现值。
func TestToolAsyncOverrideUserConfigHotReload(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	appRoot := t.TempDir()
	writeAsyncToolContract(t, appRoot, "ta_tool", []string{"async=auto", "async-threshold=30", "timeout=60"})
	s := newTestServerMCPAppRoot(t, llm, appRoot)

	const tool = "self_ta_tool" // 暴露名 = self 节点前缀 + 契约名（gateway applyPrefix）
	// ① 未配置 → 契约现值（async 不显式透出；autothreshold/timeout 按契约）
	base := toolMetaOf(t, s, tool)
	if base == nil {
		t.Fatalf("工具 %s 未进入工具面", tool)
	}
	if _, ok := base["async"]; ok {
		t.Fatalf("未配置时不应显式透出 _meta.async：%v", base)
	}
	if base["async-threshold"] != float64(30) || base["timeout"] != float64(60) {
		t.Fatalf("未配置时应保持契约值：%v", base)
	}

	// ② 保存 usr tool_async（mode=always / threshold=5 / hard_timeout=7）
	raw := `{"self_ta_tool":{"mode":"always","threshold":5,"hard_timeout":7}}`
	saveTestUserConfig(t, s, map[string]any{toolAsyncUserConfigKey: raw})

	// A：落库回读（persist 自由键通道，原样字符串）
	load := dataResult(t, dataCall(t, s, "data-user-config-load", map[string]any{"instance_id": "ins-test"}))
	if got, _ := load["data"].(map[string]any)[toolAsyncUserConfigKey].(string); got != raw {
		t.Fatalf("A 回读 tool_async = %q，期望 %q", got, raw)
	}

	// B：工具面 _meta 秒级变化（保存即生效，无需重启）
	elapsed, ok := waitToolMeta(t, s, tool, func(m map[string]any) bool {
		return m["async"] == "always" && m["async-threshold"] == float64(5)
	}, 5*time.Second)
	if !ok {
		t.Fatalf("保存后 _meta 未热更新：%v", toolMetaOf(t, s, tool))
	}
	if elapsed > 3*time.Second {
		t.Fatalf("热生效耗时过长：%v（期望秒级）", elapsed)
	}
	got := toolMetaOf(t, s, tool)
	if got["timeout"] != float64(60) {
		t.Fatalf("hard_timeout 不得改写 _meta.timeout（gateway 超时裁决点）：%v", got)
	}
	if _, ok := got["hard_timeout"]; ok {
		t.Fatalf("hard_timeout 不应透出到 _meta：%v", got)
	}
	// 网关判定源（doCall 的 async 默认族）同步：gwClient 读同一份 tools/list
	if m := s.gc.AsyncMode(tool); m != "always" {
		t.Fatalf("gateway 侧 async 判定 = %q，期望 always", m)
	}
	t.Logf("usr tool_async 保存 → tools/list _meta 生效耗时 %v（meta=%v）", elapsed, got)

	// ③ 非法值（JSON 非法）→ 忽略并保留现值，不炸
	saveTestUserConfig(t, s, map[string]any{toolAsyncUserConfigKey: "{not-json"})
	time.Sleep(300 * time.Millisecond)
	if m := toolMetaOf(t, s, tool); m["async"] != "always" {
		t.Fatalf("非法 JSON 不应改动既有覆盖：%v", m)
	}

	// ④「恢复默认」= 删该工具键项（空对象）→ 回落契约现值
	saveTestUserConfig(t, s, map[string]any{toolAsyncUserConfigKey: "{}"})
	elapsed2, ok := waitToolMeta(t, s, tool, func(m map[string]any) bool {
		_, hasAsync := m["async"]
		return !hasAsync && m["async-threshold"] == float64(30)
	}, 5*time.Second)
	if !ok {
		t.Fatalf("恢复默认后 _meta 未回落契约：%v", toolMetaOf(t, s, tool))
	}
	if s.gc.AsyncMode(tool) != "auto" {
		t.Fatalf("恢复默认后 gateway async 判定 = %q，期望 auto", s.gc.AsyncMode(tool))
	}
	t.Logf("「恢复默认」（删键项）→ 回落契约耗时 %v", elapsed2)
}
