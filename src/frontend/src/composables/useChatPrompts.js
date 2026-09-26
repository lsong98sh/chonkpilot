// chat 面「prompt 选择」状态逻辑（[25-MCP与场景分层模型] §7）——composable 封装：
//   ① 列举**知识库 prompt**（capability 三级根下 `prompts/*.prompt.md`）：复用既有 data 层
//      接口 `data-knowledge-root` / `data-knowledge-list` / `data-knowledge-read`（api/knowledge.js），
//      **不新增消息主题**；
//   ② 选中（单选，见 25 §7 未明示多选 → 置单选）→ 读正文（注入用）；
//   ③ 移除；内容并入 user 消息由调用方（InputBox.serialize → utils/chatPrompt.composeUserText）完成。
import { ref } from 'vue'
import { getKnowledgeRoot, listPrimitives, readPrimitive } from '../api/knowledge.js'
import { collectPromptEntries, mergePromptEntries } from '../utils/chatPrompt.js'

// 三级知识库（app / user / project；与 fileTree 知识树同级语义）：合并时**具体级优先**
// （project > user > app），同名只留最具体的一级。
const LEVELS = ['app', 'user', 'project']
// prompts 目录递归深度（1 = 仅 `prompts/*.prompt.md`；2 = 含一层子目录）—— 有界，避免深树风暴。
const MAX_DEPTH = 2

export function useChatPrompts(deps) {
  // 数据层接口（默认 = api/knowledge.js；注入仅为单测替换，运行时不传）
  const api = (deps && deps.api) || { getKnowledgeRoot, listPrimitives, readPrimitive }

  /** 可选的 prompt 条目（[{name, path, level, description}]，按 name 升序）。 */
  const available = ref([])
  /** 已选 prompt（{name, path, level, content}；null = 未选）。 */
  const selected = ref(null)
  /** 列举中。 */
  const loading = ref(false)
  /** 最近一次错误消息（列举 / 读取失败；空 = 无错）。 */
  const error = ref('')

  // 递归收集某目录下的 prompt 条目（目录不存在 / 无权限 → 上层捕获跳过该级）。
  async function walk(dir, depth, level, out) {
    if (!dir || depth <= 0) return
    const res = await api.listPrimitives(dir)
    out.push(...collectPromptEntries(res && res.files, level))
    for (const d of (res && res.dirs) || []) {
      if (d && d.path) await walk(d.path, depth - 1, level, out)
    }
  }

  // 列举三级知识库 prompt（某级缺失/失败 → 跳过，不阻断其它级）。
  async function load() {
    if (loading.value) return
    loading.value = true
    error.value = ''
    try {
      let merged = []
      for (const level of LEVELS) {
        let root = ''
        try {
          root = ((await api.getKnowledgeRoot(level)) || {}).root || ''
        } catch (_) {
          continue
        }
        if (!root) continue
        const out = []
        try {
          await walk(`${root}/prompts`, MAX_DEPTH, level, out)
        } catch (_) {
          // 该级无 prompts 目录（或不可读）→ 跳过该级
        }
        merged = mergePromptEntries(merged, out)
      }
      available.value = merged.sort((a, b) => a.name.localeCompare(b.name))
    } catch (e) {
      error.value = (e && e.message) || String(e)
      available.value = []
    } finally {
      loading.value = false
    }
  }

  // 选中：读 prompt 正文（data-knowledge-read）→ 供发送时并入 user 消息。
  // 失败 → 不选中 + 回报 false（调用方提示，不静默）。
  async function pick(entry) {
    if (!entry || !entry.path) return false
    try {
      const r = await api.readPrimitive(entry.path)
      const doc = (r && r.doc) || {}
      const content = (doc.content && String(doc.content)) || (r && r.source) || ''
      selected.value = { ...entry, content }
      return true
    } catch (e) {
      selected.value = null
      error.value = (e && e.message) || String(e)
      return false
    }
  }

  /** 移除已选 prompt（内容随之不再并入 user 消息）。 */
  function remove() {
    selected.value = null
  }

  return { available, selected, loading, error, load, pick, remove }
}
