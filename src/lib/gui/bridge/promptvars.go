// 提示词变量目录（gui.prompt-vars，OP-12，2026-10-06）：本地面只读，返回**变量插入**所需的
// 分组清单（键 + 说明），供前端「提示词编辑统一组件」的变量按钮 popup 渲染（61-消息一览 §1）。
//
// 单一数据源 = 本文件的**后端常量**（前端不硬编码变量清单）：
//   - {{toolchain.*}}：与工具链探测同源（toolchainCandidates，7 项）；
//   - {{path.*}}：四种根路径（exe/user/data/work，与 llm/server 的 replacePaths 同口径）；
//   - {{env.CHONKPILOT_*}}：executor DSL 只读变量（fileops 的 Env* 常量同名；dslOnly=true，
//     仅 DSL/脚本类编辑器列出 —— 本仓前端暂无该类编辑器，前端按 dslOnly 过滤）。
//
// i18n（D-41）：`label` / `desc` 一律返回**文案键**（前端 `$t()` 翻译，键表见
// src/frontend/src/locales/{zh-CN,en-US}/promptVars.json），后端**不再下发硬编码中文 UI 文案**
// （否则 en-US 无法翻译）。`key` 是占位符整串（含 {{ }}，直接插入文本），**非 UI 文案、不翻译**。
//
// 只读面：无副作用、不落库、不需要实例（变量清单与实例无关，路径取值在替换时解析）。
package bridge

import (
	"context"
	"encoding/json"
)

// promptVarItem 单个可插入变量：key = 占位符整串（含 {{ }}，直接插入文本）；desc = 用途说明的
// **文案键**（前端 `$t(desc)`）；dslOnly = 仅 DSL/脚本类编辑器展示（前端据此过滤）。
type promptVarItem struct {
	Key     string `json:"key"`
	Desc    string `json:"desc"`
	DSLOnly bool   `json:"dslOnly"`
}

// promptVarGroup 一组变量（id/label + items）。label = 分组名的**文案键**（前端 `$t(label)`）。
type promptVarGroup struct {
	ID    string          `json:"id"`
	Label string          `json:"label"`
	Items []promptVarItem `json:"items"`
}

// promptVarGroups 变量分组目录（toolchain → path → env；env 组 dslOnly=true）。
// toolchain 组逐项取自 toolchainCandidates（单一数据源），避免两处各列一份 key；
// 各 desc/label 为文案键（`promptVars.toolchain.<id>` / `promptVars.group.<id>` 等，D-41）。
func promptVarGroups() []promptVarGroup {
	toolchain := make([]promptVarItem, 0, len(toolchainCandidates))
	for _, c := range toolchainCandidates {
		toolchain = append(toolchain, promptVarItem{
			Key:  "{{toolchain." + c.ID + "}}",
			Desc: "promptVars.toolchain." + c.ID,
		})
	}
	return []promptVarGroup{
		{ID: "toolchain", Label: "promptVars.group.toolchain", Items: toolchain},
		{ID: "path", Label: "promptVars.group.path", Items: []promptVarItem{
			{Key: "{{path.exeDir}}", Desc: "promptVars.path.exeDir"},
			{Key: "{{path.userDir}}", Desc: "promptVars.path.userDir"},
			{Key: "{{path.dataDir}}", Desc: "promptVars.path.dataDir"},
			{Key: "{{path.workDir}}", Desc: "promptVars.path.workDir"},
		}},
		// {{env.*}} 同名于 executor 的 CHONKPILOT_*（fileops.Env*）；仅 DSL/脚本插值有效。
		{ID: "env", Label: "promptVars.group.env", Items: []promptVarItem{
			{Key: "{{env.CHONKPILOT_WORKDIR}}", Desc: "promptVars.env.CHONKPILOT_WORKDIR", DSLOnly: true},
			{Key: "{{env.CHONKPILOT_DATADIR}}", Desc: "promptVars.env.CHONKPILOT_DATADIR", DSLOnly: true},
			{Key: "{{env.CHONKPILOT_TEMPDIR}}", Desc: "promptVars.env.CHONKPILOT_TEMPDIR", DSLOnly: true},
			{Key: "{{env.CHONKPILOT_EXEDIR}}", Desc: "promptVars.env.CHONKPILOT_EXEDIR", DSLOnly: true},
			{Key: "{{env.CHONKPILOT_PROJECT}}", Desc: "promptVars.env.CHONKPILOT_PROJECT", DSLOnly: true},
		}},
	}
}

// callPromptVars 返回变量分组目录 → {groups:[{id,label,items:[{key,desc,dslOnly}]}]}
// （gui.prompt-vars；只读，无副作用）。
func callPromptVars(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	return json.Marshal(map[string]any{"groups": promptVarGroups()})
}
