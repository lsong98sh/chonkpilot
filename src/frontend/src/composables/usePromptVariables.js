// 提示词变量（OP-12）共享状态与外挂能力：
//   - usePromptVariables：拉取「变量目录」（gui.prompt-vars，单源 = 后端常量）并**缓存**；
//     首次调用拉一次，后续复用（清单为后端常量、进程内不变）。
//   - useVariableInsert：把变量占位符插入到指定 textarea 光标处（配合 v-model 回写）。
// 遵循项目规则：状态逻辑封装于 useXxx；组件间不 watch props/store。
import { ref } from 'vue'
import { getPromptVariables } from '../api/config'
import { insertAtCursor } from '../utils/promptInsert'

// 模块级缓存：清单（后端常量，进程内不变）。
const groups = ref([])
let loadPromise = null

export function usePromptVariables() {
  // load 拉取（幂等）：进行中/已完成的请求复用同一 Promise；失败不缓存 → 下次可重试。
  async function load() {
    if (loadPromise) return loadPromise
    loadPromise = getPromptVariables()
      .then((g) => { groups.value = Array.isArray(g) ? g : [] })
      .catch(() => { groups.value = []; loadPromise = null })
    return loadPromise
  }

  // visibleGroups 过滤 dslOnly：非 DSL/脚本编辑器（dsl=false）不列出 {{env.*}}；
  // 空组剔除。返回普通数组（调用方在 computed 中读取 groups 即可保持响应）。
  function visibleGroups(dsl = false) {
    return groups.value
      .map((g) => ({ ...g, items: (g.items || []).filter((it) => dsl || !it.dslOnly) }))
      .filter((g) => g.items.length > 0)
  }

  return { groups, load, visibleGroups }
}

/**
 * useVariableInsert：在目标 textarea 光标处插入变量占位符。
 * @param {object} opt
 * @param {() => HTMLTextAreaElement|null} opt.getEl 取目标 textarea（组件内 ref.$el）
 * @param {() => string} opt.getValue 取当前文本
 * @param {(v: string) => void} opt.setValue 回写新文本（触发 v-model）
 */
export function useVariableInsert({ getEl, getValue, setValue }) {
  function insert(token) {
    if (!token) return
    const el = getEl ? getEl() : null
    const { value, caret } = insertAtCursor(el, getValue ? getValue() : '', token)
    setValue(value)
    if (el) {
      // 下一帧（v-model 回写 DOM 后）恢复焦点与光标位置，支持连续插入。
      requestAnimationFrame(() => {
        try {
          el.focus()
          el.setSelectionRange(caret, caret)
        } catch (_) { /* 非 textarea / 已销毁 → 忽略 */ }
      })
    }
  }
  return { insert }
}
