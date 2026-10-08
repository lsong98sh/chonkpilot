// userconfig_test.go — usr 配置读写白盒（白名单键落库/回读；清空整份范围）。
//
// 阶段 4「internal 下沉」：随 config 域实现由 `chonkpilot-data/persist` 下移至本包
// （断言逐条未改；`&Service{}` 零值仍可用——被调方法不使用接收者字段）。
package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

// openTestDB 打开一个临时 usr 库（测试隔离）。
func openTestDB(t *testing.T) *data.DB {
	t.Helper()
	db, err := data.Open(filepath.Join(t.TempDir(), "usr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestSaveUserConfigCCompilerPathRoundTrip ① usr 写规范键 cCompilerPath → 落库 + 回读一致。
func TestSaveUserConfigCCompilerPathRoundTrip(t *testing.T) {
	db := openTestDB(t)
	const val = `C:\msys64\ucrt64\bin\gcc.exe`

	if err := saveUserConfig(db, map[string]any{"cCompilerPath": val}); err != nil {
		t.Fatalf("save cCompilerPath: %v", err)
	}
	if got, ok := data.GetConfig(db, "cCompilerPath"); !ok || got != val {
		t.Fatalf("cCompilerPath 未落库：got=%q ok=%v", got, ok)
	}
	if v, ok := readUserConfig(db)["cCompilerPath"]; !ok || v != val {
		t.Fatalf("readUserConfig 未回读 cCompilerPath：%v", v)
	}
}

// TestDeleteUserConfigClearsWholeUsrScope「恢复出厂」清空范围（批 3 · ⑯）：
// handleUserConfig delete **不带 key** → deleteUserConfig → 标量键 + 自由键 + 集合表（llms）
// 全部清空（读回回落系统默认）；同库**非用户配置键**不受影响
// （前端入口 = SettingsConfigIOPage「恢复出厂设置」，经 data-user-config-delete 无 key）。
func TestDeleteUserConfigClearsWholeUsrScope(t *testing.T) {
	db := openTestDB(t)

	// 铺满三个通道 + 一个同库无关键
	if err := saveUserConfig(db, map[string]any{
		"theme":       "dark",
		"retryCount":  5,
		"recent_dirs": `["D:\\proj"]`,
		"tool_async":  `{"demo":{"mode":"auto"}}`,
		"llms":        []any{map[string]any{"name": "gpt-local", "apiKey": "sk-x"}},
	}); err != nil {
		t.Fatalf("铺数据失败：%v", err)
	}
	if err := data.SetConfig(db, "unrelated_key", "1"); err != nil {
		t.Fatalf("写无关键失败：%v", err)
	}

	if err := deleteUserConfig(db); err != nil {
		t.Fatalf("deleteUserConfig 失败：%v", err)
	}

	for key := range userConfigKeyKinds {
		if _, ok := data.GetConfig(db, key); ok {
			t.Fatalf("标量键未清空：%s", key)
		}
	}
	for key := range userConfigFreeKeys {
		if _, ok := data.GetConfig(db, key); ok {
			t.Fatalf("自由键未清空：%s", key)
		}
	}
	keys, err := db.Table(tableLLMs).ListKeys()
	if err != nil {
		t.Fatalf("列 %s 失败：%v", tableLLMs, err)
	}
	if len(keys) != 0 {
		t.Fatalf("集合表未清空：%s → %v", tableLLMs, keys)
	}
	if v, ok := data.GetConfig(db, "unrelated_key"); !ok || v != "1" {
		t.Fatalf("同库无关键不应被清空：got=%q ok=%v", v, ok)
	}

	// 读回视图：标量回落系统默认、集合为空、自由键缺省
	view := readUserConfig(db)
	if view["theme"] != "light" || view["retryCount"] != 2 {
		t.Fatalf("清空后未回落系统默认：theme=%v retryCount=%v", view["theme"], view["retryCount"])
	}
	if arr, _ := view["llms"].([]map[string]any); len(arr) != 0 {
		t.Fatalf("llms 未清空：%v", view["llms"])
	}
	if _, ok := view["recent_dirs"]; ok {
		t.Fatal("自由键 recent_dirs 未清空")
	}
}

// TestExplicitUserConfigKeys list 视图 `explicit` 列（I-127 导出「保真」依据）：只列**用户显式写入**
// usr 主库的键——标量/自由键存在即显式（值等于系统默认也算显式），集合键非空才显式；
// 视图内被补系统默认的键（键不存在）不入列。字典序稳定。
func TestExplicitUserConfigKeys(t *testing.T) {
	db := openTestDB(t)

	// 空库：无显式键
	if got := explicitUserConfigKeys(db); len(got) != 0 {
		t.Fatalf("空库 explicit 应为空：%v", got)
	}

	// 显式写标量 + 自由键（defaultLLM/retryCount 等未写 → 不入列）
	if err := saveUserConfig(db, map[string]any{
		"theme":       "dark",
		"recent_dirs": `["D:\\proj"]`,
	}); err != nil {
		t.Fatalf("save 标量/自由键失败：%v", err)
	}
	if got := explicitUserConfigKeys(db); strings.Join(got, ",") != "recent_dirs,theme" {
		t.Fatalf("标量/自由键 explicit 不符：%v", got)
	}

	// 写集合 → llms 入列（defaultLLM 未显式写 → 仍不入列，尽管视图里被补了默认下标）
	if err := saveUserConfig(db, map[string]any{"llms": []any{map[string]any{"name": "a"}}}); err != nil {
		t.Fatalf("save llms 失败：%v", err)
	}
	if got := explicitUserConfigKeys(db); strings.Join(got, ",") != "llms,recent_dirs,theme" {
		t.Fatalf("集合 explicit 不符：%v", got)
	}
	view := readUserConfig(db)
	if _, has := view["defaultLLM"]; !has {
		t.Fatalf("视图应补 defaultLLM 默认值：%+v", view)
	}

	// 显式写 defaultLLM（值等于「默认选择的 name」也算显式）→ 入列
	if err := data.SetConfig(db, "defaultLLM", "a"); err != nil {
		t.Fatalf("写 defaultLLM 失败：%v", err)
	}
	if got := explicitUserConfigKeys(db); strings.Join(got, ",") != "defaultLLM,llms,recent_dirs,theme" {
		t.Fatalf("含 defaultLLM 的 explicit 不符：%v", got)
	}
}

// TestWriteCollectionReplace（A-22）：整体替换语义——旧行全清、新行按序号落库（单事务）；
// 非对象项跳过（不占序号键）。
func TestWriteCollectionReplace(t *testing.T) {
	db := openTestDB(t)

	if err := writeCollection(db, tableLLMs, []any{
		map[string]any{"name": "a", "apiKey": "k1"},
		map[string]any{"name": "b"},
	}); err != nil {
		t.Fatalf("write llms: %v", err)
	}
	got := readCollection(db, tableLLMs)
	if len(got) != 2 || got[0]["name"] != "a" || got[1]["name"] != "b" {
		t.Fatalf("首写集合不符：%v", got)
	}

	// 替换为 1 条 → 旧行（含 key "1"）不得残留
	if err := writeCollection(db, tableLLMs, []any{map[string]any{"name": "c"}}); err != nil {
		t.Fatalf("rewrite llms: %v", err)
	}
	keys, err := db.Table(tableLLMs).ListKeys()
	if err != nil || len(keys) != 1 || keys[0] != "0" {
		t.Fatalf("整体替换后应仅剩 key 0：keys=%v err=%v", keys, err)
	}
	if got := readCollection(db, tableLLMs); len(got) != 1 || got[0]["name"] != "c" {
		t.Fatalf("替换后集合不符：%v", got)
	}

	// 非对象项跳过（key "0" 缺位，key "1" 落库）
	if err := writeCollection(db, tableLLMs, []any{"junk", map[string]any{"name": "d"}}); err != nil {
		t.Fatalf("write with junk: %v", err)
	}
	if keys, err := db.Table(tableLLMs).ListKeys(); err != nil || len(keys) != 1 || keys[0] != "1" {
		t.Fatalf("非对象项应跳过：keys=%v err=%v", keys, err)
	}
}

// ── 记忆类别沉淀提示词：usr 自由键载体已删（OP-04，2026-10-06）──────────────

// TestMemoryPromptsFreeKeyRemoved：`memory_prompts`（原用户级记忆类别沉淀提示词自由键）已随
// OP-04 提示词**文件化**移除 → 不再是 usr 自由键（对应写入被静默丢弃，不入库）。
func TestMemoryPromptsFreeKeyRemoved(t *testing.T) {
	db := openTestDB(t)
	if userConfigFreeKeys["memory_prompts"] {
		t.Fatal("memory_prompts 应为已移除键（OP-04 记忆提示词文件化）")
	}
	if err := saveUserConfig(db, map[string]any{"memory_prompts": `{"用户偏好":"x"}`}); err != nil {
		t.Fatalf("save memory_prompts: %v", err)
	}
	if _, ok := data.GetConfig(db, "memory_prompts"); ok {
		t.Fatal("memory_prompts 不应再落库（键载体已删）")
	}
}

// ── SL-1：子系统默认 LLM 5 键（llm.<子系统>，2026-09-24）──────────────

// llmSubsystemKeys 是「子系统默认 LLM」5 键（40-演进计划 §SL SL-C2）——测试内独立硬编码，
// 兼作键名钉死（生产侧注册表见 userConfigKeyKinds）。
var llmSubsystemKeys = []string{
	"llm.promptOptimise",
	"llm.memory",
	"llm.compress",
	"llm.analysis",
	"llm.decision",
}

// TestSaveLLMSubsystemKeysRoundTrip ① 5 键已注册为 llmref + 写库/回读一致（显式配置不回落到 defaultLLM）。
func TestSaveLLMSubsystemKeysRoundTrip(t *testing.T) {
	db := openTestDB(t)
	// 建一个可用 LLM 并把 defaultLLM 指到它：用于验证「显式配置的子系统键不回落到 defaultLLM」
	if err := saveUserConfig(db, map[string]any{
		"llms":       []any{map[string]any{"name": "gpt-local"}},
		"defaultLLM": "gpt-local",
	}); err != nil {
		t.Fatalf("铺 llms/defaultLLM 失败：%v", err)
	}

	want := map[string]string{
		"llm.promptOptimise": "p-opt",
		"llm.memory":         "p-mem",
		"llm.compress":       "p-cmp",
		"llm.analysis":       "p-ana",
		"llm.decision":       "p-dec",
	}
	payload := map[string]any{}
	for k, v := range want {
		if kind, ok := userConfigKeyKinds[k]; !ok || kind != "llmref" {
			t.Fatalf("%s 未按 llmref 注册：kind=%q ok=%v", k, kind, ok)
		}
		payload[k] = v
	}
	if err := saveUserConfig(db, payload); err != nil {
		t.Fatalf("save 子系统键失败：%v", err)
	}

	view := readUserConfig(db)
	for _, k := range llmSubsystemKeys {
		if raw, ok := data.GetConfig(db, k); !ok || raw != want[k] {
			t.Fatalf("%s 未落库：got=%q ok=%v", k, raw, ok)
		}
		if got := view[k]; got != want[k] {
			t.Fatalf("%s 回读不一致（不得回落 defaultLLM）：got=%v want=%q", k, got, want[k])
		}
	}
}

// TestLLMSubsystemKeysFallbackToDefaultLLM ② 键缺失 / 空串 → 回落全局 defaultLLM（SL-C4）：
// 无 LLM → 回落 defaultLLM 的 -1；defaultLLM = name → 回落该 name；defaultLLM = 旧 int 索引 → 回落该 int。
func TestLLMSubsystemKeysFallbackToDefaultLLM(t *testing.T) {
	db := openTestDB(t)

	// ① 无 llms、defaultLLM 键缺失 → defaultLLM 系统默认 -1，子系统键同值
	view := readUserConfig(db)
	for _, k := range llmSubsystemKeys {
		if got, ok := view[k]; !ok || got != -1 {
			t.Fatalf("无 LLM 时 %s 未回落 -1：got=%v ok=%v", k, got, ok)
		}
	}

	// ② defaultLLM = name 字符串（新形态）：**键缺失**（不写任何子系统键）→ 回落该 name
	if err := data.SetConfig(db, "defaultLLM", "deepseek-flash"); err != nil {
		t.Fatalf("写 defaultLLM 失败：%v", err)
	}
	// 同时显式写一个**空串**（键存在）→ 亦回落 defaultLLM（与键缺失同语义）
	if err := saveUserConfig(db, map[string]any{"llm.compress": ""}); err != nil {
		t.Fatalf("写空串 llm.compress 失败：%v", err)
	}
	if _, ok := data.GetConfig(db, "llm.compress"); !ok {
		t.Fatal("空串 llm.compress 应已落库（键存在 ≠ 键缺失）")
	}
	view = readUserConfig(db)
	for _, k := range llmSubsystemKeys {
		if got := view[k]; got != "deepseek-flash" {
			t.Fatalf("defaultLLM=name 时 %s 未回落：got=%v", k, got)
		}
	}

	// ③ defaultLLM = 旧 int 索引（存储为整数文本）→ 子系统键回落同形态 int
	if err := data.SetConfig(db, "defaultLLM", "2"); err != nil {
		t.Fatalf("写 defaultLLM(int 形态) 失败：%v", err)
	}
	view = readUserConfig(db)
	for _, k := range llmSubsystemKeys {
		if got := view[k]; got != 2 {
			t.Fatalf("defaultLLM=int 形态时 %s 未回落 int 2：got=%v", k, got)
		}
	}
}

// TestLLMSubsystemKeysLegacyIntCompat ③ 读侧兼容旧 int 索引形态（SL-C3，同 defaultLLM）：
// 数字入参 → 落库为整数文本 → 回读为 int 索引，**不被回落**成 defaultLLM。
func TestLLMSubsystemKeysLegacyIntCompat(t *testing.T) {
	db := openTestDB(t)
	_ = data.SetConfig(db, "defaultLLM", "deepseek-flash")

	// 旧前端形态：JSON number（float64）入参
	if err := saveUserConfig(db, map[string]any{"llm.memory": float64(2)}); err != nil {
		t.Fatalf("写 llm.memory(int 形态) 失败：%v", err)
	}
	if raw, _ := data.GetConfig(db, "llm.memory"); raw != "2" {
		t.Fatalf("int 形态未按整数格式化落库：got=%q", raw)
	}
	// 旧记录形态：库里直接是整数文本
	if err := data.SetConfig(db, "llm.analysis", "1"); err != nil {
		t.Fatalf("写 llm.analysis(int 文本) 失败：%v", err)
	}

	view := readUserConfig(db)
	if got := view["llm.memory"]; got != 2 {
		t.Fatalf("llm.memory 旧 int 索引未原样回读：got=%v", got)
	}
	if got := view["llm.analysis"]; got != 1 {
		t.Fatalf("llm.analysis 旧 int 索引未原样回读：got=%v", got)
	}
	// 同批未配置键仍回落 defaultLLM（互不影响）
	if got := view["llm.compress"]; got != "deepseek-flash" {
		t.Fatalf("未配置的 llm.compress 未回落 defaultLLM：got=%v", got)
	}
}

// TestDeleteLLMSubsystemKeysFallback ④ 删除：单键删除 → 该键回落 defaultLLM（其余键不受影响）；
// 整份清空（deleteUserConfig）→ 5 键全清、回落 defaultLLM 系统默认。
func TestDeleteLLMSubsystemKeysFallback(t *testing.T) {
	db := openTestDB(t)
	if err := data.SetConfig(db, "defaultLLM", "deepseek-flash"); err != nil {
		t.Fatalf("写 defaultLLM 失败：%v", err)
	}
	if err := saveUserConfig(db, map[string]any{
		"llm.promptOptimise": "p-opt",
		"llm.memory":         "p-mem",
	}); err != nil {
		t.Fatalf("save 子系统键失败：%v", err)
	}

	// 单键删除 → 仅该键回落，其余键保持显式值
	if err := data.DeleteConfig(db, "llm.memory"); err != nil {
		t.Fatalf("删 llm.memory 失败：%v", err)
	}
	view := readUserConfig(db)
	if got := view["llm.memory"]; got != "deepseek-flash" {
		t.Fatalf("删键后未回落 defaultLLM：got=%v", got)
	}
	if got := view["llm.promptOptimise"]; got != "p-opt" {
		t.Fatalf("未删键被波及：got=%v", got)
	}

	// 整份清空 → 5 键 + defaultLLM 全清 → 回落系统默认（-1）
	if err := deleteUserConfig(db); err != nil {
		t.Fatalf("deleteUserConfig 失败：%v", err)
	}
	for _, k := range llmSubsystemKeys {
		if _, ok := data.GetConfig(db, k); ok {
			t.Fatalf("整份清空后键未清除：%s", k)
		}
	}
	view = readUserConfig(db)
	for _, k := range llmSubsystemKeys {
		if got := view[k]; got != -1 {
			t.Fatalf("整份清空后 %s 未回落系统默认 -1：got=%v", k, got)
		}
	}
}
