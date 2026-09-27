/**
 * 索引排除判定客户端（`data-index-ignored` 只读数据面；61-消息一览 §3.6）。
 *
 * 用途：文件树把「被索引排除规则命中」的条目灰显。判定口径与引擎实际索引范围严格一致
 * （祖先目录剪枝、`!` 反选、内置强制排除），本模块**不自己匹配规则**，只作请求/应答封装。
 *
 * 语义要点：
 *   - 入参 `paths` = **workdir 相对路径**（`/` 分隔），一次批量请求（内部去重）；
 *   - 应答 `{codegraph:{enabled,ignored}, vfts:{enabled,ignored}, truncated?}`；
 *     `ignored` = 入参中确被判排除者（原样回显）。**仅统计 `enabled=true` 的引擎**（引擎未启用
 *     其 `ignored` 恒为空 → 不产生灰显），两引擎取**并集**（被任一已启用引擎排除即视为排除项）；
 *   - `truncated=true` = 入参超上限、仅前 N 条被判定 → 调用方不得据此缓存「已判定」；
 *   - 失败 / 主题不可用 / 应答异常 → 返回空集（**静默降级为不灰显**，不抛错、不打印 console.error）。
 *
 * `mq.emit` 经 `opts.emit` 可注入 → 纯逻辑与降级路径均可单测（不触网）。
 */
import mq from './mq.js'

/** `data-index-ignored`（只读面；instance_id 由 mq.emit 统一注入）。 */
export const INDEX_IGNORED_TOPIC = 'data-index-ignored'

/** 参与判定的引擎名（与后端 indexignored.Engine* 一致）。 */
const ENGINES = ['codegraph', 'vfts']

/**
 * 绝对路径 → workdir 相对路径（`/` 分隔）；不在 workdir 内 / 空 → ''。
 * Windows 下盘符大小写可能不一致 → 前缀比较不区分大小写（返回入参原样切片）。
 */
export function toWorkdirRelPath(absPath, workDir) {
  const wd = String(workDir || '').replace(/\\/g, '/').replace(/\/+$/, '')
  const p = String(absPath || '').replace(/\\/g, '/')
  if (!wd || p.length <= wd.length) return ''
  const head = p.slice(0, wd.length)
  if (head !== wd && head.toLowerCase() !== wd.toLowerCase()) return ''
  const rest = p.slice(wd.length)
  if (!rest.startsWith('/')) return ''
  return rest.slice(1)
}

/**
 * 从 `data-index-ignored` 结果提取「被排除」相对路径集合：
 * 仅合并 `enabled === true` 的引擎的 `ignored`（并集）；缺键 / 非法 / 非数组 → 跳过。异常输入 → 空集。
 */
export function ignoredSetFromResult(result) {
  const out = new Set()
  if (!result || typeof result !== 'object') return out
  for (const engine of ENGINES) {
    const e = result[engine]
    if (!e || typeof e !== 'object' || e.enabled !== true) continue
    const list = Array.isArray(e.ignored) ? e.ignored : []
    for (const p of list) {
      if (typeof p === 'string' && p) out.add(p)
    }
  }
  return out
}

/**
 * 批量判定一批 workdir 相对路径是否被排除（**一次请求**，内部去重保序）。
 * 失败 / 主题不可用 / 应答异常 → `{ignored: Set(), truncated: false}`（静默不灰显）。
 *
 * @param {string[]} paths workdir 相对路径（'/' 分隔）
 * @param {{emit?: Function, timeout?: number}} [opts] `emit` 可注入（单测用），缺省 `mq.emit`
 * @returns {Promise<{ignored: Set<string>, truncated: boolean}>}
 */
export function fetchIndexIgnored(paths, opts = {}) {
  const list = Array.isArray(paths)
    ? Array.from(new Set(paths.filter(p => typeof p === 'string' && p)))
    : []
  const empty = { ignored: new Set(), truncated: false }
  if (list.length === 0) return Promise.resolve(empty)
  const emit = opts.emit || mq.emit
  let pending
  try {
    pending = Promise.resolve(emit(INDEX_IGNORED_TOPIC, { paths: list }, { timeout: opts.timeout || 15000 }))
  } catch (_) {
    return Promise.resolve(empty)
  }
  return pending.then((env) => {
    const backend = env && env.backend
    if (!backend || backend.ok === false) return empty
    const result = backend.result && typeof backend.result === 'object' ? backend.result : {}
    if (result.ok === false || (backend.errors && backend.errors[0])) return empty
    return { ignored: ignoredSetFromResult(result), truncated: result.truncated === true }
  }).catch(() => empty)
}

export default { INDEX_IGNORED_TOPIC, toWorkdirRelPath, ignoredSetFromResult, fetchIndexIgnored }
