// 智能体「工具/资产级别矩阵」**纯逻辑单源**（25-MCP与场景分层模型 §4；P4 2026-10-01）。
//
// 无任何 IO 依赖（可在 node 测试直跑）；由 `utils/agentAssets.js`（装载面）与
// `views/scenario/ScenarioEditDialog.vue`（场景编辑）等复用。
// 与后端 `capfs.AgentToolLevels`（src/lib/data/internal/capfs/capfs.go）**逐字一致**
// ——守卫测试 test/scenarioToolMatrix.test.js 直接比对本常量的字面量与 capfs.go。
//
// 语义 = 「同级或更高级」（"级"按共享度：app 系统级最共享 = 最高）：
//   prjusr → {prjusr,user,project,app} · project → {project,app} · user → {user,app} · app → {app}
export const AGENT_LEVEL_MATRIX = {
  prjusr: ['prjusr', 'user', 'project', 'app'],
  project: ['project', 'app'],
  user: ['user', 'app'],
  app: ['app'],
}

// allowedLevels 取某级别的**可用集合**（未知 / 空 → 用户级，与后端 AgentToolLevels 默认一致）。
export function allowedLevels(kind) {
  return AGENT_LEVEL_MATRIX[kind] || AGENT_LEVEL_MATRIX.user
}

// levelAllowed 判定级别 kind 的智能体是否可用 level 级（level 空 = 无法判定 → true，保守放行）。
export function levelAllowed(kind, level) {
  if (!level) return true
  return allowedLevels(kind).includes(level)
}

// nodeLevel capability dir 节点名 → 级别：
//   "self" → app；"<instanceID>-user" / "-project" / "-prjusr" → 对应级；
//   无法判定（第三方节点 / 裸名）→ ''（调用方放行，不误剔除）。
export function nodeLevel(node) {
  const n = String(node || '')
  if (n === 'self') return 'app'
  if (n.endsWith('-user')) return 'user'
  if (n.endsWith('-project')) return 'project'
  if (n.endsWith('-prjusr')) return 'prjusr'
  return ''
}

// toolLevelOf 工具项 → 级别（取 `_meta.server.node`；无法判定 → ''）。
export function toolLevelOf(tool) {
  const srv = (tool && tool._meta && tool._meta.server) || {}
  return nodeLevel(srv.node)
}

// toolAllowedForLevel 编辑期判定某工具是否属当前级别的**可用集合**：
// 越权（级别不在矩阵里）→ false（候选不出现）；无法判定（无 `_meta.server`）→ true（宁全勿误删）。
export function toolAllowedForLevel(tool, kind) {
  const lvl = toolLevelOf(tool)
  if (!lvl) return true
  return levelAllowed(kind, lvl)
}
