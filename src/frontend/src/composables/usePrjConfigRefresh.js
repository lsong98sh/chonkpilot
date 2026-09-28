/**
 * 设置页统一订阅 `data-prj-config-refresh`（键过滤 + 突发合并 + 保存守卫）。
 *
 * 收敛机制与依据见 `utils/prjConfigRefresh.js`；4 个设置页
 * （ContextConfig / CodegraphConfig / VftsConfig / HistoryConfig）经本 composable
 * 走**同一机制**（[41 I-138] 收敛；批量写已使一次保存只发 1 条广播，见 61 §3.1）。
 *
 * @param {object} o
 * @param {string[]} [o.keys] 关注键（精确）
 * @param {string[]} [o.prefixes] 关注键前缀（如 `memory.category.`）
 * @param {() => void} o.reload 重载函数
 * @param {() => boolean} o.isSaving 保存中判定（真 → 丢弃本轮广播）
 * @returns {() => void} 退订并取消挂起重载（供 onUnmounted 收集）
 */
import { onDataRefresh } from '../utils/dataClient'
import { createPrjConfigCoalescer, makePrjKeysMatcher } from '../utils/prjConfigRefresh'

export function usePrjConfigRefresh({ keys = [], prefixes = [], reload, isSaving }) {
  const match = (keys.length || prefixes.length) ? makePrjKeysMatcher(keys, prefixes) : undefined
  const coalescer = createPrjConfigCoalescer(reload, { isSaving, match })
  const unsubscribe = onDataRefresh('prj-config', (payload) => coalescer.onRefresh(payload))
  return () => {
    unsubscribe()
    coalescer.dispose()
  }
}
