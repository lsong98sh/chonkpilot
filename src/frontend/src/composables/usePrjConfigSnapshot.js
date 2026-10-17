// usePrjConfigSnapshot.js — prj 配置快照（状态栏栏视图 / 长任务详情页共用）：
// 取数 = 既有 `getAllConfig()`（data-prj-config-list）；刷新 = 既有 `data-prj-config-refresh`
// 广播（onDataRefresh('prj-config')）。零新增消息面；禁 watch（回调驱动 ref 重载）。
import { onMounted, onUnmounted, ref } from 'vue'
import { getAllConfig } from '../api/config'
import { onDataRefresh } from '../utils/dataClient'

export function usePrjConfigSnapshot() {
  const prjCfg = ref({})

  async function reload() {
    try {
      const res = await getAllConfig()
      const cfg = res.config || res || {}
      prjCfg.value = cfg && typeof cfg === 'object' ? cfg : {}
    } catch (_) {
      // 读失败静默（不谎报状态、不打断状态栏；下次广播/重载再试）
      prjCfg.value = {}
    }
  }

  const offs = []
  onMounted(() => {
    reload()
    offs.push(onDataRefresh('prj-config', reload))
  })
  onUnmounted(() => {
    offs.forEach((fn) => fn())
    offs.length = 0
  })

  return { prjCfg, reload }
}
