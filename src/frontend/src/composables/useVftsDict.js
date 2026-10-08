/**
 * useVftsDict：vfts **系统级**词典（jieba）与「重新索引」的状态逻辑（配置页 VftsConfig 用）。
 *
 * 消息面（61：vfts.dict.get / vfts.dict.set / vfts.reindex，点分相对主题，前端 type 同名）：
 * 前端 `mq.emit(topic, payload)` → 后端 vfts 插件（同主题 promise 写回 v.result）→
 * 前端从 `/publish` 应答信封的 `result` 取数据。
 *
 * 口径：分词器固定 `jieba`；词典**只有系统级**（全机共用一份，无项目级/用户级覆盖）；
 * 词典 / 自定义词变更后**必须重新索引**才生效（分词结果落在索引里）。
 */
import { ref } from 'vue'
import mq from '../utils/mq'
import { MsgTopics } from '../events/msgkeys.js'

export function useVftsDict() {
  const dictDir = ref('')
  const userDictPath = ref('')
  const userDict = ref('')
  const wordCount = ref(0)
  const baseDicts = ref([])
  const loaded = ref(false)
  const loading = ref(false)
  const saving = ref(false)
  const reindexing = ref(false)

  function applyDict(res) {
    if (!res) return
    dictDir.value = res.dict_dir || ''
    userDictPath.value = res.user_dict_path || ''
    userDict.value = res.user_dict || ''
    wordCount.value = typeof res.word_count === 'number' ? res.word_count : 0
    baseDicts.value = Array.isArray(res.base_dicts) ? res.base_dicts : []
    loaded.value = true
  }

  // loadDict 读系统级词典（目录 / 基础词典清单 / 自定义词全文）。
  async function loadDict() {
    loading.value = true
    try {
      const env = await mq.emit(MsgTopics.vftsDictGet, {})
      const res = env && env.backend && env.backend.result
      if (res && res.ok === false) throw new Error(res.error || 'vfts.dict.get failed')
      applyDict(res)
    } finally {
      loading.value = false
    }
  }

  // saveDict 保存系统级自定义词（后端同时自动调度强制重建）。
  async function saveDict() {
    saving.value = true
    try {
      const env = await mq.emit(MsgTopics.vftsDictSet, { user_dict: userDict.value })
      const res = env && env.backend && env.backend.result
      if (!res || res.ok !== true) throw new Error((res && res.error) || 'vfts.dict.set failed')
      if (typeof res.word_count === 'number') wordCount.value = res.word_count
      return true
    } finally {
      saving.value = false
    }
  }

  // reindex 手动触发全量重建（后台进行，立即回执 started）。
  async function reindex() {
    reindexing.value = true
    try {
      const env = await mq.emit(MsgTopics.vftsReindex, {})
      const res = env && env.backend && env.backend.result
      if (!res || res.ok !== true) throw new Error((res && res.error) || 'vfts.reindex failed')
      return true
    } finally {
      reindexing.value = false
    }
  }

  return {
    dictDir, userDictPath, userDict, wordCount, baseDicts, loaded,
    loading, saving, reindexing,
    loadDict, saveDict, reindex,
  }
}
