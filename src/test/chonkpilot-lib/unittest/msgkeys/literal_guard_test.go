// literal_guard_test.go — 消息面「主题字面量」守卫（2026-10-05，常量 sweep 阶段收尾）。
//
// 目标：禁止生产代码以**字符串字面量**书写 61 契约主题名——一律引用生成的 `msgkeys.Topic*`
// 常量（契约唯一源 = docs/spec/60-reference/61-messages.schema.json，见 50-测试体系 §8.7）。
//
// 方法（务实近似，非类型化）：用 go/scanner 逐 token 取**字符串字面量**（自动跳过注释/标识符，
// 故不受文档注释里出现的主题名干扰），strconv.Unquote 后与契约 topic 集合比对；命中即红。
// 采用 scanner 而非正则：正则无法区分「注释里的主题名」与「代码字面量」，会误报。
//
// 覆盖边界（局限，务必知悉）：
//   - 仅扫 **guardedFiles 白名单**（本批已 sweep 的生产文件）；src/lib 其余文件（未 sweep 面）
//     不在本批，待后续批次**逐文件**补入（避免一次性大改，见 50 §8.7）。
//   - 只判**完整名字**（契约主题 + schema `clientTopic`，如 "filesys.list" / "tools-list"）；
//     前缀拼接（如 "data-session-" + action）与桥内构造式不判。
//   - 字段名/结果键不在本守卫（噪声高）；其防漂移由前端对账反向校验 + 生成物逐字比对兜底。
package msgkeys_test

import (
	"encoding/json"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// guardedFiles 是本批已完成常量 sweep 的生产文件（相对仓库根）。
// 新增消息面处理方 / 后续批次 sweep 后，须同步把文件补入本表（否则视为未覆盖）。
var guardedFiles = []string{
	"src/lib/gui/bridge/bridge.go",
	"src/lib/gui/bridge/optimize.go",
	"src/lib/gui/bridge/local.go",
	"src/lib/gui/bridge/data.go",
	"src/lib/gui/bridge/compat.go",
	"src/lib/gui/bridge/guimsg.go",
	"src/lib/gui/bridge/login.go",
	"src/lib/gui/bridge/builtins.go",
	"src/lib/gui/bridge/capture.go",
	"src/lib/gui/bridge/configfile.go",
	"src/lib/gui/bridge/facade_domains.go",
	"src/lib/gui/bridge/facade_session.go",
	"src/lib/gui/bridge/toolchain.go",
	"src/lib/gui/bridge/upload.go",
	"src/lib/gui/internal/messages/messages.go",
	"src/lib/gui/registry.go",
	"src/lib/gui/main.go",
	"src/lib/gui/testserver.go",
	"src/lib/gui/loglevel.go",
	"src/lib/data/persist/envelope.go",
	"src/lib/data/persist/persist.go",
	"src/lib/data/persist/compat.go",
	"src/lib/data/persist/sweep_split.go",
	"src/lib/data/persist/sweep_inprocess.go",
	"src/lib/data/facade/api.go",
	"src/lib/data/facade/config.go",
	"src/lib/data/facade/knowledge.go",
	"src/lib/data/facade/mcp.go",
	"src/lib/data/facade/memory.go",
	"src/lib/data/facade/filelist.go",
	"src/lib/data/facade/scenario.go",
	"src/lib/data/facade/session.go",
	"src/lib/data/facade/snapshot.go",
	"src/lib/data/facade/tasktree.go",
	"src/lib/data/facade/turn.go",
	"src/lib/data/facade/message.go",
	"src/lib/data/facade/project.go",
	"src/lib/data/facade/scope.go",
	"src/lib/data/facade/wire/domains.go",
	"src/lib/data/facade/wire/wire.go",
	"src/lib/data/facade/inline/inline.go",
	"src/lib/data/internal/config/config_facade.go",
	"src/lib/data/internal/config/userconfig.go",
	"src/lib/data/internal/config/prompt.go",
	"src/lib/data/internal/config/guistate.go",
	"src/lib/data/internal/config/service.go",
	"src/lib/data/internal/session/session_facade.go",
	"src/lib/data/internal/session/service.go",
	"src/lib/data/internal/tasktree/tasktree.go",
	"src/lib/data/internal/kernel/conf.go",
	"src/lib/data/internal/kernel/kernel.go",
	"src/lib/data/internal/kernel/root.go",
	"src/lib/data/internal/kernel/viewdata.go",
	"src/lib/data/internal/capfs/capfs.go",
	"src/lib/data/internal/capfs/scenario.go",
	"src/lib/data/internal/capfs/primitive.go",
	"src/lib/data/internal/capfs/mcps.go",
	"src/lib/data/internal/scenario/scenario.go",
	"src/lib/data/internal/knowledge/knowledge.go",
	"src/lib/data/internal/memory/memory.go",
	"src/lib/data/internal/filelist/filelist.go",
	"src/lib/data/internal/snapshot/service.go",
	"src/lib/data/internal/snapshot/store.go",
	"src/lib/data/internal/project/project.go",
	"src/lib/data/internal/project/probe.go",
	"src/lib/data/internal/indexignored/indexignored.go",
	"src/lib/llm/httpapi/publish.go",
	"src/lib/llm/httpapi/httpapi.go",
	"src/lib/llm/server/server.go",
	"src/lib/llm/server/claim.go",
	"src/lib/llm/server/login.go",
	"src/lib/llm/server/llm_testconn.go",
	"src/lib/llm/server/domainmcp.go",
	"src/lib/llm/server/gwclient.go",
	"src/lib/llm/server/prompt.go",
	"src/lib/llm/server/pluginnotice.go",
	"src/lib/llm/server/memory_guide.go",
	"src/lib/llm/server/session.go",
	"src/lib/llm/server/tasks.go",
	"src/lib/llm/server/turn.go",
	"src/lib/llm/server/assemble.go",
	"src/lib/llm/server/toolchain.go",
	"src/lib/filesys/filesys.go",
	"src/lib/filesys/watcher.go",
	"src/lib/task/store.go",
	"src/lib/task/layer.go",
	"src/lib/gateway/gateway/subjects.go",
	"src/lib/gateway/gateway/meta_tools.go",
	"src/lib/gateway/internal/facade/facade.go",
	"src/plugins/plugin-compress/compress.go",
	"src/plugins/plugin-compress/summarize.go",
	"src/plugins/plugin-compress/locate.go",
	"src/plugins/plugin-codegraph/codegraph.go",
	"src/plugins/plugin-codegraph/callgate.go",
	"src/plugins/plugin-vfts/vfts.go",
	"src/plugins/plugin-vfts/manifest.go",
	"src/plugins/plugin-vfts/callgate.go",
	"src/desktop/cli/main.go",
}

// findRepoRoot 从本测试源文件向上查找到含契约文件的仓库根。
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	for d := filepath.Dir(file); ; {
		cand := filepath.Join(d, "docs", "spec", "60-reference", "61-messages.schema.json")
		if _, err := os.Stat(cand); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("未找到 61-messages.schema.json（自 %s 向上）", filepath.Dir(file))
		}
		d = parent
	}
}

// contractTopics 读契约，返回**禁止字面量的名字集合** = 契约主题 ∪ 各主题声明的 clientTopic
// （客户端 topic，如 tools-list/prompts-list/resources-list；schema `clientTopic`，由 genmsg
// 生成 `MsgClientTopics*` 常量）。两者均须以 msgkeys 常量引用。
func contractTopics(t *testing.T, root string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "docs", "spec", "60-reference", "61-messages.schema.json"))
	if err != nil {
		t.Fatalf("读契约失败: %v", err)
	}
	var doc struct {
		Topics []struct {
			Topic       string `json:"topic"`
			ClientTopic string `json:"clientTopic"`
		} `json:"topics"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析契约失败: %v", err)
	}
	out := make(map[string]bool, len(doc.Topics))
	for _, ts := range doc.Topics {
		if ts.Topic != "" {
			out[ts.Topic] = true
		}
		if ts.ClientTopic != "" {
			out[ts.ClientTopic] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("契约未收录任何主题")
	}
	return out
}

// goStringLiterals 用 go/scanner 收集源码里的**字符串字面量**值（自动跳过注释/标识符）。
func goStringLiterals(t *testing.T, path string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f := fset.AddFile(path, fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(f, src, func(_ token.Position, _ string) {}, 0) // ScanComments=0 → 跳过注释
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.STRING {
			continue
		}
		v, err := strconv.Unquote(lit)
		if err != nil {
			continue // 原始串/异常 → 跳过（不影响主题判定）
		}
		out = append(out, v)
	}
	return out
}

// TestNoTopicLiteralsInGuardedFiles 断言白名单文件里不出现契约主题名字符串字面量。
func TestNoTopicLiteralsInGuardedFiles(t *testing.T) {
	root := findRepoRoot(t)
	topics := contractTopics(t, root)

	total := 0
	for _, rel := range guardedFiles {
		rel := rel
		t.Run(filepath.Base(rel), func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(rel))
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("读源文件失败 %s: %v", rel, err)
			}
			var bad []string
			for _, lit := range goStringLiterals(t, path, src) {
				if topics[lit] {
					bad = append(bad, lit)
				}
			}
			if len(bad) > 0 {
				t.Errorf("%s 出现契约主题字面量 %v —— 请改用 msgkeys.Topic* 常量（50 §8.7）", rel, bad)
			}
			total++
		})
	}
	if total == 0 {
		t.Fatal("白名单为空（守卫未覆盖任何文件）")
	}
	t.Logf("主题/客户端topic 字面量守卫：%d 文件 · 禁止名 %d", total, len(topics))
}
