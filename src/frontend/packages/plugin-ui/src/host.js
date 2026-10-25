/**
 * 宿主端口（PluginHostPort）持有者：插件 UI 包**不直接依赖宿主** mq / 视图注册表 / 主题 /
 * 设计系统组件；全部由宿主在启动时注入（见 `@chonkpilot/plugin-ui` 的 `configurePluginUI`）。
 */
let port = null

/** 注入/合并宿主端口（幂等；可多次调用覆盖同名能力）。 */
export function configurePluginHost(patch) {
  if (!patch) return
  port = { ...(port || {}), ...patch }
}

/** 取宿主端口；未注入 → 明确报错（不静默降级为宿主实现）。 */
export function pluginHost() {
  if (!port) {
    throw new Error('plugin-ui: 宿主端口未注入（宿主须先调用 configurePluginUI(host)）')
  }
  return port
}

/** 仅供测试/宿主探活：端口是否已注入。 */
export function pluginHostReady() {
  return port !== null
}
