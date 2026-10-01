// 原语文件（*.type.md）工具：类型后缀识别（对齐后端 mcp 契约与 ListPrimitives.type）。
// token: tool / skill / prompt / resource。
export const PRIMITIVE_TOKENS = ['tool', 'skill', 'prompt', 'resource']

// 类型目录名（复数，后端自动预置）→ token
export const TYPE_DIR_MAP = { tools: 'tool', skills: 'skill', prompts: 'prompt', resources: 'resource' }

// 类型目录相对 capability 根（知识库）的物理路径 → token（对齐后端 capfs.Types.Rel）：
// capability 根下 **6 个扁平子目录**（prompts/tools/resources/skills/agents/scenarios），
// 旧 `knowledge/**` 归并层已删除。用于「工具页签」按类型范围过滤目录子树。
export const TYPE_DIR_REL = {
  tool: 'tools',
  skill: 'skills',
  prompt: 'prompts',
  resource: 'resources',
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
  return { tool: 'tool', skill: 'skill', prompt: 'prompt', resource: 'resource' }[tok] || tok
}

// 文件类型 token（含裸 md 按父类型目录回退时由后端返回；此处只做展示归类）
export function primitiveTagType(tok) {
  return { tool: 'primary', skill: 'success', prompt: 'warning', resource: 'info' }[tok] || 'info'
}
