package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
)

// TestApplyInterpretersChromePath 验证 Apply 解析新增的 interpreters / chrome_path（含非空才生效语义）。
func TestApplyInterpretersChromePath(t *testing.T) {
	cfg := DefaultConfig()
	raw := []byte(`{
		"interpreters": {"python": "C:\\Python\\python.exe", "js": "C:\\node\\node.exe"},
		"chrome_path": "C:\\Chrome\\chrome.exe"
	}`)
	if err := cfg.Apply(raw); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := map[string]string{"python": `C:\Python\python.exe`, "js": `C:\node\node.exe`}
	if !reflect.DeepEqual(cfg.Interpreters, want) {
		t.Fatalf("Interpreters = %v, want %v", cfg.Interpreters, want)
	}
	if cfg.ChromePath != `C:\Chrome\chrome.exe` {
		t.Fatalf("ChromePath = %q", cfg.ChromePath)
	}
	// 空串不覆盖既有值
	if err := cfg.Apply([]byte(`{"chrome_path": ""}`)); err != nil {
		t.Fatalf("Apply(empty): %v", err)
	}
	if cfg.ChromePath != `C:\Chrome\chrome.exe` {
		t.Fatalf("ChromePath 被空串覆盖: %q", cfg.ChromePath)
	}
}

// TestDefaultsMapNoContextArgs 验证 defaultsMap 不再注入 _interpreters/_workdir/_datadir/_instance
// （R-11 二次升级：上下文经调用上下文 _meta 传递，解释器经子进程环境注入）。
func TestDefaultsMapNoContextArgs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SetRuntime(0, 0, map[string]string{"python": "C:\\Python\\python.exe"}, `C:\Chrome\chrome.exe`, nil)
	m := cfg.defaultsMap()
	for _, k := range []string{"_interpreters", "_workdir", "_datadir", "_instance"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%s 不应注入 args（改用 _meta / 子进程 env）: %#v", k, m)
		}
	}
}

// TestCallContextFromMeta 验证调用上下文经 _meta 解析：无上下文 → 不报错；
// 上下文存在但 instance 空 → 异常；正常 → 字段齐全。
func TestCallContextFromMeta(t *testing.T) {
	t.Run("无命名空间不报错", func(t *testing.T) {
		for _, meta := range []mcp.Meta{nil, {"other": 1}} {
			cx := callContextFromMeta(meta)
			if cx.Present || cx.err() != nil {
				t.Fatalf("无 chonkpilot 命名空间应 Present=false 且无错，got %+v err=%v", cx, cx.err())
			}
		}
	})
	t.Run("上下文存在但 instance 空 → 异常", func(t *testing.T) {
		cx := callContextFromMeta(mcp.Meta{"chonkpilot": map[string]any{"instance_id": ""}})
		if !cx.Present || cx.err() == nil {
			t.Fatalf("instance 空应报错，got %+v err=%v", cx, cx.err())
		}
		if !strings.Contains(cx.err().Error(), "缺少 instance") {
			t.Fatalf("错误消息不符: %v", cx.err())
		}
	})
	t.Run("上下文齐全", func(t *testing.T) {
		cx := callContextFromMeta(CallContextMeta("ins-1", `C:\w`, `C:\d`))
		if !cx.Present || cx.err() != nil {
			t.Fatalf("正常上下文不应报错，got %+v err=%v", cx, cx.err())
		}
		if cx.InstanceID != "ins-1" || cx.WorkDir != `C:\w` || cx.DataDir != `C:\d` {
			t.Fatalf("上下文解析不符: %+v", cx)
		}
	})
}

// TestExecutorEnv 验证 executor 子进程环境注入 CHONKPILOT_*（含剥离残留）+ CHONK_CHROME
// + agentbox 策略（仅按工具开关注入）。
func TestExecutorEnv(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SetRuntime(0, 0, map[string]string{"python": "C:\\Python\\python.exe"}, `C:\Chrome\chrome.exe`, nil)
	t.Setenv("CHONKPILOT_INSTANCE", "stale-should-be-stripped")
	t.Setenv("CHONKPILOT_SANDBOX", `[{"dir":"C:\\stale","writable":true}]`) // 残留策略也须被剥离
	env := cfg.executorEnv(CallContext{InstanceID: "ins-1", WorkDir: `C:\w`, DataDir: `C:\d`}, "")
	got := envMap(env)
	if got["CHONKPILOT_INSTANCE"] != "ins-1" || got["CHONKPILOT_WORKDIR"] != `C:\w` || got["CHONKPILOT_DATADIR"] != `C:\d` {
		t.Fatalf("CHONKPILOT_* 注入不符: %v", got)
	}
	if got["CHONKPILOT_INSTANCE"] == "stale-should-be-stripped" {
		t.Fatal("残留 CHONKPILOT_INSTANCE 应被剥离")
	}
	if !strings.Contains(got["CHONKPILOT_INTERPRETERS"], "python") {
		t.Fatalf("应注入 CHONKPILOT_INTERPRETERS，got %q", got["CHONKPILOT_INTERPRETERS"])
	}
	if got["CHONK_CHROME"] != `C:\Chrome\chrome.exe` {
		t.Fatalf("应注入 CHONK_CHROME，got %q", got["CHONK_CHROME"])
	}
	if _, ok := got[agentbox.EnvSandbox]; ok {
		t.Fatalf("未开启工具沙箱开关时不应注入 %s：%q", agentbox.EnvSandbox, got[agentbox.EnvSandbox])
	}
	// 传入策略（= 某工具已开启隔离）→ 注入
	env2 := cfg.executorEnv(CallContext{InstanceID: "ins-1"}, `[{"dir":"C:\\p","writable":true}]`)
	if got2 := envMap(env2)[agentbox.EnvSandbox]; got2 == "" {
		t.Fatal("传入策略时应注入 CHONKPILOT_SANDBOX")
	}
}

// envMap 把 KEY=VALUE 列表转为 map（同名后者覆盖）。
func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// TestSetRuntimeOverrides 验证 SetRuntime 仅 >0/非空生效，并同步调整限流器上限。
func TestSetRuntimeOverrides(t *testing.T) {
	cfg := DefaultConfig()
	lim := cfg.ensureLimiter()
	cfg.SetRuntime(120, 4, nil, "/usr/bin/chrome", []string{"node_modules", ".git"})
	if cfg.TimeoutSec != 120 {
		t.Fatalf("TimeoutSec = %d, want 120", cfg.TimeoutSec)
	}
	if cfg.MaxConcurrency != 4 {
		t.Fatalf("MaxConcurrency = %d, want 4", cfg.MaxConcurrency)
	}
	if cfg.ChromePath != "/usr/bin/chrome" {
		t.Fatalf("ChromePath = %q", cfg.ChromePath)
	}
	if !reflect.DeepEqual(cfg.Defaults.SkipDirs, []string{"node_modules", ".git"}) {
		t.Fatalf("SkipDirs = %v", cfg.Defaults.SkipDirs)
	}
	lim.mu.Lock()
	limit := lim.limit
	lim.mu.Unlock()
	if limit != 4 {
		t.Fatalf("limiter.limit = %d, want 4", limit)
	}
	// 零值/空值不覆盖
	cfg.SetRuntime(0, 0, nil, "", nil)
	if cfg.TimeoutSec != 120 || cfg.MaxConcurrency != 4 || cfg.ChromePath != "/usr/bin/chrome" {
		t.Fatalf("零值发生了覆盖: %+v", cfg)
	}
}
