// genmsg —— 消息面键常量生成器（B 方案：以 schema 为唯一源）。
//
// 读 `docs/spec/60-reference/61-messages.schema.json`（= 61-消息一览 的机器可读抽取），
// 生成两侧键/主题常量，消除"同一事实写三遍"（Go 字面量 / JS 读取 / 文档）：
//
//   - Go：src/lib/core/msgkeys/msgkeys_gen.go（package msgkeys）
//   - JS：src/frontend/src/events/msgkeys.js
//
// 生成物带"由 genmsg 生成，勿手改"头注释；漂移由 gen_test.go 逐字比对兜底。
// 手动生成：go run src/tools/genmsg（或 ./src/tools/genmsg 内 go run .）。
package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strings"
)

// fieldSpec 契约字段（与 61-messages.schema.json 对齐）。
type fieldSpec struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// topicSpec 契约主题（payload=请求字段；result=应答字段；event=下行事件载荷字段）。
type topicSpec struct {
	Topic       string               `json:"topic"`
	Face        string               `json:"face"`
	Direction   string               `json:"direction"`
	BusTopic    string               `json:"busTopic"`
	ClientTopic string               `json:"clientTopic"`
	Payload     map[string]fieldSpec `json:"payload"`
	Result      map[string]fieldSpec `json:"result"`
	Event       map[string]fieldSpec `json:"event"`
}

type schemaDoc struct {
	Version         string               `json:"version"`
	Topics          []topicSpec          `json:"topics"`
	FieldDictionary map[string]dictEntry `json:"fieldDictionary"`
}

// dictExc 是字段字典中「允许的同语义别名」条目（仅限 where 主题，来自 61 §0.5 历史例外）。
type dictExc struct {
	Name   string `json:"name"`
	Where  string `json:"where"`
	Reason string `json:"reason"`
}

// dictEntry 是字段语义字典条目：键 = 语义键；name = 规范字段名（缺省 = 键）；
// aliases = 禁用别名（出现即报错）；exceptions = 允许的同语义别名（仅限 where 主题）。
type dictEntry struct {
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Desc       string    `json:"desc"`
	Aliases    []string  `json:"aliases"`
	Exceptions []dictExc `json:"exceptions"`
}

// Outputs 是两侧生成物（逐字写入或比对）。
type Outputs struct {
	Go string
	JS string
}

// side 表示字段所属侧（决定 Go 常量名中段）。
type side struct {
	word  string // Payload / Result / Event
	wordJ string // payload/result/event
}

var sides = []side{
	{"Payload", "payload"},
	{"Result", "result"},
	{"Event", "event"},
}

func fieldsOf(ts topicSpec, s side) map[string]fieldSpec {
	switch s.word {
	case "Payload":
		return ts.Payload
	case "Result":
		return ts.Result
	default:
		return ts.Event
	}
}

// Generate 依据 schema 原文生成 Go / JS 两侧源码。
func Generate(schemaRaw []byte) (Outputs, error) {
	var doc schemaDoc
	if err := json.Unmarshal(schemaRaw, &doc); err != nil {
		return Outputs{}, fmt.Errorf("解析 schema: %w", err)
	}
	if len(doc.Topics) == 0 {
		return Outputs{}, fmt.Errorf("schema 未收录任何主题")
	}
	if err := validateDictionary(doc); err != nil {
		return Outputs{}, err
	}
	goSrc, err := genGo(doc)
	if err != nil {
		return Outputs{}, err
	}
	jsSrc, err := genJS(doc)
	if err != nil {
		return Outputs{}, err
	}
	return Outputs{Go: goSrc, JS: jsSrc}, nil
}

// validateDictionary 校验 fieldDictionary 与各主题字段的一致性（硬要求「同语义命名唯一」）。
//
// 规则（任一违反 → 返回 error，生成/ -check 即失败）：
//
//	R1 每个语义键的规范名（name，缺省 = 键）全局唯一；不得与任何别名/例外名撞名。
//	R2 别名（aliases）全局唯一注册；主题字段命中别名 → 报错（须改用规范名）。
//	R3 例外名（exceptions[].name）仅允许出现在指定的 where 主题；越位 → 报错。
//	R4 主题字段名必须能在字典中解析（规范名或例外名）；未登记 → 报错。
//	R5 主题字段 type 须等于字典 type（字典 type = "any" 时豁免）。
//
// 「同一语义出现两个不同名字」由 R2/R3 直接拦截：同一语义的非规范写法只能是别名（R2 拦）或
// 已登记例外（R3 拦且限主题），不存在第二条路径，故无法并存两个不同的有效名。
func validateDictionary(doc schemaDoc) error {
	if len(doc.FieldDictionary) == 0 {
		return fmt.Errorf("fieldDictionary 为空（应为字段语义唯一源）")
	}
	canonical := map[string]string{} // 规范名 -> 语义键
	aliasTo := map[string]string{}   // 别名 -> 语义键
	entryByName := map[string]dictEntry{}
	excWhere := map[string]string{} // 例外名 -> where 主题
	for sem, e := range doc.FieldDictionary {
		if strings.HasPrefix(sem, "_") { // 备注键（如 _doc）
			continue
		}
		name := e.Name
		if name == "" {
			name = sem
		}
		if prev, ok := canonical[name]; ok {
			return fmt.Errorf("字段规范名重复: %q（语义 %s 与 %s）", name, prev, sem)
		}
		canonical[name] = sem
		ent := e
		ent.Name = name
		entryByName[name] = ent
		for _, a := range e.Aliases {
			if _, ok := canonical[a]; ok {
				return fmt.Errorf("字段别名 %q 与规范名冲突（语义 %s）", a, sem)
			}
			if prev, ok := aliasTo[a]; ok {
				return fmt.Errorf("字段别名 %q 重复登记（语义 %s 与 %s）", a, prev, sem)
			}
			aliasTo[a] = sem
		}
		for _, ex := range e.Exceptions {
			if ex.Where == "" || ex.Reason == "" {
				return fmt.Errorf("字段例外 %q（语义 %s）缺 where/reason", ex.Name, sem)
			}
			if _, ok := canonical[ex.Name]; ok {
				return fmt.Errorf("字段例外名 %q 与规范名冲突（语义 %s）", ex.Name, sem)
			}
			if _, ok := aliasTo[ex.Name]; ok {
				return fmt.Errorf("字段例外名 %q 与别名冲突（语义 %s）", ex.Name, sem)
			}
			if prev, ok := excWhere[ex.Name]; ok {
				return fmt.Errorf("字段例外名 %q 重复登记（%s 与 %s）", ex.Name, prev, ex.Where)
			}
			excWhere[ex.Name] = ex.Where
			ent := e
			ent.Name = ex.Name
			entryByName[ex.Name] = ent
		}
	}

	for _, ts := range doc.Topics {
		for _, s := range sides {
			for k, fs := range fieldsOf(ts, s) {
				ctx := fmt.Sprintf("主题 %s · %s · 字段 %q", ts.Topic, s.wordJ, k)
				if sem, ok := aliasTo[k]; ok {
					return fmt.Errorf("%s 命中禁用别名（语义 %s）—— 应改用规范名", ctx, sem)
				}
				if where, ok := excWhere[k]; ok {
					if where != ts.Topic {
						return fmt.Errorf("%s 是字典例外（仅限 %s）—— 越位使用", ctx, where)
					}
					continue // 例外名类型随其自身语义，豁免类型比对
				}
				ent, ok := entryByName[k]
				if !ok {
					return fmt.Errorf("%s 未登记于 fieldDictionary —— 请先登记语义", ctx)
				}
				if ent.Type != "" && ent.Type != "any" && fs.Type != ent.Type {
					return fmt.Errorf("%s 类型不符：字典=%s，主题=%s", ctx, ent.Type, fs.Type)
				}
			}
		}
	}
	return nil
}

// ── Go 侧 ──────────────────────────────────────────────────────────────

func genGo(doc schemaDoc) (string, error) {
	var b strings.Builder
	b.WriteString("// Code generated by genmsg. DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// 契约唯一源：docs/spec/60-reference/61-messages.schema.json\n")
	b.WriteString("//（= docs/spec/60-reference/61-消息一览.md，消息面唯一准则）。\n")
	b.WriteString("// 重新生成：go run src/tools/genmsg\n\n")
	b.WriteString("package msgkeys\n\n")

	// 主题常量（Schema 顺序，1:1）。
	b.WriteString("// ── 主题常量（61 topic，逐条镜像）──\n")
	b.WriteString("const (\n")
	seenTopic := map[string]string{}
	for _, ts := range doc.Topics {
		name := "Topic" + ident(ts.Topic)
		if prev, ok := seenTopic[name]; ok {
			return "", fmt.Errorf("topic 常量名冲突: %s（%s vs %s）", name, prev, ts.Topic)
		}
		seenTopic[name] = ts.Topic
		fmt.Fprintf(&b, "\t%s = %q\n", name, ts.Topic)
	}
	b.WriteString(")\n\n")

	// 客户端 topic 常量（schema clientTopic：前端 type ↔ 总线相对主题 的键；JS 侧 = MsgClientTopics）。
	b.WriteString("// ── 客户端 topic 常量（schema clientTopic；前端 type ↔ 总线相对主题 的键；JS 侧 = MsgClientTopics）──\n")
	b.WriteString("const (\n")
	seenClientTopic := map[string]string{}
	for _, ts := range doc.Topics {
		if ts.ClientTopic == "" {
			continue
		}
		name := "MsgClientTopics" + ident(ts.ClientTopic)
		if prev, ok := seenClientTopic[name]; ok {
			return "", fmt.Errorf("客户端 topic 常量名冲突: %s（%s vs %s）", name, prev, ts.ClientTopic)
		}
		seenClientTopic[name] = ts.ClientTopic
		fmt.Fprintf(&b, "\t%s = %q\n", name, ts.ClientTopic)
	}
	b.WriteString(")\n\n")

	// 键常量（按主题/侧分组；命名 = <主题><侧><键>）。
	b.WriteString("// ── 键常量（按主题/侧分组；命名 = <主题><侧><键>）──\n")
	b.WriteString("const (\n")
	seenKey := map[string]string{}
	emittedAny := false
	for _, ts := range doc.Topics {
		topicIdent := ident(ts.Topic)
		for _, s := range sides {
			keys := sortedKeys(fieldsOf(ts, s))
			if len(keys) == 0 {
				continue
			}
			if emittedAny {
				b.WriteString("\n")
			}
			emittedAny = true
			fmt.Fprintf(&b, "\t// %s · %s\n", ts.Topic, s.wordJ)
			for _, k := range keys {
				name := topicIdent + s.word + ident(k)
				if prev, ok := seenKey[name]; ok {
					return "", fmt.Errorf("键常量名冲突: %s（%s vs %s）", name, prev, k)
				}
				seenKey[name] = k
				fmt.Fprintf(&b, "\t%s = %q\n", name, k)
			}
		}
	}
	b.WriteString(")\n")

	// 通用字段常量（全部字段名并集；命名 = Field<键>；镜像 JS FieldKeys）。
	// 供跨主题/信封（ok/error/result…）等「非某主题专属」处引用，避免字符串字面量。
	allSet := map[string]bool{}
	for _, ts := range doc.Topics {
		for _, s := range sides {
			for k := range fieldsOf(ts, s) {
				allSet[k] = true
			}
		}
	}
	b.WriteString("\n// ── 通用字段常量（全部字段名并集，跨主题共用；镜像 JS FieldKeys）──\n")
	b.WriteString("const (\n")
	// 同名转 PascalCase 冲突（如 openedFiles/opened_files、workDir/work_dir）→ 优先规范 snake_case；
	// 被让位的 camelCase 历史例外仍可用其主题专属常量（如 GuiInitDataResultOpenedFiles）。
	resolved := map[string]string{}
	for _, k := range sortedKeysSet(allSet) {
		name := "Field" + ident(k)
		if prev, ok := resolved[name]; ok {
			if hasUpper(k) && !hasUpper(prev) {
				continue
			}
			if !hasUpper(k) && hasUpper(prev) {
				resolved[name] = k
				continue
			}
			return "", fmt.Errorf("字段常量名冲突: %s（%s vs %s）", name, prev, k)
		}
		resolved[name] = k
	}
	for _, name := range sortedKeysSet(mapKeys(resolved)) {
		fmt.Fprintf(&b, "\t%s = %q\n", name, resolved[name])
	}
	b.WriteString(")\n")

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return "", fmt.Errorf("格式化 Go 生成物: %w", err)
	}
	return string(formatted), nil
}

// ── JS 侧 ──────────────────────────────────────────────────────────────

func genJS(doc schemaDoc) (string, error) {
	var b strings.Builder
	b.WriteString("// 由 genmsg 生成，勿手改。DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// 契约唯一源：docs/spec/60-reference/61-messages.schema.json\n")
	b.WriteString("//（= docs/spec/60-reference/61-消息一览.md，消息面唯一准则）。\n")
	b.WriteString("// 重新生成：go run src/tools/genmsg\n\n")

	// MsgTopics：相对主题名。
	b.WriteString("/** 相对主题名（61 topic，逐条镜像）。 */\n")
	b.WriteString("export const MsgTopics = {\n")
	seenTopic := map[string]string{}
	for _, ts := range doc.Topics {
		id := lowerFirst(ident(ts.Topic))
		if prev, ok := seenTopic[id]; ok {
			return "", fmt.Errorf("MsgTopics 键冲突: %s（%s vs %s）", id, prev, ts.Topic)
		}
		seenTopic[id] = ts.Topic
		fmt.Fprintf(&b, "  %s: '%s',\n", jsKey(id), ts.Topic)
	}
	b.WriteString("}\n\n")

	// MsgClientTopics：schema clientTopic（前端 type ↔ 总线相对主题 的键）。
	b.WriteString("/** 客户端 topic（schema clientTopic；前端 type ↔ 总线相对主题 的键）。 */\n")
	b.WriteString("export const MsgClientTopics = {\n")
	seenClientTopic := map[string]string{}
	for _, ts := range doc.Topics {
		if ts.ClientTopic == "" {
			continue
		}
		id := lowerFirst(ident(ts.ClientTopic))
		if prev, ok := seenClientTopic[id]; ok {
			return "", fmt.Errorf("MsgClientTopics 键冲突: %s（%s vs %s）", id, prev, ts.ClientTopic)
		}
		seenClientTopic[id] = ts.ClientTopic
		fmt.Fprintf(&b, "  %s: '%s',\n", jsKey(id), ts.ClientTopic)
	}
	b.WriteString("}\n\n")

	// FieldKeys：全部字段名并集（跨主题；JS 对象键无冲突）。
	all := map[string]bool{}
	for _, ts := range doc.Topics {
		for _, s := range sides {
			for k := range fieldsOf(ts, s) {
				all[k] = true
			}
		}
	}
	b.WriteString("/** 通用字段键（全部字段名并集，跨主题共用）。 */\n")
	b.WriteString("export const FieldKeys = {\n")
	for _, k := range sortedKeysSet(all) {
		fmt.Fprintf(&b, "  %s: '%s',\n", jsKey(k), k)
	}
	b.WriteString("}\n")

	// FieldDict：语义键 → 规范名（字段字典镜像；同语义唯一名，别名/例外不在此）。
	if len(doc.FieldDictionary) > 0 {
		sems := make([]string, 0, len(doc.FieldDictionary))
		for sem := range doc.FieldDictionary {
			if strings.HasPrefix(sem, "_") {
				continue
			}
			sems = append(sems, sem)
		}
		sort.Strings(sems)
		b.WriteString("\n/** 字段语义字典（语义键 → 规范名；同语义唯一名，见 schema fieldDictionary）。 */\n")
		b.WriteString("export const FieldDict = {\n")
		for _, sem := range sems {
			name := doc.FieldDictionary[sem].Name
			if name == "" {
				name = sem
			}
			fmt.Fprintf(&b, "  %s: '%s',\n", jsKey(sem), name)
		}
		b.WriteString("}\n")
	}

	// 每主题键对象（payload/result/event 合并去重）。
	for _, ts := range doc.Topics {
		merged := map[string]bool{}
		for _, s := range sides {
			for k := range fieldsOf(ts, s) {
				merged[k] = true
			}
		}
		if len(merged) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n/** %s 字段键（payload/result/event 合并去重）。 */\n", ts.Topic)
		fmt.Fprintf(&b, "export const %sKeys = {\n", ident(ts.Topic))
		for _, k := range sortedKeysSet(merged) {
			fmt.Fprintf(&b, "  %s: '%s',\n", jsKey(k), k)
		}
		b.WriteString("}\n")
	}
	return b.String(), nil
}

// ── 标识符工具 ─────────────────────────────────────────────────────────

// splitIdent 按 `.`/`-`/`_` 切分标识符来源串。
func splitIdent(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	})
}

// ident 转 PascalCase（`gui.init-data` → `GuiInitData`）。
func ident(s string) string {
	var b strings.Builder
	for _, p := range splitIdent(s) {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

// lowerFirst 首字母小写（`GuiInitData` → `guiInitData`）。
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// jsKey 需要时把属性名加引号（非合法 JS 标识符，如 `tool-call-id`）。
func jsKey(k string) string {
	if isValidJSIdent(k) {
		return k
	}
	return "'" + k + "'"
}

func isValidJSIdent(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		ok := r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if i > 0 {
			ok = ok || (r >= '0' && r <= '9')
		}
		if !ok {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]fieldSpec) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hasUpper 是否含大写字母（camelCase 判据）。
func hasUpper(s string) bool { return strings.ToLower(s) != s }

// mapKeys 取 map 的键集合（供 sortedKeysSet 排序）。
func mapKeys(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func sortedKeysSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
