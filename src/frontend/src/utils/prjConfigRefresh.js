/**
 * prj-config 变更广播的**消费侧收敛器**（[41 I-138] 收敛）。
 *
 * 背景（[61-消息一览 §3.1]）：`data-prj-config-save` 报文兼容**单键** `{data:{key,value}}` 与
 * **批量** `{data:{entries:{<key>:<value>}}}`（`src/lib/data/persist/envelope.go` ·
 * `src/lib/gui/bridge/data.go`；前端批量入口 = `api/config.js` 的 `setConfigs`）。后端写入后广播
 * `data-prj-config-refresh`：单键 = 1 条 `{id, op, list}`；**批量 = 1 条** `{id, ids, op, list}`
 * （`ids` = 全组键，`src/lib/data/internal/config/config_facade.go` 整批一次 RefreshScopedKeys）。
 * 本模块在**消费侧**兜底收敛（防订阅方仍需应对零散广播）：
 *
 *   ① 键过滤：`match(payload)` 为假 → 丢弃（无关键不触发重载，如引擎 `*.status` 回写；
 *      批量广播按键集 `ids`（回落 `id`）判定，命中任一即算命中）；
 *   ② 合并：同一突发内多条命中广播 → 只重载 1 次（读突发后的**最终**快照，消除中间快照窗口）；
 *   ③ 保存守卫：`isSaving()` 为真 → 不安排重载（本页自身保存期间，二道防线）。
 *
 * 纯逻辑（不依赖 mq / DOM）→ 可直接单测；订阅接线见 `composables/usePrjConfigRefresh.js`。
 */

/** 默认调度：宏任务尾执行（同一突发内的多次 onRefresh 合并为一次 fire）。 */
function defaultSchedule(fn) { return setTimeout(fn, 0) }
/** 默认取消调度。 */
function defaultCancel(t) { clearTimeout(t) }

/**
 * createPrjConfigCoalescer 生成合并器。
 * @param {() => void} reload 重载函数
 * @param {object} [opts]
 * @param {() => boolean} [opts.isSaving] 保存中判定（真 → 丢弃本轮广播）
 * @param {(payload: object) => boolean} [opts.match] 关注键命中判定（假 → 丢弃；缺省 = 全命中）
 * @param {(fn: Function) => any} [opts.schedule] 调度函数（测试可注入）
 * @param {(t: any) => void} [opts.cancel] 取消调度（测试可注入）
 * @returns {{ onRefresh(payload?: object): void, dispose(): void }}
 */
export function createPrjConfigCoalescer(reload, opts = {}) {
  const { isSaving, match, schedule = defaultSchedule, cancel = defaultCancel } = opts
  let timer = null
  const fire = () => {
    timer = null
    if (isSaving && isSaving()) return // 守卫在触发时刻再判一次（保存可能在调度后开始）
    reload()
  }
  return {
    onRefresh(payload) {
      if (isSaving && isSaving()) return
      if (match && !match(payload || {})) return
      if (timer !== null) return // 合并：突发已有挂起重载 → 丢弃本条
      timer = schedule(fire)
    },
    dispose() {
      if (timer !== null) { cancel(timer); timer = null }
    },
  }
}

/**
 * makePrjKeysMatcher 生成「关注键」判定：`keys` 精确命中 ∪ `prefixes` 前缀命中。
 * 广播载荷的键集 = **批量写带 `ids`（全组键）**；缺省回落单键 `id`（61 §3.1 向后兼容）。
 * 命中任一键即命中；两者皆空（旧广播无键名）→ 保守命中（宁可多刷一次，不漏刷）。
 * @param {string[]} [keys] 关注键（精确匹配）
 * @param {string[]} [prefixes] 关注键前缀
 * @returns {(payload: object) => boolean}
 */
export function makePrjKeysMatcher(keys = [], prefixes = []) {
  return (payload) => {
    const p = payload || {}
    const ids = Array.isArray(p.ids) && p.ids.length ? p.ids : (p.id ? [p.id] : [])
    if (ids.length === 0) return true
    return ids.some((id) => keys.includes(id) || prefixes.some((pre) => String(id).startsWith(pre)))
  }
}
