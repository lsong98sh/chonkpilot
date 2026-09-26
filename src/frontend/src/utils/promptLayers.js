// 场景「组合后系统提示词」三层拼接纯函数（[25-MCP与场景分层模型] §3）。
//
// 三层 = 全局层 → 场景层 → agent 层：
//   全局 | 身份 / 运行环境           | **后端代码写死**（不落文件、不可配置）
//   场景 | scenario.description + 代码按 agents 自动拼接的成员段（名字 + roleTag + 描述）
//   agent| 当前 agent 的 prompt（主 agent = main.agent.md）
//
// ⚠️ 全局层文案**由后端代码写死**，前端不得编造：本模块只标记 `available:false`，
//    由 UI（CombinedPromptPreview.vue）标注「全局层文案以后端为准」，不硬编码业务文案。
// 场景层由后端拼接（`src/lib/llm/server/turn.go` 的 `scenarioLayer` / `membersSegment`）；
// 后端**无**"按未保存草稿现算"的接口 → 编辑器预览按**后端同款格式**复刻（前缀 `【场景】` /
// `【团队成员】`、成员行 `- 名字（roleTag）：描述`），保证预览与实际注入一致。

/**
 * 场景层「成员段」：每条 = 名字 + roleTag + 描述（一条一行，`- 名字（roleTag）：描述`）。
 * 格式与后端 `turn.go membersSegment` 一致：字段各自 trim、三项皆空的名目跳过。
 * @param {Array} agents 场景 agents（含主 agent）
 * @returns {string} 成员段文本（空 agents → ''）
 */
export function composeScenarioMembers(agents) {
  const list = Array.isArray(agents) ? agents : []
  const lines = []
  for (const a of list) {
    if (!a || typeof a !== 'object') continue
    const name = String(a.name == null ? '' : a.name).trim()
    const role = String(a.roleTag == null ? '' : a.roleTag).trim()
    const desc = String(a.description == null ? '' : a.description).trim()
    if (!name && !role && !desc) continue
    let line = '- ' + name
    if (role) line += `（${role}）`
    if (desc) line += `：${desc}`
    lines.push(line)
  }
  return lines.join('\n')
}

/**
 * 场景层文本 = `【场景】description` + `【团队成员】\n成员段`（格式同后端 `turn.go scenarioLayer`：
 * 两段皆空 → ''；两段间单换行；成员行内单换行）。
 * @param {{description?: string, agents?: Array}} scenario
 * @returns {string}
 */
export function composeScenarioLayer(scenario) {
  const description = String((scenario && scenario.description) || '').trim()
  const members = composeScenarioMembers(scenario && scenario.agents)
  const parts = []
  if (description) parts.push('【场景】' + description)
  if (members) parts.push('【团队成员】\n' + members)
  return parts.join('\n')
}

/**
 * 按 §3 组装三层结构（供只读预览渲染）。
 *
 * 边界：agent 层 = **当前 agent 的 prompt**（§3 字面口径）。后端在「被委派的子轮次」还会给
 * 子 agent 的 prompt 套一层子会话包装（`domainmcp.go childAgentSystem`：`[子会话 agent: X]` +
 * prompt + `[委派条件] Y`）—— 那是**子轮次运行时**行为，不在本只读预览（编辑器视角 = 场景定义）内。
 *
 * @param {{description?: string, agents?: Array, agent?: object}} input
 *   agent = 当前 agent（预览页签用，缺省取主 agent）
 * @returns {{global: {available: boolean, content: string}, scenario: {content: string}, agent: {name: string, content: string}}}
 */
export function buildPromptLayers(input) {
  const src = input || {}
  const agents = Array.isArray(src.agents) ? src.agents : []
  const agent = src.agent || agents.find(a => a && a.isMain) || null
  return {
    // 全局层：后端代码写死，前端取不到 → available=false（UI 标注「以后端为准」，不编造文案）
    global: { available: false, content: '' },
    scenario: { content: composeScenarioLayer({ description: src.description, agents }) },
    agent: { name: String((agent && agent.name) || ''), content: String((agent && agent.prompt) || '') },
  }
}
