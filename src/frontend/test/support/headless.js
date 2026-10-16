/**
 * 测试支撑 · 无 DOM 的自定义渲染器（node:test 下挂 Vue 组件）。
 *
 * Vue 3 的 `createRenderer(hostOps)` 允许把组件树挂到**任意宿主对象树**上
 * （`@vue/runtime-test` 同法）——于是不需要 jsdom，也能拿到真实的生命周期钩子
 * （`onMounted` / `onUnmounted`）与真实的重渲染 diff（保活断言靠的就是"没触发 onUnmounted"）。
 *
 * 同时提供被测组件需要的浏览器全局桩（`document` / `window` / `ResizeObserver` /
 * `getComputedStyle`）——这些组件在 `onMounted` 里会装 ResizeObserver 与全局监听。
 */

function createEl(tag) {
  const el = {
    tag,
    parent: null,
    children: [],
    props: {},
    style: {},
    text: '',
    offsetWidth: 0,
    offsetHeight: 0,
    getBoundingClientRect: () => ({ left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }),
    // Teleport 先在**挂载容器**上按选择器找目标（容器是自定义宿主对象）→ 命中 `body` 时
    // 交回全局 `document.body`（与 `document.querySelector` 同一元素）
    querySelector: (sel) => (sel === 'body' && globalThis.document ? globalThis.document.body : null),
    querySelectorAll: () => [],
    closest: () => null,
    addEventListener() {},
    removeEventListener() {},
    setAttribute() {},
    removeAttribute() {},
  }
  return el
}

/** 自定义宿主操作（Vue `createRenderer` 契约的最小实现） */
const hostOps = {
  patchProp(el, key, _prev, next) {
    el.props[key] = next
  },
  insert(child, parent, anchor = null) {
    child.parent = parent
    const i = anchor ? parent.children.indexOf(anchor) : -1
    if (i >= 0) parent.children.splice(i, 0, child)
    else parent.children.push(child)
  },
  remove(child) {
    if (!child) return
    const p = child.parent
    if (!p) return
    const i = p.children.indexOf(child)
    if (i >= 0) p.children.splice(i, 1)
    child.parent = null
  },
  createElement: (type) => createEl(type),
  createText: (text) => ({ tag: '#text', text, parent: null }),
  createComment: (text) => ({ tag: '#comment', text, parent: null }),
  setText(node, text) {
    node.text = text
  },
  setElementText(node, text) {
    node.children = [{ tag: '#text', text, parent: node }]
  },
  parentNode: (node) => node.parent || null,
  nextSibling(node) {
    const p = node.parent
    if (!p) return null
    const i = p.children.indexOf(node)
    return p.children[i + 1] || null
  },
  querySelector: () => null,
  setScopeId() {},
  insertStaticContent() {
    const el = createEl('static')
    return [el, el]
  },
}

/** 被测组件的浏览器全局桩（幂等；返回还原函数） */
export function installDomStubs() {
  const saved = {}
  const keys = ['document', 'window', 'ResizeObserver', 'getComputedStyle']
  for (const k of keys) saved[k] = globalThis[k]

  const doc = {
    addEventListener() {},
    removeEventListener() {},
    createElement: (tag) => createEl(tag),
    // Teleport 目标查找按字符串选择器（如 `body`）→ 必须返回**同一**元素（每次新建会让
    // teleport 后的主体与卸载时的父节点不是同一个）
    querySelector: () => doc.body,
    body: createEl('body'),
  }
  globalThis.document = doc
  globalThis.window = {
    addEventListener() {},
    removeEventListener() {},
    innerWidth: 1280,
    innerHeight: 800,
  }
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  globalThis.getComputedStyle = () => ({ getPropertyValue: () => '', paddingRight: '0px', paddingBottom: '0px' })

  return () => {
    for (const k of keys) {
      if (saved[k] === undefined) delete globalThis[k]
      else globalThis[k] = saved[k]
    }
  }
}

export { createEl, hostOps }
