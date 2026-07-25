import { createApp, type Component, type VNode, reactive, h } from 'vue'
import DialogShell from './DialogShell.vue'
import type { DialogHandle, DialogOptions, DialogState } from '../../types/dialog'

interface DialogInstance {
  id: string
  app: ReturnType<typeof createApp>
  container: HTMLDivElement
}

let instanceIdCounter = 0

class DialogManager {
  private dialogs: Map<string, DialogInstance> = new Map()
  private zIndex = 1000

  /**
   * Show a dialog with a VNode as content.
   * Usage: dialog.show(h(MyComponent, { prop: value }), { title: 'Dialog' })
   */
  show(vnode: VNode, options: DialogOptions): DialogHandle
  /**
   * Show a dialog with a component definition as content.
   * Usage: dialog.show(MyComponent, { title: 'Dialog' })
   */
  show(component: Component, options: DialogOptions): DialogHandle
  show(componentOrVNode: Component | VNode, options: DialogOptions): DialogHandle {
    const id = `dialog_${++instanceIdCounter}`
    const currentZIndex = ++this.zIndex

    // Determine if it's a VNode or Component
    const isVNode = typeof componentOrVNode === 'object' && 'type' in componentOrVNode

    const container = document.createElement('div')
    document.body.appendChild(container)

    let shellRef: any = null
    let currentState: DialogState = 'normal'

    // Capture DialogManager instance for use inside createApp render() where `this` is the Vue component
    const manager = this

    const app = createApp({
      render() {
        const onDialogAction = (action: string, payload?: any) => {
          if (action === 'maximize' || action === 'minimize' ||
              action === 'restore' || action === 'collapse') {
            currentState = action === 'maximize' ? 'maximized'
              : action === 'minimize' ? 'minimized'
              : action === 'collapse' ? 'collapsed'
              : 'normal'
          }
          // Always pass a default empty object for payload safety
          options.onAction?.(action as any, payload ?? {})
          if (action === 'closed') {
            manager.dialogs.delete(id)
          }
        }

        if (isVNode) {
          // VNode mode: pass VNode as default slot
          return h(DialogShell, {
            ref: (r: any) => { shellRef = r },
            dialogId: id,
            options,
            zIndex: currentZIndex,
            onDialogAction,
          }, {
            default: () => componentOrVNode as VNode,
          })
        }
        // Component mode: pass component definition as prop
        return h(DialogShell, {
          ref: (r: any) => { shellRef = r },
          dialogId: id,
          options,
          component: componentOrVNode as Component,
          zIndex: currentZIndex,
          onDialogAction,
        })
      },
    })

    app.mount(container)

    this.dialogs.set(id, { id, app, container })

    const handle: DialogHandle = {
      close: () => this.close(id),
      minimize: () => shellRef?.minimize?.(),
      restore: () => shellRef?.restore?.(),
      collapse: () => shellRef?.collapse?.(),
      expand: () => shellRef?.expand?.(),
      setTitle: (title: string) => shellRef?.setTitle?.(title),
      setOptions: (opts: Partial<DialogOptions>) => {
        Object.assign(options, opts)
      },
      getState: () => shellRef?.getState?.() || currentState,
    }

    return handle
  }

  private close(id: string) {
    const instance = this.dialogs.get(id)
    if (!instance) return
    try {
      instance.app.unmount()
    } catch { /* already unmounted */ }
    if (instance.container.parentNode) {
      instance.container.parentNode.removeChild(instance.container)
    }
    this.dialogs.delete(id)
  }

  closeAll() {
    for (const [id] of this.dialogs) {
      this.close(id)
    }
  }

  getCount(): number {
    return this.dialogs.size
  }
}

export const dialog = new DialogManager()