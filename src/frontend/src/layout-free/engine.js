/**
 * 自由布局容器 · 大组件门面（UILAYOUT-1 §3.2，集成唯一入口）。
 *
 * ```js
 * const engine = createLayoutEngine({
 *   mq,             // 宿主 mq（emit/on/off）；缺省 = 内置 standalone mq
 *   views,          // { definitions: [...], resolve(viewKey) | components: {viewKey: Component} }
 *   persist,        // { load(), save(doc) }（本阶段仅持有，落库接线见 §7 P3）
 *   i18n,           // t(key, params)；缺省恒等
 *   iconResolver,   // (name) => svgString | null（接管图标取值）
 *   iconNames,      // 图标名清单（图标选择器分组）
 *   components,     // { Icon, SplitPanel } —— 去重：复用宿主既有实现（P1③）
 *   dialog,         // 宿主全局对话框服务（去重：既有 DialogManager 单例）
 * })
 * engine.install(app)   // 宿主须在 app.use(pinia) 之后调用
 * ```
 *
 * 分层：机制层只认「区域树 + 命令 + 配置协议」，不认识任何宿主区块；
 * 区块↔viewKey↔落点由适配层（`src/frontend/src/layout/`，§7 P3）负责。
 *
 * 本阶段（P0/P1）只做门面本身：`install` 注册 v-mq / 全局去重组件并装配 mq 订阅；
 * host 装配（注册表 / 命令落点 / 持久化 / MainLayout 换装）属 P3。
 */
import { mqPlugin as standaloneMqPlugin } from './utils/mq.js'
import { getLayoutDeps, setLayoutDeps } from './deps.js'
import { installMqCommands, uninstallMqCommands } from './stores/mqCommands.js'
import { useConfigStore } from './stores/config.js'
import { useEditStore } from './stores/edit.js'
import { useRuntimeStore } from './stores/runtime.js'
import { useKeysStore } from './stores/keys.js'

export function createLayoutEngine(deps = {}) {
  setLayoutDeps(deps)
  const d = getLayoutDeps()

  return {
    /**
     * 装配到 Vue app。
     * ⚠️ 必须在 `app.use(pinia)` 之后调用（mq 订阅会取用 Pinia store）。
     */
    install(app) {
      // v-mq：宿主已装（主工程 `utils/mq` 的同协议指令）则复用，避免重复注册指令
      const installed = app._context && app._context.directives && app._context.directives.mq
      if (!installed) app.use(standaloneMqPlugin)
      // 去重复用宿主既有组件（§3.10）：注册为子系统命名域下的全局组件
      if (d.components.Icon) app.component('LfIcon', d.components.Icon)
      if (d.components.SplitPanel) app.component('LfSplitPanel', d.components.SplitPanel)
      // 装配：所有交互（UI 行为 / 编辑动作 / 运行期命令）都在这里订阅
      uninstallMqCommands()
      installMqCommands()
    },

    /** 布局域 store 实例（供诊断/测试直取，不供组件直写） */
    get state() {
      return {
        config: useConfigStore(),
        edit: useEditStore(),
        runtime: useRuntimeStore(),
        keys: useKeysStore(),
      }
    },

    /** 布局域动作（store 动作的白名单转发） */
    get actions() {
      const config = useConfigStore()
      const edit = useEditStore()
      const runtime = useRuntimeStore()
      return {
        save: () => config.save(),
        revert: () => config.revert(),
        resetAll: () => config.resetAll(),
        importJson: (text) => config.importJson(text),
        exportJson: () => config.exportJson(),
        openEditor: (tab) => edit.openEditor(tab),
        closeEditor: () => edit.closeEditor(),
        openDrawer: (d) => runtime.openDrawer(d),
        closeDrawer: (id) => runtime.closeDrawer(id),
        setHint: (text) => runtime.setHint(text),
      }
    },

    /** 首页渲染用布局树：结构取 live、节点背景取 draft（§3.6 编辑态预览语义） */
    get renderLayout() {
      return useConfigStore().liveLayoutWithDraftBackgrounds
    },

    /** 栏渲染对象（条目取 live、背景取 draft） */
    renderBar(barKey) {
      return useConfigStore().renderBar(barKey)
    },

    get editing() {
      return useEditStore().open
    },

    get selectedNodeId() {
      return useEditStore().selectedNodeId
    },

    /** 保存前校验（引用完整性 / 单例占用 / min ≤ max） */
    validate() {
      return useConfigStore().validate()
    },
  }
}
