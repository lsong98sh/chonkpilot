/**
 * 启动期 init-data 预取/消费状态机（2026-09-22，B2 布局时序修复）。
 *
 * 用途：主视图（MainLayout）挂载受**实例认领**（instance-claim）门控，而布局/UI 恢复用的
 * `gui.init-data` 过去在 MainLayout.onMounted 才发起 → 「认领往返 + init-data 往返」串行；
 * 修法 = 引导期（App setup）**与认领并行**发起一次预取，主视图挂载时消费 → 恢复随首帧生效。
 *
 * 语义（三条，缺一不可）：
 *   - **单飞**：并发/重复 `prefetch()` 共享同一次在飞请求（不放大往返）；
 *   - **一次性消费**：`consume()` 取走并清空在飞（同一结果只服务首个消费者）；
 *   - **回退**：无预取 / 已被消费 / 预取失败 → `consume()` 重新实时读取（行为同改前，
 *     失败时预取自身清空在飞，避免把失败结果缓存给后续消费者）。
 *
 * 抽为**纯模块**（零 import、无组件实例依赖）→ `npm test` 可直测（同 `utils/llmConfigRefresh.js`
 * 的既有做法，见 [42 §2 (130)]）。
 */

/**
 * @param {() => Promise<any>} load 实际读取函数（生产 = `gui.init-data` 往返）
 * @returns {{ prefetch: () => Promise<any>, consume: () => Promise<any> }}
 */
export function createInitDataPrefetch(load) {
  let inflight = null
  return {
    /** 启动期预取（幂等：在飞则复用）。失败 → 清空在飞并抛出（不再缓存失败结果）。 */
    prefetch() {
      if (inflight) return inflight
      inflight = load().catch((e) => {
        inflight = null
        throw e
      })
      return inflight
    },
    /** 取走预取结果（一次性）；无预取 / 已消费 / 预取失败 → 回落实时读取。 */
    consume() {
      const p = inflight
      inflight = null
      return p || load()
    },
  }
}
