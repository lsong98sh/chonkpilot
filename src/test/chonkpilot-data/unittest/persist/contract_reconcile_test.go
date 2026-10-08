// contract_reconcile_test.go — B1 契约面 · 数据面对账（2026-10-05）。
//
// 目标（延续 B0 机制，见 src/test/chonkpilot-gui/unittest/bridge/contract_reconcile_test.go）：
// 以 `docs/spec/60-reference/61-messages.schema.json`（61 §3 数据面的机器可读抽取）为**唯一基准**，
// 用**最小合法输入**经内存总线驱动 persist 服务（`data-<domain>-*`），断言**实际发出的 result /
// event 键 ⊆ 契约声明**（多出的键 = 红）。
//
// 覆盖：
//   - §3.1 配置域（user-config / prj-config / prompt / prj-security / scenario / mcp / memory / filelist）
//   - §3.2 会话域（list/get/history/content/latest/title/delete/active-set/active-get）
//   - §3.2a 会话运行时原语（ensure-session/ensure-turn/append-message/set-summary/complete-turn/
//     cleanup-stale/load-messages/context）
//   - §3.2b 会话快照域（snapshot get/set）
//   - §3.3 知识库域（root/list/read/save/create/delete/rename/mkdir/rmdir/rename-dir）
//   - §3.4 任务树域（list/tasks/delete/upsert）
//   - §3.6 索引排除判定域（index-ignored）
//   - 下行广播：data-<domain>-refresh 家族 · config-refresh · data-user-config-changed ·
//     data-session-title-changed · task-deleted · session-new
//
// 局限（覆盖边界，见 docs/spec/50-testing/50-测试体系.md §8.5）：
//   - 只驱动**可在内存总线 + 临时工作目录跑通**的处理；不可驱动者列于 TestDataNotDrivenRegistry；
//   - 只对账**顶层键**（契约字段模型为逐主题平铺键）；嵌套行字段（如会话行 title/updated_at）不在本批；
//   - 断言强度 = 存在级（键子集）；语义级 / 回环级逐批补强。
package persist_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// ── 契约结构（与 61-messages.schema.json 对齐）─────────────────────────────

type contractField struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type contractTopic struct {
	Topic    string                   `json:"topic"`
	Face     string                   `json:"face"`
	Direction string                  `json:"direction"`
	Payload  map[string]contractField `json:"payload"`
	Result   map[string]contractField `json:"result"`
	Event    map[string]contractField `json:"event"`
}

type contractDoc struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Topics  []contractTopic `json:"topics"`
}

func (c *contractDoc) byTopic() map[string]contractTopic {
	m := make(map[string]contractTopic, len(c.Topics))
	for _, ts := range c.Topics {
		m[ts.Topic] = ts
	}
	return m
}

// contractFindFile 从本测试源文件目录向上查找契约文件（支持任意运行 cwd）。
func contractFindFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	for d := filepath.Dir(file); ; {
		cand := filepath.Join(d, "docs", "spec", "60-reference", "61-messages.schema.json")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("未找到 61-messages.schema.json（自 %s 向上）", filepath.Dir(file))
		}
		d = parent
	}
}

func contractLoad(t *testing.T) *contractDoc {
	t.Helper()
	raw, err := os.ReadFile(contractFindFile(t))
	if err != nil {
		t.Fatalf("读取契约失败: %v", err)
	}
	var c contractDoc
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("契约 JSON 解析失败: %v", err)
	}
	return &c
}

// assertContractKeys 断言 actual 的键集 ⊆ 声明（通用 error 恒放行）。
// 多出的键 = 红（无豁免机制：契约=61，须改契约或改代码，见 50 §8.4）。
// which 仅用于失败信息（"result"/"event"）。
func assertContractKeys(t *testing.T, topic, which string, declared map[string]contractField, actual map[string]any) {
	t.Helper()
	var extra []string
	for k := range actual {
		if k == "error" {
			continue
		}
		if _, ok := declared[k]; ok {
			continue
		}
		extra = append(extra, k)
	}
	if len(extra) > 0 {
		t.Errorf("%s.%s 出现契约未声明的键（多键 = 红）: %v（契约声明=%v）",
			topic, which, extra, contractKeys(declared))
	}
}

func contractKeys(m map[string]contractField) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// assertDataResult 驱动一条 data-* 请求并按契约 result 侧对账顶层键。
func assertDataResult(t *testing.T, c *contractDoc, bus mq.Bus, topic string, payload map[string]any) map[string]any {
	t.Helper()
	byTopic := c.byTopic()
	ts, ok := byTopic[topic]
	if !ok {
		t.Fatalf("契约未收录主题 %s", topic)
	}
	res := dataResult(t, dataCall(t, bus, topic, payload))
	assertContractKeys(t, topic, "result", ts.Result, res)
	return res
}

// assertDataEvent 按契约 event 侧对账一条下行广播/事件的顶层键。
func assertDataEvent(t *testing.T, c *contractDoc, topic string, payload map[string]any) {
	t.Helper()
	ts, ok := c.byTopic()[topic]
	if !ok {
		t.Fatalf("契约未收录主题 %s", topic)
	}
	if len(ts.Event) == 0 {
		t.Fatalf("契约主题 %s 未声明 event 侧", topic)
	}
	assertContractKeys(t, topic, "event", ts.Event, payload)
}

// ── 对账主体 ──────────────────────────────────────────────────────────────

// TestDataProducerKeysSubsetOfContract 驱动各数据域，断言实际发出键 ⊆ 契约（§3 全域）。
func TestDataProducerKeysSubsetOfContract(t *testing.T) {
	c := contractLoad(t)

	t.Run("user-config", func(t *testing.T) {
		bus, _, _ := newTestPersist(t)
		refresh := collectTopic(t, bus, "data-user-config-refresh")
		cfgRefresh := collectTopic(t, bus, "config-refresh")
		changed := collectTopic(t, bus, "data-user-config-changed")

		assertDataResult(t, c, bus, "data-user-config-list", map[string]any{"req_id": "r1"})
		assertDataResult(t, c, bus, "data-user-config-load", map[string]any{"req_id": "r2"})
		assertDataResult(t, c, bus, "data-user-config-save", map[string]any{
			"req_id": "r3", "data": map[string]any{"theme": "dark"},
		})
		assertDataEvent(t, c, "data-user-config-refresh", waitEvent(t, refresh, "data-user-config-refresh"))
		assertDataEvent(t, c, "config-refresh", waitEvent(t, cfgRefresh, "config-refresh"))
		assertDataEvent(t, c, "data-user-config-changed", waitEvent(t, changed, "data-user-config-changed"))
		assertDataResult(t, c, bus, "data-user-config-delete", map[string]any{"req_id": "r4", "id": "theme"})
	})

	t.Run("prj-config", func(t *testing.T) {
		bus, _, _ := newTestPersist(t)
		regInstance(t, bus)
		refresh := collectTopic(t, bus, "data-prj-config-refresh")

		assertDataResult(t, c, bus, "data-prj-config-list", map[string]any{"req_id": "r1", "instance_id": "ins-test"})
		assertDataResult(t, c, bus, "data-prj-config-save", map[string]any{
			"req_id": "r2", "instance_id": "ins-test",
			"data": map[string]any{"key": "smoke.key", "value": "v"},
		})
		assertDataEvent(t, c, "data-prj-config-refresh", waitEvent(t, refresh, "data-prj-config-refresh"))
		assertDataResult(t, c, bus, "data-prj-config-load", map[string]any{
			"req_id": "r3", "instance_id": "ins-test", "id": "smoke.key",
		})
		// 批量写：一次广播 1 条并带 ids（61 §3.1）
		assertDataResult(t, c, bus, "data-prj-config-save", map[string]any{
			"req_id": "r4", "instance_id": "ins-test",
			"data": map[string]any{"entries": map[string]any{"b.key": "2", "a.key": "1"}},
		})
		assertDataEvent(t, c, "data-prj-config-refresh", waitEvent(t, refresh, "data-prj-config-refresh"))
		assertDataResult(t, c, bus, "data-prj-config-delete", map[string]any{
			"req_id": "r5", "instance_id": "ins-test", "id": "smoke.key",
		})
	})

	t.Run("prompt", func(t *testing.T) {
		bus, _, _ := newTestPersist(t)
		regInstance(t, bus)
		refresh := collectTopic(t, bus, "data-prompt-refresh")
		assertDataResult(t, c, bus, "data-prompt-list", map[string]any{"req_id": "r1", "instance_id": "ins-test"})
		assertDataResult(t, c, bus, "data-prompt-save", map[string]any{
			"req_id": "r2", "instance_id": "ins-test",
			"data": map[string]any{"key": "smoke_prompt", "value": "v"},
		})
		assertDataEvent(t, c, "data-prompt-refresh", waitEvent(t, refresh, "data-prompt-refresh"))
		assertDataResult(t, c, bus, "data-prompt-load", map[string]any{
			"req_id": "r3", "instance_id": "ins-test", "id": "smoke_prompt",
		})
		assertDataResult(t, c, bus, "data-prompt-delete", map[string]any{
			"req_id": "r4", "instance_id": "ins-test", "id": "smoke_prompt",
		})
	})

	t.Run("prj-security", func(t *testing.T) {
		bus, _, _ := newTestPersist(t)
		regInstance(t, bus)
		refresh := collectTopic(t, bus, "data-prj-security-refresh")
		assertDataResult(t, c, bus, "data-prj-security-list", map[string]any{"req_id": "r1", "instance_id": "ins-test"})
		assertDataResult(t, c, bus, "data-prj-security-save", map[string]any{
			"req_id": "r2", "instance_id": "ins-test",
			"data": map[string]any{"key": "dir-smoke", "value": `{"dir":"E:/x","writable":true}`},
		})
		assertDataEvent(t, c, "data-prj-security-refresh", waitEvent(t, refresh, "data-prj-security-refresh"))
		assertDataResult(t, c, bus, "data-prj-security-load", map[string]any{
			"req_id": "r3", "instance_id": "ins-test", "id": "dir-smoke",
		})
		assertDataResult(t, c, bus, "data-prj-security-delete", map[string]any{
			"req_id": "r4", "instance_id": "ins-test", "id": "dir-smoke",
		})
	})

	t.Run("scenario", func(t *testing.T) {
		bus, _, _ := newTestPersistOpts(t, persist.Options{AppDir: appCapabilityRoot(t)})
		refresh := collectTopic(t, bus, "data-scenario-refresh")
		assertDataResult(t, c, bus, "data-scenario-list", map[string]any{"req_id": "r1"})
		assertDataResult(t, c, bus, "data-scenario-save", map[string]any{
			"req_id": "r2",
			"data": map[string]any{
				"id": "smoke_sc", "name": "冒烟", "level": "user",
				"agents": []any{map[string]any{"name": "主", "isMain": true, "prompt": "p"}},
			},
		})
		assertDataEvent(t, c, "data-scenario-refresh", waitEvent(t, refresh, "data-scenario-refresh"))
		assertDataResult(t, c, bus, "data-scenario-load", map[string]any{"req_id": "r3", "id": "smoke_sc"})
		assertDataResult(t, c, bus, "data-scenario-delete", map[string]any{"req_id": "r4", "id": "smoke_sc", "level": "user"})
	})

	t.Run("mcp", func(t *testing.T) {
		bus, _, _ := newTestPersist(t)
		regInstance(t, bus)
		refresh := collectTopic(t, bus, "data-mcp-refresh")
		assertDataResult(t, c, bus, "data-mcp-list", map[string]any{"req_id": "r1", "instance_id": "ins-test"})
		assertDataResult(t, c, bus, "data-mcp-save", map[string]any{
			"req_id": "r2", "instance_id": "ins-test",
			"data": map[string]any{"name": "smoke_mcp", "level": "user", "transport": "stdio", "command": "x"},
		})
		assertDataEvent(t, c, "data-mcp-refresh", waitEvent(t, refresh, "data-mcp-refresh"))
		assertDataResult(t, c, bus, "data-mcp-load", map[string]any{
			"req_id": "r3", "instance_id": "ins-test", "name": "smoke_mcp",
		})
		assertDataResult(t, c, bus, "data-mcp-delete", map[string]any{
			"req_id": "r4", "instance_id": "ins-test", "name": "smoke_mcp",
		})
	})

	t.Run("memory", func(t *testing.T) {
		bus, _, _ := newTestPersist(t)
		regInstance(t, bus)
		refresh := collectTopic(t, bus, "data-memory-refresh")
		assertDataResult(t, c, bus, "data-memory-list", map[string]any{"req_id": "r1", "instance_id": "ins-test"})
		assertDataResult(t, c, bus, "data-memory-read", map[string]any{
			"req_id": "r2", "instance_id": "ins-test", "category": "项目概要",
		})
		assertDataResult(t, c, bus, "data-memory-save", map[string]any{
			"req_id": "r3", "instance_id": "ins-test",
			"data": map[string]any{"category": "冒烟类别", "content": "x"},
		})
		assertDataEvent(t, c, "data-memory-refresh", waitEvent(t, refresh, "data-memory-refresh"))
		assertDataResult(t, c, bus, "data-memory-delete", map[string]any{
			"req_id": "r4", "instance_id": "ins-test",
			"data": map[string]any{"category": "冒烟类别"},
		})
		// 记忆提取进度专用表（OP-05/06，2026-10-06）
		assertDataResult(t, c, bus, "data-memory-extract-save", map[string]any{
			"req_id": "r5", "instance_id": "ins-test",
			"data": map[string]any{"session_id": "smoke-s", "category": "冒烟类别", "last_turn_id": "t1"},
		})
		assertDataResult(t, c, bus, "data-memory-extract-load", map[string]any{
			"req_id": "r6", "instance_id": "ins-test", "session_id": "smoke-s",
		})
		assertDataResult(t, c, bus, "data-memory-extract-delete", map[string]any{
			"req_id": "r7", "instance_id": "ins-test",
			"data": map[string]any{"session_id": "smoke-s"},
		})
	})

	t.Run("filelist", func(t *testing.T) {
		bus, _, _ := newTestPersist(t)
		regInstance(t, bus)
		assertDataResult(t, c, bus, "data-filelist-put", map[string]any{
			"req_id": "r1", "instance_id": "ins-test",
			"data": map[string]any{
				"key": "flk1", "path": "/w/a.go", "size": 1, "mtime": "2026-01-01T00:00:00Z",
				"md5": "x", "doc_ids": []any{}, "chunks": 0, "indexed_at": "2026-01-01T00:00:00Z",
			},
		})
		assertDataResult(t, c, bus, "data-filelist-list", map[string]any{"req_id": "r2", "instance_id": "ins-test"})
		assertDataResult(t, c, bus, "data-filelist-del", map[string]any{
			"req_id": "r3", "instance_id": "ins-test", "data": map[string]any{"keys": []any{"flk1"}},
		})
	})

	t.Run("index", func(t *testing.T) {
		bus, _, _ := newTestPersist(t)
		regInstance(t, bus)
		assertDataResult(t, c, bus, "data-index-ignored", map[string]any{
			"req_id": "r1", "instance_id": "ins-test",
			"data": map[string]any{"paths": []any{"a.go", "node_modules/x.js"}},
		})
	})
}

// TestDataSessionKeysSubsetOfContract 驱动会话域（§3.2 / §3.2a / §3.2b）+ 下行事件。
func TestDataSessionKeysSubsetOfContract(t *testing.T) {
	c := contractLoad(t)
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	sessionNew := collectTopic(t, bus, "session-new")
	titleChanged := collectTopic(t, bus, "data-session-title-changed")

	// §3.2a 运行时原语（先建会话/轮次/消息，后续查询才有内容）
	assertDataResult(t, c, bus, "data-session-ensure-session", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "session_id": "smoke-s1",
	})
	assertDataEvent(t, c, "session-new", waitEvent(t, sessionNew, "session-new"))

	assertDataResult(t, c, bus, "data-session-ensure-turn", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "turn_id": "smoke-t1", "session_id": "smoke-s1",
	})
	assertDataResult(t, c, bus, "data-session-append-message", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"turn_id": "smoke-t1",
		"msg":     map[string]any{"role": "user", "content": "hi", "session_id": "smoke-s1"},
	})
	assertDataResult(t, c, bus, "data-session-set-summary", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "turn_id": "smoke-t1", "summary": "摘要",
	})
	assertDataResult(t, c, bus, "data-session-complete-turn", map[string]any{
		"req_id": "r5", "instance_id": "ins-test", "turn_id": "smoke-t1", "status": "done",
	})
	assertDataResult(t, c, bus, "data-session-cleanup-stale", map[string]any{"req_id": "r6", "instance_id": "ins-test"})
	assertDataResult(t, c, bus, "data-session-load-messages", map[string]any{
		"req_id": "r7", "instance_id": "ins-test", "turn_id": "smoke-t1",
	})
	assertDataResult(t, c, bus, "data-session-context", map[string]any{
		"req_id": "r8", "instance_id": "ins-test", "session_id": "smoke-s1",
	})

	// §3.2 会话域查询/动作
	assertDataResult(t, c, bus, "data-session-list", map[string]any{"req_id": "r9", "instance_id": "ins-test"})
	assertDataResult(t, c, bus, "data-session-get", map[string]any{
		"req_id": "r10", "instance_id": "ins-test", "id": "smoke-s1",
	})
	assertDataResult(t, c, bus, "data-session-history", map[string]any{
		"req_id": "r11", "instance_id": "ins-test", "session_id": "smoke-s1",
	})
	assertDataResult(t, c, bus, "data-session-content", map[string]any{
		"req_id": "r12", "instance_id": "ins-test",
		"data": map[string]any{"session_id": "smoke-s1", "keys": []any{"message:none"}},
	})
	assertDataResult(t, c, bus, "data-session-latest", map[string]any{"req_id": "r13", "instance_id": "ins-test"})
	assertDataResult(t, c, bus, "data-session-active-set", map[string]any{
		"req_id": "r14", "instance_id": "ins-test", "data": map[string]any{"session_id": "smoke-s1"},
	})
	assertDataResult(t, c, bus, "data-session-active-get", map[string]any{"req_id": "r15", "instance_id": "ins-test"})
	assertDataResult(t, c, bus, "data-session-title", map[string]any{
		"req_id": "r16", "instance_id": "ins-test", "data": map[string]any{"id": "smoke-s1", "title": "冒烟标题"},
	})
	assertDataEvent(t, c, "data-session-title-changed", waitEvent(t, titleChanged, "data-session-title-changed"))

	// §3.2b 快照域
	assertDataResult(t, c, bus, "data-snapshot-set", map[string]any{
		"req_id": "r17", "instance_id": "ins-test",
		"data": map[string]any{"session_id": "smoke-s1", "snapshot": map[string]any{"history": []any{}, "snapshot_turn": ""}},
	})
	assertDataResult(t, c, bus, "data-snapshot-get", map[string]any{
		"req_id": "r18", "instance_id": "ins-test", "data": map[string]any{"session_id": "smoke-s1"},
	})

	// 删除会话（末步）
	assertDataResult(t, c, bus, "data-session-delete", map[string]any{
		"req_id": "r19", "instance_id": "ins-test", "id": "smoke-s1",
	})
}

// TestDataTasktreeKeysSubsetOfContract 驱动任务树域（§3.4）+ task-deleted 事件。
func TestDataTasktreeKeysSubsetOfContract(t *testing.T) {
	c := contractLoad(t)
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	deleted := collectTopic(t, bus, "task-deleted")

	assertDataResult(t, c, bus, "data-tasktree-upsert", map[string]any{
		"req_id": "r1", "instance_id": "ins-test",
		"data": map[string]any{
			"task_id": "smoke-n1", "top_session": "smoke-top", "session_id": "smoke-top",
			"kind": "tool", "title": "节点", "status": "pending", "created_at": "2026-01-01T00:00:00Z",
		},
	})
	assertDataResult(t, c, bus, "data-tasktree-list", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "data": map[string]any{"top_session": "smoke-top"},
	})
	assertDataResult(t, c, bus, "data-tasktree-tasks", map[string]any{
		"req_id": "r3", "instance_id": "ins-test", "data": map[string]any{"session_id": "smoke-top"},
	})
	assertDataResult(t, c, bus, "data-tasktree-delete", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "data": map[string]any{"node_id": "smoke-n1"},
	})
	assertDataEvent(t, c, "task-deleted", waitEvent(t, deleted, "task-deleted"))
}

// TestDataKnowledgeKeysSubsetOfContract 驱动知识库域（§3.3）全域动作。
func TestDataKnowledgeKeysSubsetOfContract(t *testing.T) {
	c := contractLoad(t)
	bus, _, _ := newTestPersistOpts(t, persist.Options{AppDir: t.TempDir()})
	regInstance(t, bus)

	res := assertDataResult(t, c, bus, "data-knowledge-root", map[string]any{
		"req_id": "r1", "instance_id": "ins-test", "data": map[string]any{"kind": "app"},
	})
	root, _ := res["root"].(string)
	if root == "" {
		t.Fatalf("knowledge root 解析失败: %+v", res)
	}
	toolsDir := filepath.ToSlash(filepath.Join(root, "tools"))

	assertDataResult(t, c, bus, "data-knowledge-list", map[string]any{
		"req_id": "r2", "instance_id": "ins-test", "data": map[string]any{"dir": root},
	})
	cr := assertDataResult(t, c, bus, "data-knowledge-create", map[string]any{
		"req_id": "r3", "instance_id": "ins-test",
		"data": map[string]any{"dir": toolsDir, "type": "tool", "name": "smoke_tool"},
	})
	path, _ := cr["path"].(string)
	assertDataResult(t, c, bus, "data-knowledge-read", map[string]any{
		"req_id": "r4", "instance_id": "ins-test", "data": map[string]any{"path": path},
	})
	doc := map[string]any{"title": "smoke_tool", "description": "d", "meta": map[string]any{}}
	assertDataResult(t, c, bus, "data-knowledge-save", map[string]any{
		"req_id": "r5", "instance_id": "ins-test", "data": map[string]any{"path": path, "doc": doc},
	})
	assertDataResult(t, c, bus, "data-knowledge-delete", map[string]any{
		"req_id": "r6", "instance_id": "ins-test", "data": map[string]any{"path": path},
	})
	md := assertDataResult(t, c, bus, "data-knowledge-mkdir", map[string]any{
		"req_id": "r7", "instance_id": "ins-test", "data": map[string]any{"parent": toolsDir, "name": "smoke_dir"},
	})
	dirPath, _ := md["path"].(string)
	assertDataResult(t, c, bus, "data-knowledge-rmdir", map[string]any{
		"req_id": "r8", "instance_id": "ins-test", "data": map[string]any{"path": dirPath},
	})
	md2 := assertDataResult(t, c, bus, "data-knowledge-mkdir", map[string]any{
		"req_id": "r9", "instance_id": "ins-test", "data": map[string]any{"parent": toolsDir, "name": "smoke_dir2"},
	})
	dirPath2, _ := md2["path"].(string)
	assertDataResult(t, c, bus, "data-knowledge-rename-dir", map[string]any{
		"req_id": "r10", "instance_id": "ins-test",
		"data": map[string]any{"path": dirPath2, "new_name": "smoke_dir3"},
	})
	cr2 := assertDataResult(t, c, bus, "data-knowledge-create", map[string]any{
		"req_id": "r11", "instance_id": "ins-test",
		"data": map[string]any{"dir": toolsDir, "type": "tool", "name": "smoke_rn"},
	})
	path2, _ := cr2["path"].(string)
	assertDataResult(t, c, bus, "data-knowledge-rename", map[string]any{
		"req_id": "r12", "instance_id": "ins-test",
		"data": map[string]any{"path": path2, "new_name": "smoke_rn2"},
	})
}

// TestDataDownlinkEventRegistry 登记 §3 数据面的**下行事件**（只由写操作触发，均在对应子测试
// 经事件侧对账）——显式声明「无遗漏项」；本批数据面 79 主题全部可经内存总线驱动（无未驱动项）。
func TestDataDownlinkEventRegistry(t *testing.T) {
	entries := []struct {
		topic  string
		reason string
	}{
		// §3 数据面（79 主题）能经内存总线驱动者已全部覆盖（请求 result 侧 + 下行 event 侧）；
		// 此处显式列出「只由写操作触发、在对应子测试经事件侧对账」的下行主题，声明无遗漏。
		{"data-user-config-refresh", "由 user-config save/delete 触发（user-config 子测试事件侧对账）"},
		{"data-prj-config-refresh", "由 prj-config save/delete 触发（prj-config 子测试事件侧对账）"},
		{"data-prj-security-refresh", "由 prj-security save/delete 触发（prj-security 子测试事件侧对账）"},
		{"data-prompt-refresh", "由 prompt save/delete 触发（prompt 子测试事件侧对账）"},
		{"data-scenario-refresh", "由 scenario save/delete 触发（scenario 子测试事件侧对账）"},
		{"data-mcp-refresh", "由 mcp save/delete 触发（mcp 子测试事件侧对账）"},
		{"data-memory-refresh", "由 memory save/delete 触发（memory 子测试事件侧对账）"},
		{"config-refresh", "由 user-config save/delete 额外兼容广播（user-config 子测试事件侧对账）"},
		{"data-user-config-changed", "由 user-config save/delete 触发（user-config 子测试事件侧对账）"},
		{"data-session-title-changed", "由 data-session-title 写库成功后广播（session 子测试事件侧对账）"},
		{"task-deleted", "由 data-tasktree-delete（非 shadow）后广播（tasktree 子测试事件侧对账）"},
		{"session-new", "由 data-session-ensure-session 首次落库后广播（session 子测试事件侧对账）"},
	}
	for _, e := range entries {
		if e.topic == "" || e.reason == "" {
			t.Errorf("未驱动登记项不完整: %+v", e)
		}
		t.Logf("[事件侧对账] topic=%s | 说明=%s", e.topic, e.reason)
	}
}
