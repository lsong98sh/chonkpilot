// chat 面「prompt 选择注入」纯函数（[25-MCP与场景分层模型] §7）：
//   用户在 chat 输入框从**知识库 prompt**（capability 下 `*.prompt.md`）中选择 →
//   发送时把 prompt 内容**并入 user 消息文本**（零契约变更：llm-start / message payload 不加字段）；
//   HTML 上以 **`/<prompt-name>`** 的 tag 显示（可移除）。
//
// 本模块只做纯计算与归一，不含 IO / Vue 状态（IO 见 composables/useChatPrompts.js，
// 组件见 views/chat/InputBox.vue）→ 便于 node:test 直测。

/** 知识库提示词文件后缀（后端 KnowledgeFile.type = 'prompt'；文件名恒为 `<名>.prompt.md`） */
export const PROMPT_SUFFIX = '.prompt.md'

/**
 * prompt 名 = 文件名去掉 `.prompt.md`（无后缀则原样；仅取末段文件名，不含目录）。
 * @param {string} fileName 文件名或路径（如 `prompts/summary.prompt.md`）
 * @returns {string} prompt 名（如 `summary`；空输入 → ''）
 */
export function promptNameOf(fileName) {
  const base = String(fileName || '').split(/[/\\]/).pop() || ''
  if (!base) return ''
  return base.toLowerCase().endsWith(PROMPT_SUFFIX) ? base.slice(0, -PROMPT_SUFFIX.length) : base
}

/**
 * prompt 名 → HTML 展示 tag 文本（`/<name>`）。
 * @param {string} name prompt 名
 * @returns {string} 如 `/summary`（空名 → ''）
 */
export function promptTagOf(name) {
  const n = String(name || '').trim()
  return n ? '/' + n : ''
}

/** 是否知识库提示词条目（后端 type=prompt，或文件名以 `.prompt.md` 结尾）。 */
export function isPromptEntry(file) {
  if (!file) return false
  if (file.type === 'prompt') return true
  return String(file.name || '').toLowerCase().endsWith(PROMPT_SUFFIX)
}

/**
 * 知识库目录列举结果 → prompt 条目列表（[{name, path, level, description}]）。
 * @param {Array} files data-knowledge-list 的 files
 * @param {string} level 该目录所属级别（app / user / project）
 */
export function collectPromptEntries(files, level) {
  const out = []
  for (const f of Array.isArray(files) ? files : []) {
    if (!isPromptEntry(f)) continue
    const name = promptNameOf(f.name)
    if (!name) continue
    out.push({
      name,
      path: String((f && f.path) || ''),
      level: level || '',
      description: String((f && f.description) || ''),
    })
  }
  return out
}

// 三级归属优先级（同 id 去重）：更具体的一级胜出（app < user < project）。
export const PROMPT_LEVEL_RANK = { app: 0, user: 1, project: 2 }

function _rank(level) {
  const r = PROMPT_LEVEL_RANK[level]
  return r === undefined ? -1 : r
}

/**
 * 合并两批 prompt 条目：**同名保留级别更具体者**（project > user > app；同级后者覆盖前者）。
 * @param {Array} prev 已有条目
 * @param {Array} next 新增条目
 * @returns {Array} 去重后的条目列表
 */
export function mergePromptEntries(prev, next) {
  const map = new Map()
  for (const e of [...(Array.isArray(prev) ? prev : []), ...(Array.isArray(next) ? next : [])]) {
    if (!e || !e.name) continue
    const old = map.get(e.name)
    if (!old || _rank(e.level) >= _rank(old.level)) map.set(e.name, e)
  }
  return [...map.values()]
}

/**
 * 把所选 prompt 内容并入 user 消息文本（prompt 内容在前 + 空行 + 用户输入）。
 * 入参 prompt = `{name, content}`（未选中 / 空内容 → 原样返回用户文本，**零变更**）。
 * @param {string} userText 用户输入（含附件标记的序列化文本）
 * @param {{content?: string}|null} prompt 所选 prompt（含内容）
 * @returns {string} 并入后的 user 消息文本
 */
export function composeUserText(userText, prompt) {
  const text = userText == null ? '' : String(userText)
  const content = prompt && prompt.content ? String(prompt.content).trim() : ''
  if (!content) return text
  return text.trim() ? `${content}\n\n${text}` : content
}
