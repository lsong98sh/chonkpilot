/**
 * dataClient — data-<domain>-<action> 消息客户端（20-gui / 61-消息一览 §3）
 *
 * 配置/场景等数据 CRUD 不走 /call（IDE 数据桥），改走 data-<domain> 消息面：
 *   publish + promise 收集：await mq.emit(`data-<domain>-<action>`, body)
 *   → 后端结果信封 {ok, result, errors}（errors 恒收集、恒 accept，发送端自查）
 *   变更广播：save/delete 后服务方广播 data-<domain>-refresh，前端 mq.on 订阅自动刷新。
 *
 * 消息映射：
 *   list   → data-<domain>-list{filter?}    → result {list}
 *   load   → data-<domain>-load{id}         → result {data}
 *   save   → data-<domain>-save{data}       → result {ok, id}
 *   delete → data-<domain>-delete{id}       → result {ok}
 *
 * 域清单：user-config / prj-config / prj-security / scenario /
 * knowledge / prompt。
 */
import mq from './mq'

/**
 * 发送 data-<domain>-<action> 并 await 收集结果（publish + promise；61-消息一览 §0.1）。
 * @param {string} domain - 数据域（user-config / prj-config / prj-security / scenario / prompt …）
 * @param {string} action - list / load / save / delete / restore
 * @param {object} body - 业务载荷（load/delete 的 id、save 的 data、list 的 filter）
 * @param {object} [opts]
 * @param {number} [opts.timeout=15000] - 超时毫秒
 * @returns {Promise<object>} result 载荷（list/data/ok 等）
 */
export function dataRequest(domain, action, body = {}, opts = {}) {
  const topic = `data-${domain}-${action}`
  const timeout = opts.timeout || 15000
  return mq.emit(topic, body, { timeout }).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(`${topic}: backend unreachable`)
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = (backend.errors && backend.errors[0]) || (p && p.error) || ''
    if (!backend.ok || emsg || p.ok === false) {
      const e = new Error(emsg || `${topic} failed`)
      e.domain = domain
      throw e
    }
    return p
  })
}

/**
 * 订阅 data-<domain>-refresh 变更广播（save/delete 后服务方主动广播，
 * 载荷 {list} 或 {id, op}）。返回取消订阅函数。
 * @param {string} domain
 * @param {(payload: object) => void} cb
 */
export function onDataRefresh(domain, cb) {
  return mq.on(`data-${domain}-refresh`, cb)
}

// 便捷方法：list / load / save / remove + onRefresh。
export const dataClient = {
  /** data-<domain>-list{filter?} → {list} */
  list: (domain, filter, opts) => dataRequest(domain, 'list', filter ? { filter } : {}, opts),
  /** data-<domain>-load{id} → {data} */
  load: (domain, id, opts) => dataRequest(domain, 'load', id !== undefined ? { id } : {}, opts),
  /** data-<domain>-save{data} → {ok, id}（有 id 更新 / 无 id 新建） */
  save: (domain, data, opts) => dataRequest(domain, 'save', { data }, opts),
  /** data-<domain>-delete{id} → {ok} */
  remove: (domain, id, opts) => dataRequest(domain, 'delete', { id }, opts),
  /** 订阅 data-<domain>-refresh 广播 */
  onRefresh: onDataRefresh,
}

export default dataClient
