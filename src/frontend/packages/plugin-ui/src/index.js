/**
 * `@chonkpilot/plugin-ui` —— 插件平台前端 UI 包（PLUGIN-SPLIT-1 D-70 ②）。
 *
 * 宿主注入 `PluginHostPort`：
 *   - `mq`：`{ on(topic, cb), emit(topic, payload) }`（宿主总线客户端）
 *   - `views`：`{ register(plugin), unregister(id) }`（插件视图注册表）
 *   - `theme`：`{ subscribe(cb), snapshot() }`（主题端口）
 *   - `components`：`{ Icon, Button, Switch }`（设计系统组件）
 *
 * 包内只含插件 UI 的逻辑与视图（状态、就绪态、动作面、路由、状态栏条目、管理页 / 页面宿主）；
 * 不 import 宿主任何模块（`vue` 除外）。
 */
import { configurePluginHost } from './host.js'
import { setPluginUIComponents } from './components.js'

export { configurePluginHost, pluginHost, pluginHostReady } from './host.js'
export { setPluginUIComponents, pluginUIComponents } from './components.js'
export { subscribePluginTheme, pluginThemeSnapshot } from './theme.js'

/**
 * 一次性装配端口（宿主启动时调用一次）。
 * @param {{host?: object, theme?: object, components?: object}} cfg
 */
export function configurePluginUI(cfg = {}) {
  const { host, theme, components } = cfg
  if (host) configurePluginHost(host)
  if (theme) configurePluginHost({ theme })
  if (components) setPluginUIComponents(components)
}

export * from './usePlugins.js'
export { usePluginReadiness, LOAD_TIMEOUT_MS, AUTO_RETRY_LIMIT, AUTO_RETRY_DELAY_MS } from './usePluginReadiness.js'
export { pluginAction } from './pluginAction.js'
export { pluginFailureText } from './pluginNotice.js'
export { PLUGIN_UI_PREFIX, pluginUIPageURL } from './pluginUiRoute.js'
export { pluginBarItems } from './pluginBar.js'

export { default as PluginPageHost } from './PluginPageHost.vue'
export { default as PluginManagerPage } from './PluginManagerPage.vue'
