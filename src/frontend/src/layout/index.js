/**
 * 自由布局容器 · 装配入口（UILAYOUT-1 §2 装配 / §7 P3⑤）。
 *
 * 职责（宿主侧唯一装配点）：
 *   1. 把**适配层**（区块注册表 / 命令落点 / 持久化 / i18n / 主题）与**机制层**
 *      （`layout-free/engine.js` 的大组件门面）接起来；
 *   2. `engine.install(app)` 注册 v-mq（宿主已装则复用）+ 去重组件（`LfIcon` / `LfSplitPanel`）
 *      + 装配全部 mq 订阅（UI 行为 / 编辑动作 / 运行期命令）；
 *   3. 暴露 `getLayoutEngine()` / `getLayoutPersist()` 供 `MainLayout` 读渲染树与落库。
 *
 * 调用时机（`main.js`）：`app.use(createPinia())` 与 `app.use(mqPlugin)` **之后**、`mount` 之前
 * —— mq 订阅会取用 Pinia store，且 v-mq 复用需要宿主指令已注册。
 *
 * ⚠️ 本模块 import 了宿主 i18n（`plugins/i18n`，内含 `*.json`）与 `.vue` → **不可**被 node:test
 * 直接 import；单测请只引 `layout/{mode,persist,commands,view-definitions,i18n}.js`（均为叶子/纯模块）。
 */
import { createLayoutEngine } from '../layout-free/engine.js'
import { createViewRegistry } from './views.registry.js'
import { createLayoutTranslator, setLayoutTranslator } from './i18n.js'
import { createLayoutPersist } from './persist.js'
import { i18n } from '../plugins/i18n'
import mq from '../utils/mq'
import { loadInitData, saveLayoutState } from '../api/file'
import { dialog } from '../components/dialog'
import { Icon, icons } from '../components/icon'
import { SplitPanel } from '../components/split'

let engine = null
let persist = null

/**
 * 装配自由布局容器（幂等：重复调用返回同一实例）。
 * @param {import('vue').App} app 宿主 app（须已 `use(pinia)` / `use(mqPlugin)`）
 */
export function setupLayoutEngine(app) {
  if (engine) return engine

  // i18n：容器与适配层共用同一取值函数（命名空间 `layoutFree`）
  const translate = createLayoutTranslator(i18n)
  setLayoutTranslator(translate)

  // 持久化桥（§3.9）：save → `gui.ui.save {layout:{free}}`；load → `gui.init-data` 的 layout 子对象
  persist = createLayoutPersist({ save: saveLayoutState, load: loadInitData })

  engine = createLayoutEngine({
    mq,
    views: createViewRegistry(translate),
    persist,
    i18n: translate,
    /** 图标：复用宿主 `components/icon/icons.js`（name → svg 串），不引入 lucide-static（§3.1） */
    iconResolver: (name) => icons[name] || '',
    iconNames: Object.keys(icons),
    /** 去重（§3.10）：图标 / 分栏复用宿主既有实现（注册为 `LfIcon` / `LfSplitPanel`） */
    components: { Icon, SplitPanel },
    /** 去重：复用宿主既有全局对话框服务（不再自建 DialogManager） */
    dialog,
  })
  engine.install(app)

  // 诊断/联调入口（非业务 API；不参与任何逻辑）
  if (typeof window !== 'undefined') window.__ckLayout = { engine, persist }
  return engine
}

/** 已装配的容器实例（未装配 → null）。 */
export function getLayoutEngine() {
  return engine
}

/** 已装配的持久化桥（未装配 → null）。 */
export function getLayoutPersist() {
  return persist
}
