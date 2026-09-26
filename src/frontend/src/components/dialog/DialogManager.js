import { h, reactive, render } from 'vue'
import DialogShell from './DialogShell.vue'

class DialogManager {
  constructor() {
    this.dialogs = new Map()
    // 从 2000 起跳，与 drawer(1000)/toolbar(1100) 等应用层完全隔离，避免同层竞争
    this.zIndex = 2000
    this.mainContext = null
    this.instanceIdCounter = 0
  }

  setApp(app) {
    this.mainContext = app._context
  }

  show(componentOrVNode, options) {
    const id = `dialog_${++this.instanceIdCounter}`
    const currentZIndex = ++this.zIndex

    const isVNode = typeof componentOrVNode === 'object' && 'type' in componentOrVNode

    // 创建带 id 的 container，dialog 作为 container 的直接子元素渲染
    const container = document.createElement('div')
    container.id = id
    document.body.appendChild(container)

    let shellRef = null
    let currentState = 'normal'
    const manager = this

    // options 必须是响应式对象：DialogShell 通过 props 接收它，
    // setOptions 直接替换属性才会触发 UI 更新（裸对象 Object.assign 不响应）。
    const reactiveOptions = reactive(options)

    const dialogProps = {
      dialogId: id,
      options: reactiveOptions,
      zIndex: currentZIndex,
    }

    const widgetEvents = {
      onClose() {
        manager.close(id)
      },
      onCollapse() {
        currentState = 'collapsed'
      },
      onExpand() {
        currentState = 'normal'
      },
      onMaximize() {
        currentState = 'maximized'
      },
      onMinimize() {
        currentState = 'minimized'
      },
      onRestore() {
        currentState = 'normal'
      },
    }

    const vnode = h(DialogShell, {
      ...dialogProps,
      ...widgetEvents,
    }, isVNode
      ? { default: () => componentOrVNode }
      : { default: () => h(componentOrVNode) }
    )

    if (this.mainContext) {
      vnode.appContext = this.mainContext
    }

    render(vnode, container)

    // 注意：根级 render 时，ref 回调的 owner（currentRenderingInstance）为 null，
    // Vue 的 setRef 会因 !owner 提前 return，函数 ref 回调不会触发，shellRef 永远是 null。
    // 因此改为从 container._vnode.component 直接获取 DialogShell 组件实例。
    // defineExpose 暴露的方法（setTitle/getState/collapse/expand/minimize/maximize/restore/close）
    // 都在 component.exposed 上，component 本身没有这些方法。
    const shellInstance = container._vnode && container._vnode.component
    // shellRef 指向 defineExpose 返回的 exposed 对象（含 minimize 等所有公开方法）
    shellRef = (shellInstance && shellInstance.exposed) || null

    this.dialogs.set(id, { container, shellRef })

    return {
      close: () => this.close(id),
      minimize: () => shellRef?.minimize?.(),
      restore: () => shellRef?.restore?.(),
      collapse: () => shellRef?.collapse?.(),
      expand: () => shellRef?.expand?.(),
      setTitle: (title) => shellRef?.setTitle?.(title),
      setOptions: (opts) => { Object.assign(reactiveOptions, opts) },
      getState: () => shellRef?.getState?.() || currentState,
    }
  }

  close(id) {
    const instance = this.dialogs.get(id)
    if (!instance) {
      return
    }

    // 用 id 实时查找 container，不依赖可能失效的缓存引用
    const container = document.getElementById(id) || instance.container

    // Step 1: 尝试用 render(null) 卸载（会触发 onUnmounted）
    try {
      if (container && container._vnode) {
        render(null, container)
      }
    } catch (e) {
      console.warn(`[DialogManager] close(${id}): render(null) error:`, e)
    }

    // Step 2: 无论是否成功，强制移除 container（含其所有子 DOM）
    if (container && container.parentNode) {
      container.parentNode.removeChild(container)
    }

    // Step 3: 清理记录
    this.dialogs.delete(id)
  }

  closeAll() {
    for (const [id] of this.dialogs) {
      this.close(id)
    }
  }

  getCount() {
    return this.dialogs.size
  }
}

export const dialog = new DialogManager()
