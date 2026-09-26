package memory

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-plugin"
)

// llmSystemByCategory 在 llm-simple 请求里按 prompt 的「【类别】X」行取该类的 system 提示词。
// 返回收集器（category → system）；handler 结束后由调用方读取。
type systemCollector struct {
	mu  sync.Mutex
	got map[string]string
}

func (s *systemCollector) add(prompt, system string) {
	const mark = "【类别】"
	i := strings.Index(prompt, mark)
	if i < 0 {
		return
	}
	rest := prompt[i+len(mark):]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got[strings.TrimSpace(rest)] = system
}

func (s *systemCollector) system(category string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got[category]
}

// stubDistillReplies 让沉淀回路跑通所需的最小应答集（prj 配置 / 类别清单 / 旧全文 / 保存），
// 并返回 llm-simple 的 system 收集器。
func stubDistillReplies(t *testing.T, bus mq.Bus, prjList map[string]any, cats []any) *systemCollector {
	t.Helper()
	reply(t, bus, prjConfigListSubject, func(map[string]any) map[string]any {
		return map[string]any{"list": prjList}
	})
	reply(t, bus, sessionLoadSubject, func(map[string]any) map[string]any {
		return map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": strings.Repeat("甲", 200)},
		}}
	})
	reply(t, bus, memoryListSubject, func(map[string]any) map[string]any {
		return map[string]any{"list": cats}
	})
	reply(t, bus, memoryReadSubject, func(map[string]any) map[string]any {
		return map[string]any{"data": map[string]any{"content": "旧全文"}}
	})
	reply(t, bus, memorySaveSubject, func(map[string]any) map[string]any { return map[string]any{"ok": true} })

	sc := &systemCollector{got: map[string]string{}}
	_, _ = bus.On(llmSimpleSubject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req struct {
			Prompt string `json:"prompt"`
			System string `json:"system"`
		}
		_ = json.Unmarshal(v.Payload, &req)
		sc.add(req.Prompt, req.System)
		v.Result = map[string]any{"text": "重写后的全文"}
		return nil
	})
	return sc
}

// TestDistillUsesCustomCategoryPrompt：项目级类别读 prj `memory.prompt.<类别名>` 作为沉淀
// 提示词（llm-simple 的 `system`）；未配置类别的类别回落内置默认。
func TestDistillUsesCustomCategoryPrompt(t *testing.T) {
	bus := newTestBus(t) // 默认空 usr 配置
	const custom = "自定义提示词-项目概要"
	sc := stubDistillReplies(t, bus,
		map[string]any{
			memoryEnabledKey:              "true",
			memoryMinTokensKey:            "1",
			memoryPromptPrefix + "项目概要": custom,
			memoryPromptPrefix + "开发规范": "", // 显式空串 → 与未配置同口径回落默认
		},
		[]any{
			map[string]any{"category": "项目概要", "level": "project"},
			map[string]any{"category": "开发规范", "level": "project"},
		})

	p := New(Options{MinTurnTokens: 10})
	p.deps = plugin.Deps{Bus: bus}
	p.extract(turnEvent{InstanceID: "ins-1", WorkDir: `C:\ws`, Session: "s1", LastTurn: "t1"})

	if got := sc.system("项目概要"); got != custom {
		t.Fatalf("项目概要未用自定义提示词：got=%q want=%q", got, custom)
	}
	if got := sc.system("开发规范"); got != defaultRewriteSystemPrompt {
		t.Fatalf("未配置类别应回落内置默认：got=%q", got)
	}
}

// TestDistillUsesUserPrefPrompt：用户级类别（用户偏好）读 usr 自由键 `memory_prompts` 中该
// 类别的值；未配置 → 回落内置默认。键读取走一次 data-user-config-load（与 llm.memory 同批）。
func TestDistillUsesUserPrefPrompt(t *testing.T) {
	bus := newTestBusRaw(t)
	const custom = "用户级自定义提示词"
	stubUserConfig(t, bus, map[string]any{
		userMemoryPromptsKey: `{"用户偏好":"` + custom + `"}`,
	})
	sc := stubDistillReplies(t, bus,
		map[string]any{memoryEnabledKey: "true", memoryMinTokensKey: "1"},
		[]any{
			map[string]any{"category": "用户偏好", "level": "user"},
			map[string]any{"category": "项目概要", "level": "project"},
		})

	p := New(Options{MinTurnTokens: 10})
	p.deps = plugin.Deps{Bus: bus}
	p.extract(turnEvent{InstanceID: "ins-1", WorkDir: `C:\ws`, Session: "s1", LastTurn: "t1"})

	if got := sc.system("用户偏好"); got != custom {
		t.Fatalf("用户偏好未用 usr 自定义提示词：got=%q want=%q", got, custom)
	}
	if got := sc.system("项目概要"); got != defaultRewriteSystemPrompt {
		t.Fatalf("项目级未配置类别应回落内置默认：got=%q", got)
	}
}

// TestDistillUserPrefPromptInvalidJSONFallsBack：`memory_prompts` 非法 JSON / 空串 → 回落内置默认
// （不视为失败，沉淀照常进行）。
func TestDistillUserPrefPromptInvalidJSONFallsBack(t *testing.T) {
	for _, raw := range []string{"not-json", "", "[]"} {
		bus := newTestBusRaw(t)
		stubUserConfig(t, bus, map[string]any{userMemoryPromptsKey: raw})
		sc := stubDistillReplies(t, bus,
			map[string]any{memoryEnabledKey: "true", memoryMinTokensKey: "1"},
			[]any{map[string]any{"category": "用户偏好", "level": "user"}})

		p := New(Options{MinTurnTokens: 10})
		p.deps = plugin.Deps{Bus: bus}
		p.extract(turnEvent{InstanceID: "ins-1", WorkDir: `C:\ws`, Session: "s1", LastTurn: "t1"})

		if got := sc.system("用户偏好"); got != defaultRewriteSystemPrompt {
			t.Fatalf("memory_prompts=%q 应回落内置默认：got=%q", raw, got)
		}
	}
}

// TestMemoryPromptForFallback：提示词取值三态（自定义 / 空白 / 未配置）与用户级判定，纯函数口径。
func TestMemoryPromptForFallback(t *testing.T) {
	cfg := memoryConfig{Prompts: map[string]string{
		"项目概要": "P1",
		"共同库":  "   ", // 全空白 → 回落默认
	}}
	userPrompts := map[string]string{"用户偏好": "U1", "用户决策": " "}

	cases := []struct {
		name string
		cat  categoryInfo
		want string
	}{
		{"项目级自定义", categoryInfo{Category: "项目概要", Level: "project"}, "P1"},
		{"项目级空白值", categoryInfo{Category: "共同库", Level: "project"}, defaultRewriteSystemPrompt},
		{"项目级未配置", categoryInfo{Category: "开发规范", Level: "project"}, defaultRewriteSystemPrompt},
		{"用户级自定义", categoryInfo{Category: "用户偏好", Level: "user"}, "U1"},
		{"用户级空白值", categoryInfo{Category: "用户决策", Level: "user"}, defaultRewriteSystemPrompt},
		{"用户级未配置", categoryInfo{Category: "接口库", Level: "user"}, defaultRewriteSystemPrompt},
	}
	for _, tc := range cases {
		if got := memoryPromptFor(cfg, userPrompts, tc.cat); got != tc.want {
			t.Fatalf("%s：got=%q want=%q", tc.name, got, tc.want)
		}
	}
	// nil 映射（读失败 / 无配置）→ 一律回落内置默认，不 panic。
	if got := memoryPromptFor(memoryConfig{}, nil, categoryInfo{Category: "项目概要"}); got != defaultRewriteSystemPrompt {
		t.Fatalf("nil 配置应回落内置默认：got=%q", got)
	}
}
