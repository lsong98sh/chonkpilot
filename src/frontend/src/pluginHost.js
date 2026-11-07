/**
 * 插件 UI 宿主适配（PLUGIN-SPLIT-1 D-70 ②）。
 *
 * 前端插件 UI 已抽为同仓 npm 包 `@chonkpilot/plugin-ui`（`src/frontend/packages/plugin-ui`）；
 * 包不依赖宿主任何模块，宿主在此注入 `PluginHostPort`：
 *   - `mq`：宿主总线客户端（`utils/mq`）
 *   - `views`：插件视图注册表（`layout/views.registry.js`）
 *   - `layout`：布局域**运行态动作**（G-89 起内部命令不走 mq：插件维护页的动作按钮
 *     需要 open-tabs / open-drawer / open-dialog / toggle-drawer / close-tabs-active）
 *   - `theme`：主题订阅 / 快照（`composables/useTheme.js`）
 *   - `components`：设计系统组件（Icon / Button / Switch）
 *
 * 启动顺序：`initPluginUIHost()` 必须早于 `initPlugins()` 与任何插件视图挂载。
 */
import mq, { currentInstanceId } from '@ckfw/utils/mq.js'
import Icon from '@ckfw/components/icon/Icon.vue'
import { Button, Switch } from '@ckfw/components/ui/index.js'
import { registerPluginViews, unregisterPluginViews } from './layout/views.registry.js'
import { subscribeTheme, themeSnapshot } from './composables/useTheme.js'
import {
  openTabs,
  openDrawer,
  openDialog,
  toggleDrawer,
  closeActiveTabs,
} from '@ckfw/layout-free/stores/actions.js'
import { configurePluginUI } from '../packages/plugin-ui/src/index.js'

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
      // G-89：布局域内部命令直调域动作（插件维护页动作按钮的打开落点）
      layout: {
        openTabs,
        openDrawer,
        openDialog,
        toggleDrawer,
        closeActiveTabs,
      },
      // PLUGIN-INSTANCE-1：插件页归属 = 承载它的窗口实例（拼进 iframe URL 的 `instance_id`）。
      instance: { currentId: () => currentInstanceId() },
    },
    theme: { subscribe: subscribeTheme, snapshot: themeSnapshot },
    components: { Icon, Button, Switch },
  })
}

export * from '../packages/plugin-ui/src/index.js'
