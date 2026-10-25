/**
 * PLUGIN-UI-1：插件 iframe 资源前缀（第二段）。
 *
 * 路由已**统一**为 `/plugins/<id>/<entry>`：宿主经 UDS 向已就绪插件请求资源
 * （`plugin/v1/ui.asset.get`）。旧前缀（`plugin-ui/<id>/…`，宿主直读 `<插件目录>/webui`）
 * 已下线（一律 404），该前缀下仅保留共享桥 SDK（`plugin-ui/_sdk/plugin.js`）。
 *
 * 注：后端仍下发 `plugin.self_serve`（manifest `ui.selfServe`）字段，**保留用于遥测 / 兼容**，
 * 前端路由不再据此分流。
 *
 * `entry` 形如 `webui/config.html`（manifest `ui.views[].entry`），新路由兼容该前缀。
 */

/** 插件页资源前缀（唯一取值）。 */
export const PLUGIN_UI_PREFIX = '/plugins/'

/** 拼插件页 iframe 地址（不含查询串；`?p=<viewId>` 由调用方附加）。 */
export function pluginUIPageURL(pluginId, entry) {
  return PLUGIN_UI_PREFIX + encodeURIComponent(pluginId) + '/' + entry
}
