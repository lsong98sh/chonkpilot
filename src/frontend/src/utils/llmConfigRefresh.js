/**
 * 「读一次用户配置 → 通知调用方」的刷新动作（用户视角缺陷批 1 – 问题 B，2026-09-20）。
 *
 * 用途：`useLLMSetup` 在订阅**既有消息面** `data-user-config-refresh`（`onDataRefresh('user-config')`）
 * 与 IDE `config-refresh` 后调用本工厂产出的动作 —— 用户配置一变化（在设置页配好 LLM、
 * 改了服务地址 / API Key 等）即通知调用方，使发送失败提示**立即消失**（不必等下一次发送）。
 *
 * 单独成模块（而非内联在 composable 内）：不依赖 Vue / 组件实例、无任何 import，
 * 可直接 `npm test` 直测（`test/uxBatch1.test.js` ⑤ 段）。
 */

/**
 * 构建「读一次用户配置 → 通知调用方」的刷新动作。
 * 读失败仍通知（配置刷新事件本身即「配置已变」信号 → 宁可多清一次提示）。
 * @param {() => Promise<object>} getConfig - 读取用户配置（生产 = api/config 的 getUserConfig）
 * @param {(llms: Array) => void} [onConfigRefresh] - 刷新完成回调（参数 = 最新 usr llms 快照）
 * @returns {() => Promise<void>} 刷新动作
 */
export function refreshUserConfig(getConfig, onConfigRefresh) {
  return async function refresh() {
    let llms = []
    try {
      const res = await getConfig()
      const cfg = res.config || res
      llms = Array.isArray(cfg.llms) ? cfg.llms : []
    } catch (e) {
      console.warn('[useLLMSetup] load user config failed:', e)
    }
    if (typeof onConfigRefresh === 'function') onConfigRefresh(llms)
  }
}
