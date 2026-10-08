// 场景子 agent「引用（ref）」解析 + 引用 agent 文件的「契约文档 ↔ 可编辑模型」互转（纯函数）。
//
// 子 agent 唯一形态 = 引用（[37-场景] SCEN-002；后端 capfs/scenario.go）：`scenario.json` 的
// `agents[]` 存 `<变量前缀>/<相对路径>`。变量前缀 → 级别（capability 根）映射**与后端
// `capfs.refVarPrefix` 逐字一致**（后端为权威，改后端须同步本表）：
//   `${exeDir}/capability`              → app（系统级，<exeDir>/capability）
//   `${usrDir}/capability`              → user（用户级，~/.chonkpilot/capability）
//   `${workDir}/.chonkpilot/capability` → project（项目级，<workDir>/.chonkpilot/capability）
//   `${dataDir}/capability`             → prjusr（项目私有级）
//
// 编辑引用 agent = **直接读写被引文件**（走知识库读写面 data-knowledge-read/save）：四级
// capability 根**均可读写**（[39 KB-001-S04] 2026-10-01 P1 取消「系统级只读」）→ 不做
// 「系统级只读 → 复制到项目级」的降级；场景里的 ref 保持不变。

import { normalizeTools } from './agentToolFilter.js'

/** 引用变量前缀 → 级别 kind（顺序即匹配优先，与后端 capfs.refVarPrefix 一致）。 */
export const AGENT_REF_PREFIXES = [
  { prefix: '${exeDir}/capability', kind: 'app' },
  { prefix: '${usrDir}/capability', kind: 'user' },
  { prefix: '${workDir}/.chonkpilot/capability', kind: 'project' },
  { prefix: '${dataDir}/capability', kind: 'prjusr' },
]

/** 路径归一：去空白 + 反斜杠转正斜杠。 */
export function normalizeRefPath(ref) {
  return String(ref == null ? '' : ref).trim().replace(/\\/g, '/')
}

/** 是否绝对路径（Windows 盘符 / UNC / Unix 根）。 */
export function isAbsPath(p) {
  const s = normalizeRefPath(p)
  return /^[A-Za-z]:\//.test(s) || s.startsWith('//') || s.startsWith('/')
}

/**
 * 解析引用 → `{kind, rel}`（rel 含前导 `/`，如 `/agents/x.agent.md`）；
 * 非变量前缀（含已是绝对路径）→ `null`。
 */
export function parseAgentRef(ref) {
  const p = normalizeRefPath(ref)
  if (!p) return null
  for (const { prefix, kind } of AGENT_REF_PREFIXES) {
    if (p === prefix) return { kind, rel: '' }
    if (p.startsWith(prefix + '/')) return { kind, rel: p.slice(prefix.length) }
  }
  return null
}

/**
 * 引用 → 绝对路径：按前缀映射到对应级 capability 根（roots = `{app,user,project,prjusr}` → 根）。
 * 前缀不可识别 / 该级根不可解析 → `''`。
 */
export function resolveAgentRef(ref, roots) {
  const info = parseAgentRef(ref)
  if (!info) return ''
  const root = roots ? roots[info.kind] : ''
  if (!root) return ''
  return String(root).replace(/[\\/]+$/, '') + info.rel
}

/**
 * 引用 → 绝对路径（兼容「新建引用尚未落盘」时前端持有的绝对路径）：已是绝对路径原样返回，
 * 否则按变量前缀解析（见 resolveAgentRef）。
 */
export function resolveAgentRefAbs(ref, roots) {
  const p = normalizeRefPath(ref)
  if (!p) return ''
  if (isAbsPath(p)) return p
  return resolveAgentRef(p, roots)
}

/**
 * 契约文档（[meta]/[description]/[content] 的领域形态）→ 可编辑 agent 模型字段
 * （AgentEditor 消费；`_refMeta` 保留原始 meta，供写回时保真）。
 */
export function agentModelFromDoc(doc, fallback = {}) {
  const d = doc || {}
  const meta = { ...(d.meta || {}) }
  const tools = normalizeTools(meta.tools)
  return {
    name: d.title || fallback.name || '',
    description: d.description || '',
    roleTag: meta.roletag || '',
    prompt: d.content || '',
    tools,
    filterTools: tools.length > 0,
    llmRef: meta.llm || '',
    delegateCond: meta.delegate || '',
    _refMeta: meta,
  }
}

/**
 * 可编辑 agent 模型字段 → 契约文档（写回被引文件；保持 `# 名` + `[meta]` + `[description]` +
 * `[content]` 结构，后端 capfs.BuildDoc 按此序列化）。`_refMeta` 中的未知 meta 键保留。
 */
export function agentDocOf(agent) {
  const a = agent || {}
  const meta = { ...(a._refMeta || {}) }
  const setMeta = (k, v) => {
    const s = String(v == null ? '' : v).trim()
    if (s) meta[k] = s
    else delete meta[k]
  }
  setMeta('roletag', a.roleTag)
  const tools = normalizeTools(a.tools)
  setMeta('tools', tools.length ? JSON.stringify(tools) : '')
  setMeta('llm', a.llmRef)
  setMeta('delegate', a.delegateCond)
  return {
    title: String(a.name || '').trim(),
    meta,
    description: String(a.description || '').trim(),
    content: String(a.prompt || '').trim(),
  }
}
