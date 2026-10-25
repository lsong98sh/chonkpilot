/**
 * 插件 UI 宿主适配（PLUGIN-SPLIT-1 D-70 ②）。
 *
 * 前端插件 UI 已抽为同仓 npm 包 `@chonkpilot/plugin-ui`（`src/frontend/packages/plugin-ui`）；
 * 包不依赖宿主任何模块，宿主在此注入 `PluginHostPort`：
 *   - `mq`：宿主总线客户端（`utils/mq`）
 *   - `views`：插件视图注册表（`layout/views.registry.js`）
 *   - `theme`：主题订阅 / 快照（`composables/useTheme.js`）
 *   - `components`：设计系统组件（Icon / Button / Switch）
 *
 * 启动顺序：`initPluginUIHost()` 必须早于 `initPlugins()` 与任何插件视图挂载。
 */
import mq from './utils/mq'
import Icon from './components/icon/Icon.vue'
import { Button, Switch } from './components/ui'
import { registerPluginViews, unregisterPluginViews } from './layout/views.registry.js'
import { subscribeTheme, themeSnapshot } from './composables/useTheme.js'
import { configurePluginUI } from '../../packages/plugin-ui/src/index.js'

let injected = false

/** 注入插件 UI 包所需宿主端口（幂等）。 */
export function initPluginUIHost() {
  if (injected) return
  injected = true
  configurePluginUI({
    host: {
      mq: {
        on: (...args) => mq.on(...args),
        emit: (...args) => mq.emit(...args),
      },
      views: {
        register: registerPluginViews,
        unregister: unregisterPluginViews,
      },
    },
    theme: { subscribe: subscribeTheme, snapshot: themeSnapshot },
    components: { Icon, Button, Switch },
  })
}

export * from '../../packages/plugin-ui/src/index.js'
