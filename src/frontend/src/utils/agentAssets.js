// 智能体资产（agent）共享装载：LLM 选项 + 运行时工具分组（按级别矩阵过滤）。
// 由「场景编辑」（ScenarioEditDialog）与「智能体原语编辑」（PrimitivePanel agent 模式）共用
// （P4，2026-10-01：智能体编辑界面 = 场景编辑里的 agent 编辑部分，复用同一份装载逻辑）。
//
// 级别矩阵（纯逻辑单源）= utils/agentLevelMatrix.js（与本文件分离以便 node 测试直跑）。
import mq from './mq'
import { MsgClientTopics } from '../events/msgkeys.js'
import { getUserConfig } from '../api/config'
import { getKnowledgeRoot, listPrimitives, readPrimitive } from '../api/knowledge'
import { nodeLevel, toolAllowedForLevel } from './agentLevelMatrix'

// 无 server 信息（alias/node 均缺）时的分组键：中性 ASCII 键（展示走 scenario.level.global 词条，E-43），
// 不再硬编码中文「全局」。
const GLOBAL_GROUP = 'global'

// loadLlmOptions 载入 LLM 选项：首项为「默认」（value=''），其余为用户配置的 llms。
// t = i18n 的 translate（取默认项文案）。
export async function loadLlmOptions(t) {
  try {
    const uc = await getUserConfig()
    const llms = uc?.config?.llms || []
    return [
      { label: t('scenario.default_label'), value: '' },
      ...llms.map(llm => ({ label: `${llm.name} (${llm.model})`, value: llm.name })),
    ]
  } catch (e) {
    return [{ label: t('scenario.default_label'), value: '' }]
  }
}

// loadToolGroups 载入运行时工具分组（T-31 能力面）：经桥客户端主题 tools-list →
// 相对主题 mcp-tools-list（bridge.go frontMethodSubjects；桥自动注入 instance_id）。
// 结果 {resultType, tools, ttlMs}，每项 {name, scope, description, inputSchema,
// _meta:{hot, category, server}, node}；按 _meta.server.alias | node 分组展示。
//
// kind（可选）= 当前编辑级别（app|user|project|prjusr，P4 2026-10-01）：给出时按**级别矩阵**
// 过滤 —— **越权项不出现**（同 `toolAllowedForLevel`）；不给出（空）→ 不过滤（旧行为）。
// t（可选）= i18n 的 translate：给出时分组标签用级别文案（scenario.level.*）。
// 返回 [{ name, label, level, tools:[{name, desc, server}] }]；失败 → []。
export async function loadToolGroups(kind, t) {
  const groups = []
  try {
    // tools-list：客户端能力面 type（schema clientTopic，桥映射 mcp-tools-list），常量见 MsgClientTopics。
    const env = await mq.emit(MsgClientTopics.toolsList, {})
    const res = env && env.backend && env.backend.result
    const tools = res && Array.isArray(res.tools) ? res.tools : []

    const byServer = new Map()
    for (const tl of tools) {
      if (kind && !toolAllowedForLevel(tl, kind)) continue // 越权项不出现（级别矩阵）
      const meta = tl._meta || {}
      const srv = meta.server || {}
      const lvl = nodeLevel(srv.node)
      const groupKey = srv.alias || srv.node || GLOBAL_GROUP
      if (!byServer.has(groupKey)) byServer.set(groupKey, { level: lvl, tools: [] })
      byServer.get(groupKey).tools.push({
        name: tl.name,
        desc: (meta.hot ? '[hot] ' : '') + (tl.description || ''),
        // 展示用：网关为暴露名加的前缀（self_/dir_ 等）剥掉后显示（`name` 仍作配置键 / 勾选键）
        server: srv,
      })
    }
    for (const [key, g] of byServer) {
      // 级别标签：有级别走 scenario.level.<level>；无级别且为全局组 → scenario.level.global（E-43）
      const label = g.level && t ? t('scenario.level.' + g.level)
        : (key === GLOBAL_GROUP && t ? t('scenario.level.global') : key)
      groups.push({ name: key, label, level: g.level, tools: g.tools })
    }
  } catch (_) { /* 工具面不可用 → 空分组 */ }
  return groups
}

// 级别「可见范围」（2026-10-14）：本级可见**本级及更广**级别的资产（app ⊂ user ⊂ project ⊂ prjusr）。
const LEVEL_VISIBLE = {
  app: ['app'],
  user: ['app', 'user'],
  project: ['app', 'user', 'project'],
  prjusr: ['app', 'user', 'project', 'prjusr'],
}
// 高 → 低（同名时**高级别遮蔽低级别**，与后端 capability 覆盖式解析一致）。
const LEVEL_ORDER = ['prjusr', 'project', 'user', 'app']

// loadSkillOptions 载入**当前级别可见**的 skill 名单（供智能体「技能」页签勾选）。
// kind = 当前编辑级别（app|user|project|prjusr）；t = i18n translate（级别标签）。
// 返回 [{ name, level, label }]（按名称升序；同名以高级别优先）。
export async function loadSkillOptions(kind, t) {
  const visible = LEVEL_VISIBLE[kind] || LEVEL_VISIBLE.prjusr
  const out = []
  const seen = new Set()
  for (const lv of LEVEL_ORDER) {
    if (!visible.includes(lv)) continue
    let root = ''
    try { root = (await getKnowledgeRoot(lv)).root || '' } catch (_) { continue }
    if (!root) continue
    const dir = root.replace(/\\/g, '/').replace(/\/+$/, '') + '/skills'
    let files = []
    try { files = (await listPrimitives(dir)).files || [] } catch (_) { continue }
    for (const f of files) {
      if (f.type !== 'skill') continue
      const base = String(f.path || f.name || '').replace(/\\/g, '/').split('/').pop() || ''
      const name = base.replace(/\.skill\.md$/i, '')
      if (!name || seen.has(name)) continue // 高级别优先
      seen.add(name)
      const label = t ? t('scenario.level.' + lv) : lv
      out.push({ name, level: lv, label })
    }
  }
  return out.sort((a, b) => a.name.localeCompare(b.name))
}

// loadResourceOptions 载入**当前级别可见**的本地 resource 原语名单（供智能体「资源」页签勾选）。
// 与 loadSkillOptions 同构（同一级别可见矩阵 / 同名高级别优先），仅目录 = `resources/`、
// 文件后缀 = `.resource.md`。kind = 当前编辑级别；t = i18n translate（级别标签）。
// 返回 [{ name, level, label }]（按名称升序）。
export async function loadResourceOptions(kind, t) {
  const visible = LEVEL_VISIBLE[kind] || LEVEL_VISIBLE.prjusr
  const out = []
  const seen = new Set()
  for (const lv of LEVEL_ORDER) {
    if (!visible.includes(lv)) continue
    let root = ''
    try { root = (await getKnowledgeRoot(lv)).root || '' } catch (_) { continue }
    if (!root) continue
    const dir = root.replace(/\\/g, '/').replace(/\/+$/, '') + '/resources'
    let files = []
    try { files = (await listPrimitives(dir)).files || [] } catch (_) { continue }
    for (const f of files) {
      if (f.type !== 'resource') continue
      const base = String(f.path || f.name || '').replace(/\\/g, '/').split('/').pop() || ''
      const name = base.replace(/\.resource\.md$/i, '')
      if (!name || seen.has(name)) continue // 高级别优先
      seen.add(name)
      const label = t ? t('scenario.level.' + lv) : lv
      out.push({ name, level: lv, label })
    }
  }
  return out.sort((a, b) => a.name.localeCompare(b.name))
}

// loadMcpResources 载入 MCP resources（T-31 能力面）：经桥客户端主题 resources-list →
// 相对主题 mcp-resources-list（同 loadToolGroups 的 tools-list 写法；桥自动注入 instance_id）。
// 结果 {resources:[...]}，每项 {uri, name, description, mimeType, scope, node}（gateway handleResourcesList）；
// 按 **node（MCP 节点 / server）** 分组展示。失败 → []（不抛，工具面不可用时静默降级）。
// 返回 [{ name: <分组键>, label, resources:[{ value, name, uri, desc }] }]；value = uri（勾选键）。
export async function loadMcpResources(t) {
  const groups = []
  try {
    const env = await mq.emit(MsgClientTopics.resourcesList, {})
    const res = env && env.backend && env.backend.result
    const resources = res && Array.isArray(res.resources) ? res.resources : []

    const byNode = new Map()
    for (const rr of resources) {
      const uri = String(rr && rr.uri || '')
      if (!uri) continue // 无 uri 的条目无法作为勾选键 / 读取键
      const key = String(rr.node || GLOBAL_GROUP)
      if (!byNode.has(key)) byNode.set(key, { level: nodeLevel(rr.node), resources: [] })
      byNode.get(key).resources.push({
        value: uri,
        name: String(rr.name || ''),
        uri,
        desc: String(rr.description || ''),
      })
    }
    for (const [key, g] of byNode) {
      // 级别标签优先（node 可判定级别时）；无级别且为全局组 → scenario.level.global，其余用 node 名
      const label = g.level && t ? t('scenario.level.' + g.level)
        : (key === GLOBAL_GROUP && t ? t('scenario.level.global') : key)
      groups.push({ name: key, label, resources: g.resources })
    }
  } catch (_) { /* 资源面不可用 → 空分组 */ }
  return groups
}

// loadSkillTools 读取 skill 原语契约的 `refs.tools`（skill → 工具名列表），供「工具」页签
// 「技能关联工具默认勾选且不可取消」用。names = 当前 agent.skills（技能名清单）。
// 级别可见矩阵与 loadSkillOptions 一致（本级及更广级别可见）；同名 skill **高级别优先命中**。
// 该级无此技能 / 读取失败 → 该项不产生映射（不抛）。返回 { skillName: [toolName...] }。
export async function loadSkillTools(kind, names) {
  const out = {}
  const wanted = [...new Set((names || []).map(n => String(n || '').trim()).filter(Boolean))]
  if (wanted.length === 0) return out
  const visible = LEVEL_VISIBLE[kind] || LEVEL_VISIBLE.prjusr
  const roots = {}
  for (const lv of LEVEL_ORDER) {
    if (!visible.includes(lv)) continue
    try { roots[lv] = (await getKnowledgeRoot(lv)).root || '' } catch (_) { roots[lv] = '' }
  }
  for (const name of wanted) {
    for (const lv of LEVEL_ORDER) {
      const root = roots[lv]
      if (!root) continue
      const abs = root.replace(/\\/g, '/').replace(/\/+$/, '') + '/skills/' + name + '.skill.md'
      try {
        const res = await readPrimitive(abs)
        const tools = res && res.doc && res.doc.refs && Array.isArray(res.doc.refs.tools)
          ? res.doc.refs.tools.map(x => String(x || '').trim()).filter(Boolean)
          : []
        if (tools.length) { out[name] = tools.sort(); break } // 高级别优先命中
      } catch (_) { /* 该级无此技能 → 继续下一级 */ }
    }
  }
  return out
}
