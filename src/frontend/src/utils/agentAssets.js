// 智能体资产（agent）共享装载：LLM 选项 + 运行时工具分组。
// 由「场景编辑」（ScenarioEditDialog）与「智能体原语编辑」（PrimitivePanel agent 模式）共用
// （P4，2026-10-01：智能体编辑界面 = 场景编辑里的 agent 编辑部分，复用同一份装载逻辑）。
import mq from './mq'
import { getUserConfig } from '../api/config'

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
// 返回 [{ name, label, tools:[{name, desc, server}] }]；失败 → []。
export async function loadToolGroups() {
  const groups = []
  try {
    const env = await mq.emit('tools-list', {})
    const res = env && env.backend && env.backend.result
    const tools = res && Array.isArray(res.tools) ? res.tools : []

    const byServer = new Map()
    for (const tl of tools) {
      const meta = tl._meta || {}
      const srv = meta.server || {}
      const groupKey = srv.alias || srv.node || '全局'
      if (!byServer.has(groupKey)) byServer.set(groupKey, [])
      byServer.get(groupKey).push({
        name: tl.name,
        desc: (meta.hot ? '[hot] ' : '') + (tl.description || ''),
        // 展示用：网关为暴露名加的前缀（self_/dir_ 等）剥掉后显示（`name` 仍作配置键 / 勾选键）
        server: srv,
      })
    }
    for (const [key, list] of byServer) {
      groups.push({ name: key, label: key, tools: list })
    }
  } catch (_) { /* 工具面不可用 → 空分组 */ }
  return groups
}
