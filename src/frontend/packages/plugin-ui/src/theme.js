/**
 * 插件 UI 包主题端口：宿主主题订阅 / 快照（`pushTheme` 向插件 iframe 广播）。
 */
import { pluginHost } from './host.js'

export function subscribePluginTheme(cb) {
  return pluginHost().theme.subscribe(cb)
}

export function pluginThemeSnapshot() {
  return pluginHost().theme.snapshot()
}
