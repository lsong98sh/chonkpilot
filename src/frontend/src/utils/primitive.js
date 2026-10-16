// 原语文件（*.type.md）工具：类型后缀识别（对齐后端 mcp 契约与 ListPrimitives.type）。
// token: tool / skill / prompt / resource / agent（agent = 智能体原语，*.agent.md）。
export const PRIMITIVE_TOKENS = ['tool', 'skill', 'prompt', 'resource', 'agent']

// 类型目录名（复数，后端自动预置）→ token
export const TYPE_DIR_MAP = { tools: 'tool', skills: 'skill', prompts: 'prompt', resources: 'resource', agents: 'agent' }

// 类型目录相对 capability 根（知识库）的物理路径 → token（对齐后端 capfs.Types.Rel）：
// capability 根下 **6 个扁平子目录**（prompts/tools/resources/skills/agents/scenarios），
// 旧 `knowledge/**` 归并层已删除。用于「扩展页」按类型范围过滤目录子树。
export const TYPE_DIR_REL = {
  tool: 'tools',
  skill: 'skills',
  prompt: 'prompts',
  resource: 'resources',
  agent: 'agents',
}

export function primitiveTokenOf(path) {
  if (!path) return ''
  const p = String(path).replace(/\\/g, '/').toLowerCase()
  for (const tok of PRIMITIVE_TOKENS) {
    if (p.endsWith('.' + tok + '.md')) return tok
  }
  return ''
}

export function isPrimitiveFile(path) {
  return primitiveTokenOf(path) !== ''
}

// 沿目录链向上找最近类型目录 → 该目录的类型 token（空 = 不在类型目录下）
export function nearestTypeToken(path) {
  if (!path) return ''
  const segs = String(path).replace(/\\/g, '/').split('/').filter(Boolean)
  for (let i = segs.length - 1; i >= 0; i--) {
    const tok = TYPE_DIR_MAP[segs[i].toLowerCase()]
    if (tok) return tok
  }
  return ''
}

// 类型 token → i18n label key（knowledgePrimitive.*）
export function primitiveLabelKey(tok) {
  return { tool: 'tool', skill: 'skill', prompt: 'prompt', resource: 'resource', agent: 'agent' }[tok] || tok
}

// 文件类型 token（含裸 md 按父类型目录回退时由后端返回；此处只做展示归类）
export function primitiveTagType(tok) {
  return { tool: 'primary', skill: 'success', prompt: 'warning', resource: 'info', agent: 'warning' }[tok] || 'info'
}

// ── v2 契约（H1 分区 + yaml/json 代码块，见 25-mcp-server §3.1）──────────────

// 各类型的「默认键」：新建时**必须显示且不可删除**（连同 name）。其余键可增删。
export const PRIMITIVE_META_DEFAULTS = {
  tool: ['runtime', 'args', 'output'],
  skill: [],
  prompt: [],
  resource: [],
  agent: ['roletag', 'ismain', 'llm'],
}

// 各类型「正文」的推荐大纲（骨架，指导用户逐段填写；也是「AI 生成」的依据）。
export const PRIMITIVE_OUTLINES = {
  tool: ['## 用途', '## 适用场景', '## 用法示例', '## 注意事项'],
  skill: ['## 目标', '## 步骤', '## 检查点', '## 反例'],
  prompt: ['## 角色', '## 任务', '## 输入', '## 输出要求'],
  resource: ['## 概述', '## 要点', '## 参考'],
  agent: ['## 职责', '## 工作方式', '## 协作', '## 输出'],
}

// 各类型出现「参数」页签（JSON Schema）。
export function hasParamsTab(tok) {
  return tok === 'tool' || tok === 'prompt'
}

// 各类型出现「参考」页签（skills / tools 名单）。
export function hasRefsTab(tok) {
  return tok === 'skill' || tok === 'agent'
}

// buildContractMd 按 v2 规则把表单组装为契约 markdown（**预览**用；落盘以服务端 Build 为准）。
export function buildContractMd(doc) {
  const d = doc || {}
  const lines = ['# 基本信息', '```yaml', 'name: ' + String(d.name || '').trim()]
  if (String(d.title || '').trim()) lines.push('title: ' + String(d.title).trim())
  const meta = d.meta || {}
  Object.keys(meta)
    .filter((k) => k && k !== 'name' && k !== 'title' && String(meta[k] ?? '').trim() !== '')
    .sort()
    .forEach((k) => lines.push(k + ': ' + String(meta[k]).trim()))
  lines.push('```', '')
  if (String(d.description || '').trim()) lines.push('# 摘要', String(d.description).trim(), '')
  if (String(d.parameters || '').trim()) lines.push('# 参数', '```json', String(d.parameters).trim(), '```', '')
  const refs = d.refs || {}
  const refKeys = Object.keys(refs).filter((k) => (refs[k] || []).length > 0).sort()
  if (refKeys.length) {
    lines.push('# 参考', '```yaml')
    refKeys.forEach((k) => {
      lines.push(k + ':')
      refs[k].forEach((it) => lines.push('  - ' + String(it).trim()))
    })
    lines.push('```', '')
  }
  if (String(d.delegate || '').trim()) lines.push('# 委托', String(d.delegate).trim(), '')
  if (String(d.content || '').trim()) lines.push('# 正文', String(d.content).trim())
  return lines.join('\n')
}
