import { onMounted, onUnmounted } from 'vue'
import { getUserConfig } from '../api/config'
import { onDataRefresh } from '../utils/dataClient'
import { refreshUserConfig } from '../utils/llmConfigRefresh'
import mq from '../utils/mq'
import { EventNames } from '../events/event-names'

/**
 * 用户配置刷新的订阅与通知（用户视角缺陷批 1 – 问题 A/B；口径变更 2026-09-20）。
 *
 * 用途：把「用户配置已变化」（usr llms / 服务地址 / API Key 等保存后）告知调用方 ——
 * 发送失败提示（`ChatPanel.llmSetupFailed`）据此**配置变化即立即消失**，不必等下一次发送。
 *
 * 订阅 **既有消息面**（不新增主题）：`data-user-config-refresh`（`onDataRefresh('user-config')`）
 * 与 IDE `config-refresh`；每次刷新后回调调用方（附最新 usr `llms` 快照）。
 * 刷新动作本身 = `utils/llmConfigRefresh.js`（纯函数、可直测）。
 *
 * 规范：状态逻辑封装为 useXxx composable；不使用 watch/watchEffect 监听，改订阅既有消息面。
 */

/**
 * 订阅用户配置刷新（data-user-config-refresh + IDE config-refresh）并在每次刷新后回调。
 * @param {(llms: Array) => void} [onConfigRefresh] - 配置变化回调（可选；无参 = 仅订阅）
 */
export function useLLMSetup(onConfigRefresh) {
  const refresh = refreshUserConfig(getUserConfig, onConfigRefresh)
  const unsubs = []

  onMounted(() => {
    refresh()
    unsubs.push(onDataRefresh('user-config', refresh))
    unsubs.push(mq.on(EventNames.configRefresh, refresh))
  })
  onUnmounted(() => {
    unsubs.forEach(fn => fn())
    unsubs.length = 0
  })
}
