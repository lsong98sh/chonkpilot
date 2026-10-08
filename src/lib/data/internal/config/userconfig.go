// 用户配置域（data-user-config-*）v6 存储实现（12-数据层）：
//
//   - 标量项 → usr config 表**一个决策一个 key**（值 {"v": <字符串>}）
//   - 多记录集合 llms → usr 专用表（脱离整块 JSON）
//
// 对外**消息契约不变**：load 返回合并对象 {data: {...}}、save 收增量对象（双方仍是同一
// 形态，61-消息一览 无需变更）；变化只在存储侧（原 user_config 单 key 整块 JSON）。
//
// 阶段 4「internal 下沉」：本文件由 `chonkpilot-data/persist` 整体下移（逻辑逐字未改；
// MQ 信封 handleUserConfig 留在 persist）。
package config

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// 标量配置项 → 值类型（读回时按类型还原；前端消费 int 的字段不能回成字符串）。
// 类型取值：string / int / llmref（LLM 选择引用：defaultLLM + 子系统默认 LLM）。
var userConfigKeyKinds = map[string]string{
	"theme":           "string",
	"locale":          "string",
	"chromePath":      "string",
	"javaPath":        "string",
	"pythonPath":      "string",
	"nodePath":        "string",
	"goPath":          "string",
	"rustPath":        "string",
	"cCompilerPath":   "string",
	"responseTimeout": "int",
	"streamTimeout":   "int",
	"retryCount":      "int",
	// defaultLLM（llmref，2026-09-15 改）：值 = LLM 选择引用 —— **name 字符串**（usr llms 记录名，
	// 或内置 provider 名如 echo；空串 = 显式「系统默认（启动参数）」= 不指定 provider → 回落 exe
	// 启动参数）。读侧兼容旧记录：值能解析为整数时按**旧 int 索引**（llms[v]）原样回读，消费点
	// 两种形态都认（见 64-配置项一览 §3）。改用 name 是为了修掉「llms 增删导致索引移位」。
	"defaultLLM":      "llmref",
	"defaultScenario": "string",

	// 子系统默认 LLM（SL-1，2026-09-24 · 40-演进计划 §SL）：值 = llmref（同 defaultLLM），
	// **仅 usr**（SL-C1 —— 不入 inheritableConfigKeys）；语义 = 该子系统自己的默认模型，
	// **空串 / 键缺失 → 回落全局 defaultLLM**（SL-C3/C4，回落点见 readUserConfig）；读侧同样
	// 兼容旧 int 索引。键名与消费方见 64-配置项一览 §3；分析 / 决策为**备用**（消费方未实现，
	// 仅登记预留位 —— SL-C6）。
	"llm.promptOptimise": "llmref",
	"llm.memory":         "llmref",
	"llm.compress":       "llmref",
	"llm.analysis":       "llmref",
	"llm.decision":       "llmref",
}

// 多记录集合：usr 专用表。
const tableLLMs = "llms"

// collectionKeys 是走专用表的集合字段（save 载荷里的名字）。
var collectionKeys = map[string]string{
	"llms": tableLLMs,
}

// userConfigFreeKeys 是 usr config 表**自由键**通道的已注册键（G-05 / P2-7）：无类型、
// 无系统默认，值以字符串形态（JSON 原样）存取——承载既非 typed 用户配置（theme/超时/
// toolchain 路径等）也非集合（llms）的用户级薄数据。读写复用既有
// data-user-config-{load,save,delete}（消息契约不变）。
var userConfigFreeKeys = map[string]bool{
	// recent_dirs = 最近打开的项目目录（JSON 数组字符串；gui.recent.list 数据源）。
	"recent_dirs": true,
	// tool_async = 工具级异步配置（**结构化 JSON 对象字符串**，形状
	// {"<工具暴露名>":{"mode":"always|never|auto|manual","threshold":30,"hard_timeout":300}}；
	// 消费方见 64-配置项一览 §3：llm server loadExecConfig → mcp-server Config.SetToolAsync
	// → 暴露 _meta.async / async-threshold + executor 硬上限；**保存即生效**（既有
	// data-user-config-refresh 订阅，零新增主题）。「恢复默认」= 删该工具键项（整表覆盖写）。
	"tool_async": true,
	// tool_sandbox = **executor 级沙箱开关**（agentbox，**结构化 JSON 对象字符串**，形状
	// {"core":true|false,"desktop":true|false,"browser":true|false}；key = executor 类别
	// = 契约 `_meta.category`。消费方见 64-配置项一览 §3：llm server loadExecConfig →
	// gateway SetSandboxOverrides → mcp-server Config.SetToolSandbox → callTool 按
	// `td.Category` 查开关 → 开启时 spawn executor 注入 CHONKPILOT_SANDBOX
	// （允许目录 = prj `security-*`）；**保存即生效**（既有 data-user-config-refresh 订阅，
	// 零新增主题）。**未配置 = 不隔离**（默认兼容）。**旧形态（按工具暴露名）不再生效**。
	// 「恢复未设置」= 删该 executor 键项（整表覆盖写 / 末项删整键）。
	"tool_sandbox": true,
	// 注（OP-04，2026-10-06）：原 `memory_prompts`（用户级记忆类别沉淀提示词）自由键**已移除**
	// —— 记忆类别提示词改**文件化**（`capability/system/memory/<类别名>.md`，走 prompt 域
	// `memory_prompt.<类别名>` 键；读序 项目→用户→系统→embed），键载体不再存在。
}

// UserConfigID 是 user-config 域的整体记录 id（`data-user-config-save` 应答键 `id` 与
// `data-user-config-refresh` 广播的 `id`；仅作不透明标识，前端不解析其值）。
const UserConfigID = "user_config"

// inheritableConfigKeys 是 user-config 域中**可被项目层覆盖**的键及其值类型（02-配置层级
// §3/§5）：读序 prjusr → prj → usr（本函数以 usr 视图为基线，故只需叠加项目层两级）。
// 仅 defaultLLM / defaultScenario —— 即"**选哪个**"才可继承；llms 集合（专用表）、
// 凭据、theme/locale、chromePath 与超时重试四项按 §5 保持 usr 主库语义，不入列。
var inheritableConfigKeys = map[string]string{
	"defaultLLM":      "llmref", // 同 userConfigKeyKinds：能解析为 int 的按 int 还原（旧记录语义）
	"defaultScenario": "string",
}

// applyProjectOverrides 在 usr 基线视图上叠加项目层（prjusr → prj）同名可继承键：整值覆盖；
// 项目层未设该键 → 保持 usr 值（= 继承）；两级均为空串视为未设。实例不可解析（usr 全局调用，
// 如无 instance_id 的测试/CLI）→ 原样返回。值按 key 类型还原，与 readUserConfig 口径一致。
// scope 供跨进程/独立形态带入实例数据根（MQ 路径留空 = 按本进程实例视图解析）。
func (s *Service) applyProjectOverrides(view map[string]any, instanceID string, scope facade.Scope) map[string]any {
	prj, pudb, release, err := s.OpenLayerDBs(instanceID, scope)
	if err != nil {
		return view
	}
	defer release()
	puVals := cachedConfigValues(pudb)  // 进程内值缓存（valuecache.go，D-45）
	prjVals := cachedConfigValues(prj)
	for key, kind := range inheritableConfigKeys {
		raw := ""
		if v, ok := puVals[key]; ok && v != "" {
			raw = v // prjusr（本机本项目）优先
		} else if v, ok := prjVals[key]; ok && v != "" {
			raw = v // prj（团队预设）
		} else {
			continue // 两级均未设 → 保持 usr 基线（继承）
		}
		if kind == "int" || kind == "llmref" {
			if n, err := strconv.Atoi(raw); err == nil {
				view[key] = n
				continue
			}
		}
		view[key] = raw
	}
	return view
}

// userConfigSystemDefaults 是系统级默认（fallback 终点 = 资源/常量，不落库；
// 12-数据层）。usr 无该 key 时由这里补默认值，保证前端消费字段完整。
// 注：defaultLLM 的默认值依赖 llms 集合（第一个可用 / 无则 -1），在 readUserConfig 内计算。
// 超时/重试三项与旧硬编码 / llm server 回落常量一致（responseTimeout=120s、streamTimeout=60s、
// retryCount=2）；retryCount 按**存在性**判定——显式 0 = 不重试（P0-A，缺键才回落本默认 2）；
// 另两项 0/缺省 = 用默认（非正值不下发）。**退避间隔不在此**（经 router.RetryWait 计算，2026-10-05）。
var userConfigSystemDefaults = map[string]any{
	// theme 缺省 = light：与前端唯一默认主题一致（`Toolbar.vue` currentTheme 初值 + `:root` token）。
	// 原值 "system"（2026-09-16 订正）无实现 —— 无 `[data-theme="system"]` 规则、前端可选集仅
	// light/dark/nord，写 system 只会回落 `:root` 浅色，故常量直接对齐 `light`。
	"theme":           "light",
	"locale":          "zh-CN",
	"chromePath":      "",
	"javaPath":        "",
	"pythonPath":      "",
	"nodePath":        "",
	"goPath":          "",
	"rustPath":        "",
	"cCompilerPath":   "",
	"responseTimeout": 120,
	"streamTimeout":   60,
	"retryCount":      2,
	"defaultScenario": "",
}

// readUserConfig 组装用户配置对象：逐 key 标量（缺失补系统默认）+ 专用表集合。
// 标量/自由键读经进程内值缓存（valuecache.go，D-45）；llms 集合表不缓存（低频、整体替换写）。
func readUserConfig(db *data.DB) map[string]any {
	vals := cachedConfigValues(db)
	llms := readCollection(db, tableLLMs)
	out := map[string]any{
		"llms": llms,
	}
	for key, kind := range userConfigKeyKinds {
		if v, ok := vals[key]; ok {
			switch kind {
			case "int":
				if n, err := strconv.Atoi(v); err == nil {
					out[key] = n
					continue
				}
			case "llmref":
				// LLM 选择（2026-09-15）：能解析为整数 → **旧记录 int 索引**（兼容，原样回读为 int）；
				// 否则按 name 字符串回读（含空串 = 显式「系统默认（启动参数）」，与键缺失可区分）。
				if n, err := strconv.Atoi(v); err == nil {
					out[key] = n
					continue
				}
				out[key] = v
				continue
			default:
				out[key] = v
				continue
			}
		}
		if def, ok := userConfigSystemDefaults[key]; ok {
			out[key] = def
		}
	}
	// 自由键：无类型/无默认，存在即以字符串原样回读（G-05 / P2-7）。
	for key := range userConfigFreeKeys {
		if v, ok := vals[key]; ok {
			out[key] = v
		}
	}
	// defaultLLM 缺省规则（仅**键缺失**时生效）：未显式配置 → 第一个可用 LLM 的**下标 0**（旧 int
	// 形态，消费点按 llms[0] 解析）；无任何可用 LLM → -1（未配置）。显式配置（键存在）一律原样
	// 回读：name 字符串（新）/ int 索引（旧记录）/ 空串（显式「系统默认（启动参数）」）。
	if _, ok := out["defaultLLM"]; !ok {
		if len(llms) > 0 {
			out["defaultLLM"] = 0
		} else {
			out["defaultLLM"] = -1
		}
	}
	// 子系统默认 LLM 缺省规则（SL-1，2026-09-24 · SL-C3/C4）：值同为 llmref 的**子系统键**
	// （全局 defaultLLM 除外）——键**缺失或空串** → 回落上面已定值的 defaultLLM（name 字符串 /
	// 旧 int 索引 / -1）；**显式配置**（非空 name 字符串，或旧记录 int 索引）原样回读。
	for key, kind := range userConfigKeyKinds {
		if kind != "llmref" || key == "defaultLLM" {
			continue
		}
		if s, isStr := out[key].(string); isStr && s != "" {
			continue // 显式配置的 provider name
		}
		if _, isInt := out[key].(int); isInt {
			continue // 旧记录 int 索引
		}
		out[key] = out["defaultLLM"]
	}
	return out
}

// userConfigList 域列表：有任一配置（标量 key 或集合非空）→ 单条整体对象；否则空列表。
func (s *Service) userConfigList(db *data.DB) []map[string]any {
	view := readUserConfig(db)
	hasData := false
	if arr, ok := view["llms"].([]map[string]any); ok && len(arr) > 0 {
		hasData = true
	}
	if !hasData {
		vals := cachedConfigValues(db) // 进程内值缓存（valuecache.go，D-45）
		for key := range userConfigKeyKinds {
			if _, ok := vals[key]; ok {
				hasData = true
				break
			}
		}
	}
	if !hasData {
		vals := cachedConfigValues(db)
		for key := range userConfigFreeKeys {
			if _, ok := vals[key]; ok {
				hasData = true
				break
			}
		}
	}
	if !hasData {
		return []map[string]any{}
	}
	view["id"] = "user-config"
	// explicit = 用户**显式写入** usr 主库的配置键（I-127 导出「保真」依据：导出只写显式键，
	// 避免把系统默认填充（如 defaultLLM 的数值下标）固化为用户配置）。
	view["explicit"] = explicitUserConfigKeys(db)
	return []map[string]any{view}
}

// explicitUserConfigKeys 返回用户**显式写入** usr 主库的配置键（字典序稳定）：
//   - 标量键 / 自由键：配置表存在该键即显式（键存在 = 用户写过，即使值等于系统默认）；
//   - 集合键：专用表非空即显式（空表 = 无可导出的本体）。
//
// 仅供 data-user-config-list 的 `explicit` 字段使用；list 视图内被补系统默认的键不在此列。
func explicitUserConfigKeys(db *data.DB) []string {
	vals := cachedConfigValues(db) // 进程内值缓存（valuecache.go，D-45）
	out := make([]string, 0, len(userConfigKeyKinds)+len(userConfigFreeKeys)+len(collectionKeys))
	for key := range userConfigKeyKinds {
		if _, ok := vals[key]; ok {
			out = append(out, key)
		}
	}
	for key := range userConfigFreeKeys {
		if _, ok := vals[key]; ok {
			out = append(out, key)
		}
	}
	for key, table := range collectionKeys {
		if keys, err := db.Table(table).ListKeys(); err == nil && len(keys) > 0 {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// saveUserConfig 增量保存：标量写各自 key；集合整体替换专用表（载荷里出现的集合才写）。
func saveUserConfig(db *data.DB, payload map[string]any) error {
	for k, v := range payload {
		if k == "id" {
			continue
		}
		if table, isCollection := collectionKeys[k]; isCollection {
			items, _ := v.([]any)
			if err := writeCollection(db, table, items); err != nil {
				return err
			}
			continue
		}
		if kind, known := userConfigKeyKinds[k]; known {
			if err := data.SetConfig(db, k, scalarString(v, kind)); err != nil {
				return err
			}
			continue
		}
		if userConfigFreeKeys[k] {
			if err := data.SetConfig(db, k, scalarString(v, "")); err != nil {
				return err
			}
		}
	}
	return nil
}

// deleteUserConfig 清空用户配置（标量 key + 自由键 + 集合表）→ 全部回落默认/继承。
func deleteUserConfig(db *data.DB) error {
	for key := range userConfigKeyKinds {
		if err := data.DeleteConfig(db, key); err != nil {
			return err
		}
	}
	for key := range userConfigFreeKeys {
		if err := data.DeleteConfig(db, key); err != nil {
			return err
		}
	}
	if err := clearCollection(db, tableLLMs); err != nil {
		return err
	}
	return nil
}

// clearCollection 清空集合表全部记录（整体替换写入前 / 单 key 删除）。
func clearCollection(db *data.DB, table string) error {
	keys, err := db.Table(table).ListKeys()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := db.Table(table).Delete(k); err != nil {
			return err
		}
	}
	return nil
}

// scalarString 把载荷标量转存储字符串（int/llmref 走整型格式化，字符串原样）。
// 注：llmref 的字符串值原样落库（含空串 = 显式「系统默认（启动参数）」）；数字值（旧前端/旧记录
// 形态的 int 索引）按整数格式化 → 读回时仍按 int 索引解析。
func scalarString(v any, kind string) string {
	if s, ok := v.(string); ok {
		return s
	}
	if kind == "int" || kind == "llmref" {
		if f, ok := v.(float64); ok { // JSON number → float64
			return strconv.Itoa(int(f))
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// ── 专用表（llms）读写 ─────────────────────────────

// readCollection 读集合表 → 有序数组（按桶主键数值序，保持前端数组顺序）。
func readCollection(db *data.DB, table string) []map[string]any {
	keys, err := db.Table(table).ListKeys()
	if err != nil || len(keys) == 0 {
		return []map[string]any{}
	}
	sort.Slice(keys, func(i, j int) bool { return ordOfKey(keys[i]) < ordOfKey(keys[j]) })
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		var rec data.Record
		if ok, _ := db.Table(table).Get(k, &rec); !ok {
			continue
		}
		item := kernel.RecordView(rec, "")
		delete(item, "ord")
		out = append(out, item)
	}
	return out
}

// writeCollection 整体替换集合表（载荷数组 → 逐条记录，主键 = 序号，保持数组顺序）。
func writeCollection(db *data.DB, table string, items []any) error {
	if err := clearCollection(db, table); err != nil {
		return err
	}
	for i, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rec := data.Record{}
		for k, v := range item {
			if k == "id" || k == "ord" {
				continue // 主键由序号承担，避免旧 id 干扰顺序
			}
			rec[k] = v
		}
		rec["ord"] = i
		if err := db.Table(table).Upsert(strconv.Itoa(i), rec); err != nil {
			return err
		}
	}
	return nil
}

// ordOfKey 解析集合主键序号（非数字键排最后）。
func ordOfKey(k string) int {
	n, err := strconv.Atoi(k)
	if err != nil {
		return 1 << 30
	}
	return n
}
