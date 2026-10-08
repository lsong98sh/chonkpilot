// 提示词变量目录（gui.prompt-vars，OP-12，2026-10-06）：本地面只读，返回**变量插入**所需的
// 分组清单（键 + 说明），供前端「提示词编辑统一组件」的变量按钮 popup 渲染（61-消息一览 §1）。
//
// 单一数据源 = 本文件的**后端常量**（前端不硬编码变量清单）：
//   - {{toolchain.*}}：与工具链探测同源（toolchainCandidates，7 项）；
//   - {{path.*}}：四种根路径（exe/user/data/work，与 llm/server 的 replacePaths 同口径）；
//   - {{env.CHONKPILOT_*}}：executor DSL 只读变量（fileops 的 Env* 常量同名；dslOnly=true，
//     仅 DSL/脚本类编辑器列出 —— 本仓前端暂无该类编辑器，前端按 dslOnly 过滤）。
//
// 只读面：无副作用、不落库、不需要实例（变量清单与实例无关，路径取值在替换时解析）。
package bridge

import (
	"context"
	"encoding/json"
)

// promptVarItem 单个可插入变量：key = 占位符整串（含 {{ }}，直接插入文本）；desc = 用途说明；
// dslOnly = 仅 DSL/脚本类编辑器展示（前端据此过滤，避免在不相关编辑器列出 {{env.*}}）。
type promptVarItem struct {
	Key     string `json:"key"`
	Desc    string `json:"desc"`
	DSLOnly bool   `json:"dslOnly"`
}

// promptVarGroup 一组变量（id/label + items）。
type promptVarGroup struct {
	ID    string          `json:"id"`
	Label string          `json:"label"`
	Items []promptVarItem `json:"items"`
}

// toolchainVarDesc 工具链 key → 说明（与 toolchainCandidates 的 ID 同名；键集由候选清单决定）。
var toolchainVarDesc = map[string]string{
	"java":   "Java 可执行文件路径（usr/prj 配置 javaPath）",
	"python": "Python 解释器路径（pythonPath）",
	"node":   "Node.js 可执行文件路径（nodePath）",
	"go":     "Go 工具链路径（goPath）",
	"rust":   "Rust 编译器路径（rustPath）",
	"c":      "C/C++ 编译器路径（cCompilerPath）",
	"chrome": "Chrome 可执行文件路径（chromePath）",
}

// promptVarGroups 变量分组目录（toolchain → path → env；env 组 dslOnly=true）。
// toolchain 组逐项取自 toolchainCandidates（单一数据源），避免两处各列一份 key。
func promptVarGroups() []promptVarGroup {
	toolchain := make([]promptVarItem, 0, len(toolchainCandidates))
	for _, c := range toolchainCandidates {
		toolchain = append(toolchain, promptVarItem{
			Key:  "{{toolchain." + c.ID + "}}",
			Desc: toolchainVarDesc[c.ID],
		})
	}
	return []promptVarGroup{
		{ID: "toolchain", Label: "工具链路径", Items: toolchain},
		{ID: "path", Label: "路径", Items: []promptVarItem{
			{Key: "{{path.exeDir}}", Desc: "应用安装目录（可执行文件所在目录）"},
			{Key: "{{path.userDir}}", Desc: "用户数据根目录 ~/.chonkpilot"},
			{Key: "{{path.dataDir}}", Desc: "当前项目数据目录 ~/.chonkpilot/data/<项目 id>"},
			{Key: "{{path.workDir}}", Desc: "当前项目工作目录"},
		}},
		// {{env.*}} 同名于 executor 的 CHONKPILOT_*（fileops.Env*）；仅 DSL/脚本插值有效。
		{ID: "env", Label: "环境变量（DSL/脚本）", Items: []promptVarItem{
			{Key: "{{env.CHONKPILOT_WORKDIR}}", Desc: "项目工作目录", DSLOnly: true},
			{Key: "{{env.CHONKPILOT_DATADIR}}", Desc: "数据目录", DSLOnly: true},
			{Key: "{{env.CHONKPILOT_TEMPDIR}}", Desc: "临时目录根", DSLOnly: true},
			{Key: "{{env.CHONKPILOT_EXEDIR}}", Desc: "可执行文件所在目录", DSLOnly: true},
			{Key: "{{env.CHONKPILOT_PROJECT}}", Desc: "项目目录（同 WORKDIR）", DSLOnly: true},
		}},
	}
}

// callPromptVars 返回变量分组目录 → {groups:[{id,label,items:[{key,desc,dslOnly}]}]}
// （gui.prompt-vars；只读，无副作用）。
func callPromptVars(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error) {
	return json.Marshal(map[string]any{"groups": promptVarGroups()})
}
