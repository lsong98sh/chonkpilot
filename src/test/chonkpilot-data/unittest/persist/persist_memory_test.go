// 记忆库（memory）域往返：data-memory-{list,read,save,delete}——
// 首次 list 预置类别文件（项目级 <workdir>/.chonkpilot/memory + 用户级 用户偏好.md）；
// 读/写某类全文；预估 token 与字符数/2 口径一致；类别白名单；save/delete 后广播 data-memory-refresh。
package persist_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// memoryProjectCategories 是项目级类别（与 persist 内部清单一致，黑盒复制）。
var memoryProjectCategories = []string{
	"项目概要", "共同库", "开发规范", "构建发布规则", "接口库", "测试规范", "典型参照", "用户决策",
}

// enableMemory 打开实例记忆库总开关（data-prj-config-save，既有面）。
// P0-B 起：预置类别文件**仅记忆库启用时**才落盘 → 断言"预置文件已创建"的用例须先启用。
func enableMemory(t *testing.T, bus mq.Bus) {
	t.Helper()
	r := dataCall(t, bus, "data-prj-config-save", map[string]any{
		"req_id": "mem-enable", "instance_id": "ins-test",
		"data": map[string]any{"key": "memory.enabled", "value": "true"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("启用记忆库失败: %+v", r)
	}
}

// TestDataMemoryListPresetReadSave：list 预置类别文件（8 项目级 + 1 用户级）→ read/save 往返。
func TestDataMemoryListPresetReadSave(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)
	wd := regInstance(t, bus)
	memDir := filepath.Join(wd, ".chonkpilot", "memory")
	usrPref := filepath.Join(filepath.Dir(usrPath), "用户偏好.md")
	enableMemory(t, bus) // 预置文件仅在启用时落盘（P0-B）

	// list 首次 → 预置类别文件（8 项目级 + 1 用户级）
	r := dataCall(t, bus, "data-memory-list", map[string]any{"req_id": "m1", "instance_id": "ins-test"})
	list := dataList(dataResult(t, r))
	if len(list) != len(memoryProjectCategories)+1 {
		t.Fatalf("类别数=%d want %d: %+v", len(list), len(memoryProjectCategories)+1, list)
	}
	seen := map[string]map[string]any{}
	for _, e := range list {
		m, _ := e.(map[string]any)
		seen[persist.Sval(m["category"])] = m
	}
	for _, cat := range memoryProjectCategories {
		m := seen[cat]
		if m == nil {
			t.Fatalf("缺少项目级类别 %s: %+v", cat, list)
		}
		if m["level"] != "project" {
			t.Fatalf("%s level=%v", cat, m["level"])
		}
		wantPath := filepath.ToSlash(filepath.Join(memDir, cat+".md"))
		if m["path"] != wantPath {
			t.Fatalf("%s path=%v want %s", cat, m["path"], wantPath)
		}
		if _, err := os.Stat(filepath.Join(memDir, cat+".md")); err != nil {
			t.Fatalf("%s 预置文件缺失: %v", cat, err)
		}
	}
	user := seen["用户偏好"]
	if user == nil || user["level"] != "user" {
		t.Fatalf("用户偏好条目缺失/级别错: %+v", seen["用户偏好"])
	}
	if user["path"] != filepath.ToSlash(usrPref) {
		t.Fatalf("用户偏好 path=%v want %s", user["path"], usrPref)
	}
	if _, err := os.Stat(usrPref); err != nil {
		t.Fatalf("用户偏好预置文件缺失: %v", err)
	}

	// read 项目级：预置模板含用途说明；tokens = 字符数/2
	r = dataCall(t, bus, "data-memory-read", map[string]any{
		"req_id": "m2", "instance_id": "ins-test",
		"data": map[string]any{"category": "项目概要"},
	})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	content := persist.Sval(d["content"])
	if !strings.Contains(content, "用途") {
		t.Fatalf("预置模板应含用途说明: %q", content)
	}
	if got, want := d["tokens"].(float64), float64(utf8.RuneCountInString(content)/2); got != want {
		t.Fatalf("tokens=%v want %v（字符数/2 口径）", got, want)
	}

	// save → 落盘 + 读回一致
	long := "# 项目概要\n\n本项目是 X。\n" + strings.Repeat("甲", 300)
	r = dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "m3", "instance_id": "ins-test",
		"data": map[string]any{"category": "项目概要", "content": long},
	})
	res := dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok || res["id"] != "项目概要" {
		t.Fatalf("save failed: %+v", r)
	}
	raw, err := os.ReadFile(filepath.Join(memDir, "项目概要.md"))
	if err != nil || string(raw) != long {
		t.Fatalf("落盘内容不一致: err=%v", err)
	}
	r = dataCall(t, bus, "data-memory-read", map[string]any{
		"req_id": "m4", "instance_id": "ins-test",
		"data": map[string]any{"category": "项目概要"},
	})
	d, _ = dataResult(t, r)["data"].(map[string]any)
	if persist.Sval(d["content"]) != long {
		t.Fatalf("读回不一致: %q", d["content"])
	}
	if got, want := d["tokens"].(float64), float64(utf8.RuneCountInString(long)/2); got != want {
		t.Fatalf("tokens=%v want %v", got, want)
	}

	// 用户偏好（唯一用户级）读写
	r = dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "m5", "instance_id": "ins-test",
		"data": map[string]any{"category": "用户偏好", "content": "偏好中文回答"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("用户偏好 save failed: %+v", r)
	}
	if raw, err := os.ReadFile(usrPref); err != nil || string(raw) != "偏好中文回答" {
		t.Fatalf("用户偏好落盘不一致: err=%v raw=%q", err, string(raw))
	}

	// 类别白名单：未知类别 → 失败应答
	r = dataCall(t, bus, "data-memory-read", map[string]any{
		"req_id": "m6", "instance_id": "ins-test",
		"data": map[string]any{"category": "不存在的类别"},
	})
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("未知类别应失败: %+v", r)
	}
}

// TestDataMemoryRefreshBroadcast：save 后广播 data-memory-refresh（带类别 id 与最新清单）。
func TestDataMemoryRefreshBroadcast(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	ch := collectRefresh(t, bus, "memory")

	dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "m1", "instance_id": "ins-test",
		"data": map[string]any{"category": "开发规范", "content": "统一 gofmt"},
	})
	select {
	case m := <-ch:
		if m["op"] != "save" || m["id"] != "开发规范" {
			t.Fatalf("refresh=%+v", m)
		}
		if list, _ := m["list"].([]any); len(list) != len(memoryProjectCategories)+1 {
			t.Fatalf("refresh 应附带完整类别清单: %+v", m["list"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("save 后未收到 data-memory-refresh")
	}
}

// memCategoryOf 在 data-memory-list 应答中按类别名取条目（未命中 → nil）。
func memCategoryOf(t *testing.T, bus mq.Bus, reqID, category string) map[string]any {
	t.Helper()
	r := dataCall(t, bus, "data-memory-list", map[string]any{"req_id": reqID, "instance_id": "ins-test"})
	for _, e := range dataList(dataResult(t, r)) {
		m, _ := e.(map[string]any)
		if persist.Sval(m["category"]) == category {
			return m
		}
	}
	return nil
}

// TestDataMemoryCustomCategoryCRUD（41 I-66）：自定义类别 = 预置集 ∪ 目录内 <类别>.md
// —— 新建（可带空内容）/读取/保存/删除；预置与自定义并存；删除预置被拒。
func TestDataMemoryCustomCategoryCRUD(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	wd := regInstance(t, bus)
	memDir := filepath.Join(wd, ".chonkpilot", "memory")
	enableMemory(t, bus) // 预置文件仅在启用时落盘（P0-B；用例末尾断言 项目概要.md 存在）

	// 初始：仅预置 8+1
	r := dataCall(t, bus, "data-memory-list", map[string]any{"req_id": "c1", "instance_id": "ins-test"})
	if n := len(dataList(dataResult(t, r))); n != len(memoryProjectCategories)+1 {
		t.Fatalf("初始类别数=%d want %d", n, len(memoryProjectCategories)+1)
	}

	// 新建自定义类别（带内容）→ save 成功并落盘
	r = dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "c2", "instance_id": "ins-test",
		"data": map[string]any{"category": "架构决策", "content": "记录架构决策"},
	})
	res := dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok || persist.Sval(res["id"]) != "架构决策" {
		t.Fatalf("新建自定义类别失败: %+v", r)
	}
	customPath := filepath.Join(memDir, "架构决策.md")
	if raw, err := os.ReadFile(customPath); err != nil || string(raw) != "记录架构决策" {
		t.Fatalf("自定义类别未落盘: err=%v raw=%q", err, string(raw))
	}

	// 新建空内容自定义类别 → 同样出现在清单（文件为空的 .md 即类别）
	r = dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "c3", "instance_id": "ins-test",
		"data": map[string]any{"category": "待整理", "content": ""},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("新建空类别失败: %+v", r)
	}

	// list 增返自定义项（项目级，路径在记忆目录内），预置项不回归
	m := memCategoryOf(t, bus, "c4", "架构决策")
	if m == nil || m["level"] != "project" || m["path"] != filepath.ToSlash(customPath) {
		t.Fatalf("list 自定义项错: %+v", m)
	}
	if memCategoryOf(t, bus, "c5", "待整理") == nil {
		t.Fatal("空内容自定义类别应出现在清单")
	}
	if memCategoryOf(t, bus, "c6", "项目概要") == nil {
		t.Fatal("预置类别不回归")
	}

	// read 自定义类别 → 内容 + token（字符数/2 口径）
	r = dataCall(t, bus, "data-memory-read", map[string]any{
		"req_id": "c7", "instance_id": "ins-test",
		"data": map[string]any{"category": "架构决策"},
	})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d == nil || persist.Sval(d["content"]) != "记录架构决策" || d["level"] != "project" {
		t.Fatalf("读取自定义类别错: %+v", d)
	}

	// save 覆写自定义类别内容
	r = dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "c8", "instance_id": "ins-test",
		"data": map[string]any{"category": "架构决策", "content": "改写后的决策"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("覆写自定义类别失败: %+v", r)
	}

	// 删除自定义类别：data-memory-delete → 文件移除 + 清单消失
	ch := collectRefresh(t, bus, "memory")
	r = dataCall(t, bus, "data-memory-delete", map[string]any{
		"req_id": "c9", "instance_id": "ins-test",
		"data": map[string]any{"category": "架构决策"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("删除自定义类别失败: %+v", r)
	}
	if _, err := os.Stat(customPath); !os.IsNotExist(err) {
		t.Fatal("删除后内容文件应被移除")
	}
	if memCategoryOf(t, bus, "c10", "架构决策") != nil {
		t.Fatal("删除后自定义类别应从清单消失")
	}
	select {
	case rm := <-ch:
		if rm["op"] != "delete" || rm["id"] != "架构决策" {
			t.Fatalf("删除 refresh=%+v", rm)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("删除后未收到 data-memory-refresh")
	}
	// 删除后 read → unknown
	r = dataCall(t, bus, "data-memory-read", map[string]any{
		"req_id": "c11", "instance_id": "ins-test",
		"data": map[string]any{"category": "架构决策"},
	})
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("已删除类别应读取失败: %+v", r)
	}

	// 预置类别不可删除（仅可清空内容）
	r = dataCall(t, bus, "data-memory-delete", map[string]any{
		"req_id": "c12", "instance_id": "ins-test",
		"data": map[string]any{"category": "项目概要"},
	})
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("删除预置类别应被拒: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(memDir, "项目概要.md")); err != nil {
		t.Fatalf("预置类别文件不应被删: %v", err)
	}
}

// TestDataMemoryClearContentKeepsCategory（2026-09-20，批 3 ⑱「清空某类别内容」）：
// 清空 = **保留类别、清空内容**（与「删除类别」区分）——复用既有 data-memory-save 写空串：
//   - 预置类别：清空后**仍在清单**（预置恒在），内容为空、tokens 0，文件保留（空 .md）；
//   - 自定义类别：清空后**仍在清单**（类别 = 目录内 <类别>.md，文件保留即类别保留），
//     而 data-memory-delete 才是移除清单项 + 文件（两者语义互不混淆）。
func TestDataMemoryClearContentKeepsCategory(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	wd := regInstance(t, bus)
	memDir := filepath.Join(wd, ".chonkpilot", "memory")
	enableMemory(t, bus)
	dataCall(t, bus, "data-memory-list", map[string]any{"req_id": "cl0", "instance_id": "ins-test"})

	// 预置类别：先写内容 → 清空（save 空串）→ 类别保留、内容为空
	presetPath := filepath.Join(memDir, "项目概要.md")
	dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "cl1", "instance_id": "ins-test",
		"data": map[string]any{"category": "项目概要", "content": "待清空的内容"},
	})
	r := dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "cl2", "instance_id": "ins-test",
		"data": map[string]any{"category": "项目概要", "content": ""},
	})
	res := dataResult(t, r)
	if ok, _ := res["ok"].(bool); !ok || persist.Sval(res["id"]) != "项目概要" {
		t.Fatalf("清空预置类别内容失败: %+v", r)
	}
	raw, err := os.ReadFile(presetPath)
	if err != nil {
		t.Fatalf("清空后类别文件应保留（清空 ≠ 删除）: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("清空后文件内容应为空，实得 %q", string(raw))
	}
	r = dataCall(t, bus, "data-memory-read", map[string]any{
		"req_id": "cl3", "instance_id": "ins-test",
		"data": map[string]any{"category": "项目概要"},
	})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d == nil || persist.Sval(d["content"]) != "" || d["tokens"].(float64) != 0 {
		t.Fatalf("清空后 read 应为空内容 + tokens 0: %+v", d)
	}
	m := memCategoryOf(t, bus, "cl4", "项目概要")
	if m == nil {
		t.Fatal("清空后预置类别应仍在清单")
	}
	if m["tokens"].(float64) != 0 {
		t.Fatalf("清空后清单 tokens 应为 0: %+v", m)
	}

	// 自定义类别：先建（有内容）→ 清空 → 仍为类别；delete 才是移除
	customPath := filepath.Join(memDir, "架构决策.md")
	dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "cl5", "instance_id": "ins-test",
		"data": map[string]any{"category": "架构决策", "content": "记录架构决策"},
	})
	dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "cl6", "instance_id": "ins-test",
		"data": map[string]any{"category": "架构决策", "content": ""},
	})
	if raw, err := os.ReadFile(customPath); err != nil || len(raw) != 0 {
		t.Fatalf("清空后自定义类别文件应保留且为空: err=%v raw=%q", err, string(raw))
	}
	if memCategoryOf(t, bus, "cl7", "架构决策") == nil {
		t.Fatal("清空后自定义类别应仍在清单（清空 ≠ 删除）")
	}
	if _, err := os.Stat(customPath); err != nil {
		t.Fatalf("清空不得删除文件: %v", err)
	}
}

// TestDataMemoryInvalidCategoryRejected（41 I-66 安全）：非法 category 名一律被拒
// （路径分隔符 / 目录穿越 / 空 / 空白 / 超长 / 保留字符 / 保留设备名），不产生越界文件。
func TestDataMemoryInvalidCategoryRejected(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	wd := regInstance(t, bus)
	memDir := filepath.Join(wd, ".chonkpilot", "memory")
	enableMemory(t, bus) // 预置文件仅在启用时落盘（P0-B；用例末尾断言目录内恰 8 个预置文件）

	// 先 list 预置类别（合法路径），后续非法名不得污染记忆目录
	dataCall(t, bus, "data-memory-list", map[string]any{"req_id": "seed", "instance_id": "ins-test"})

	bad := []string{
		"", "   ", "a/b", "../evil", "..", ".", "a\\b",
		"c:name", "a*b", "a?b", "a\"b", "a<b", "a>b", "a|b",
		"CON", "com1", "NUL", strings.Repeat("甲", 65), " 首尾空格", "内 部空格",
	}
	for i, name := range bad {
		r := dataCall(t, bus, "data-memory-save", map[string]any{
			"req_id": "bad-save", "instance_id": "ins-test",
			"data": map[string]any{"category": name, "content": "x"},
		})
		if ok, _ := r["ok"].(bool); ok {
			t.Fatalf("非法 category %q(#%d) 应被拒，却成功: %+v", name, i, r)
		}
		if r["error"] == nil || r["error"] == "" {
			t.Fatalf("非法 category %q 缺错误信息: %+v", name, r)
		}
		// read 同样拒绝
		r = dataCall(t, bus, "data-memory-read", map[string]any{
			"req_id": "bad-read", "instance_id": "ins-test",
			"data": map[string]any{"category": name},
		})
		if ok, _ := r["ok"].(bool); ok {
			t.Fatalf("非法 category %q read 应被拒: %+v", name, r)
		}
	}
	// 越界文件不产生：记忆目录内仅预置 8 个 .md；workdir 根下无越界文件
	entries, err := os.ReadDir(memDir)
	if err != nil {
		t.Fatalf("记忆目录应存在: %v", err)
	}
	if len(entries) != len(memoryProjectCategories) {
		t.Fatalf("记忆目录应仅含 %d 个预置文件，实得 %d: %+v", len(memoryProjectCategories), len(entries), entries)
	}
	if _, err := os.Stat(filepath.Join(wd, "evil.md")); !os.IsNotExist(err) {
		t.Fatal("目录穿越未阻止：workdir 下出现 evil.md")
	}

	// 合法长度边界（64 字符）可创建
	ok64 := strings.Repeat("甲", 64)
	r := dataCall(t, bus, "data-memory-save", map[string]any{
		"req_id": "ok64", "instance_id": "ins-test",
		"data": map[string]any{"category": ok64, "content": "边界"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("64 字符类别名应被接受: %+v", r)
	}
}

// assertNoMemoryFiles 断言关闭态未落盘任何记忆文件（记忆目录不存在/为空 + 无用户偏好.md）。
func assertNoMemoryFiles(t *testing.T, memDir, usrPref string) {
	t.Helper()
	if entries, err := os.ReadDir(memDir); err == nil && len(entries) != 0 {
		t.Fatalf("记忆库关闭时不应落盘记忆文件: %+v", entries)
	}
	if _, err := os.Stat(usrPref); !os.IsNotExist(err) {
		t.Fatalf("记忆库关闭时不应创建用户偏好.md: err=%v", err)
	}
}

// TestDataMemoryListDisabledNoPresetFiles（P0-B）：记忆库关闭（配置缺失 / 显式 false）→
// data-memory-list 清单照常返回（预置 8 + 用户偏好），但**不落盘创建**任何 .md 文件；
// 启用后再次 list → 预置补齐（既有行为保留）。
func TestDataMemoryListDisabledNoPresetFiles(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)
	wd := regInstance(t, bus)
	memDir := filepath.Join(wd, ".chonkpilot", "memory")
	usrPref := filepath.Join(filepath.Dir(usrPath), "用户偏好.md")

	// ① 配置缺失（默认关闭）→ list 不落盘
	r := dataCall(t, bus, "data-memory-list", map[string]any{"req_id": "d1", "instance_id": "ins-test"})
	if n := len(dataList(dataResult(t, r))); n != len(memoryProjectCategories)+1 {
		t.Fatalf("关闭态清单仍应为 %d 项，实得 %d", len(memoryProjectCategories)+1, n)
	}
	assertNoMemoryFiles(t, memDir, usrPref)

	// ② 显式 false（设置页关闭后保存的形态）→ list 仍不落盘
	r = dataCall(t, bus, "data-prj-config-save", map[string]any{
		"req_id": "d2", "instance_id": "ins-test",
		"data": map[string]any{"key": "memory.enabled", "value": "false"},
	})
	if ok, _ := dataResult(t, r)["ok"].(bool); !ok {
		t.Fatalf("写 memory.enabled=false 失败: %+v", r)
	}
	r = dataCall(t, bus, "data-memory-list", map[string]any{"req_id": "d3", "instance_id": "ins-test"})
	if n := len(dataList(dataResult(t, r))); n != len(memoryProjectCategories)+1 {
		t.Fatalf("关闭态清单仍应为 %d 项，实得 %d", len(memoryProjectCategories)+1, n)
	}
	assertNoMemoryFiles(t, memDir, usrPref)

	// ③ 启用后 list → 预置文件补齐（8 项目级 + 用户偏好）
	enableMemory(t, bus)
	dataCall(t, bus, "data-memory-list", map[string]any{"req_id": "d4", "instance_id": "ins-test"})
	if _, err := os.Stat(filepath.Join(memDir, "项目概要.md")); err != nil {
		t.Fatalf("启用后 list 应补齐预置文件: %v", err)
	}
	if _, err := os.Stat(usrPref); err != nil {
		t.Fatalf("启用后 list 应补齐用户偏好文件: %v", err)
	}
}
