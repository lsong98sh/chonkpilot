package appwire

import "testing"

// TestSnapshotRevisionDeterministic 校验快照哈希的确定性（同一输入恒同一 Revision，
// 排列顺序不影响结果）。
func TestSnapshotRevisionDeterministic(t *testing.T) {
	a := Snapshot(BootConfig{
		Form: "desktop", WorkDir: `E:\ws\a`, LLMBase: "http://127.0.0.1:8901/v1",
		Servers: []ServerSpec{{ID: "b"}, {ID: "a"}},
		EnvKeys: []string{"CHONK_PLUGIN_UDS", "CHONK_LLM_BRIDGE_ADDR"},
	})
	b := Snapshot(BootConfig{
		Form: "desktop", WorkDir: `E:\ws\a`, LLMBase: "http://127.0.0.1:8901/v1",
		Servers: []ServerSpec{{ID: "a"}, {ID: "b"}},
		EnvKeys: []string{"CHONK_LLM_BRIDGE_ADDR", "CHONK_PLUGIN_UDS"},
	})
	if a.Revision == "" || a.Revision != b.Revision {
		t.Fatalf("revision not deterministic: %q vs %q", a.Revision, b.Revision)
	}
	// 输入变化 → Revision 必须变化。
	c := Snapshot(BootConfig{Form: "desktop", WorkDir: `E:\ws\c`})
	if c.Revision == a.Revision {
		t.Fatal("revision must change when inputs change")
	}
}

// TestSnapshotPoolDefault 校验执行池缺省值（与 assembly 既有硬编码一致）。
func TestSnapshotPoolDefault(t *testing.T) {
	cfg := Snapshot(BootConfig{Form: "browser"})
	if cfg.Pool != DefaultPool() {
		t.Fatalf("pool default = %+v, want %+v", cfg.Pool, DefaultPool())
	}
}

// TestRedactedScrubsCredentials 校验脱敏视图不含 URL 内嵌凭据。
func TestRedactedScrubsCredentials(t *testing.T) {
	cfg := Snapshot(BootConfig{Form: "gui", LLMBase: "https://user:secret@llm.example.com/v1"})
	view := cfg.Redacted()
	if got := view["llmBase"]; got != "https://redacted@llm.example.com/v1" {
		t.Fatalf("llmBase not scrubbed: %v", got)
	}
	for _, v := range view {
		if s, ok := v.(string); ok && s == "user:secret@llm.example.com/v1" {
			t.Fatal("credential leaked in redacted view")
		}
	}
}
