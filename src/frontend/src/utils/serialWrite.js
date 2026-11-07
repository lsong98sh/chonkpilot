/**
 * 串行 + 合并最新的写队列（opened-files 落库顺序保障）。
 *
 * 背景（2026-11-07 实跑定位）：启动恢复逐文件重放 fileOpen（a.txt → b.txt）时，两次
 * `gui.ui.save` 在同一轮事件循环内发出；persist 侧并发处理、**落库顺序不保证** →
 * 先发出的 `[a]` 可能后落地，覆盖后发出的 `[a,b]`，使 `prj opened-files` 退化为单文件，
 * 下次启动只恢复一个页签。
 *
 * 本队列保证：
 *   1. `write` **串行**执行（上一次 resolve 后才发下一次）→ 最后发出的值最后落库；
 *   2. 上一次写尚未开始时，多次 push 只保留**最新**一份（同轮 a→b 重放合并为一次全量写）；
 *   3. 单次 `write` 失败不会中断队列（失败被吞掉，与原先 `.catch(() => {})` 同口径）。
 *
 * @template T
 * @param {(value: T) => Promise<any>} write 单次写入函数
 * @returns {(value: T) => Promise<void>} push：入队一份「最新值」并返回本轮队列尾
 */
export function createLatestWriteQueue(write) {
  let chain = Promise.resolve()
  let pending
  let hasPending = false
  return function push(value) {
    pending = value
    hasPending = true
    chain = chain.then(() => {
      if (!hasPending) return
      const next = pending
      pending = undefined
      hasPending = false
      return Promise.resolve()
        .then(() => write(next))
        .catch(() => {})
    })
    return chain
  }
}