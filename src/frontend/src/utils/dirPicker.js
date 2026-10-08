/**
 * browser 形态「目录选择器」纯逻辑（无 Vue / 无 DOM；`node --test` 可直接导入）。
 *
 * 数据源 = 服务端等价面 `GET /dirs` 的扁平清单 `[{path,name,depth}]`（本 instance 的
 * work_dir 及其子目录，服务端已限作用域与规模，见 19 §8.9）。本模块只做「按层浏览」的视图
 * 计算，**只读**：当前层级只能取自清单内路径（work_dir 或其子目录），前端不拼接/伪造任意
 * 绝对路径；越界路径一律回落到 work_dir。
 */

/** 归一化路径：统一分隔符为 '/'，去掉尾部 '/'（与 `GET /dirs` 的 filepath.ToSlash 对齐）。 */
export function normPath(p) {
  return String(p == null ? '' : p).replace(/\\/g, '/').replace(/\/+$/, '')
}

/** 末级名（用于清单缺 name 时兜底显示）。 */
export function baseName(p) {
  const n = normPath(p)
  const i = n.lastIndexOf('/')
  return i >= 0 ? n.slice(i + 1) : n
}

/** 父目录（无分隔符时返回自身）。 */
export function parentOf(p) {
  const n = normPath(p)
  const i = n.lastIndexOf('/')
  return i > 0 ? n.slice(0, i) : n
}

/** p 是否等于 root 或位于 root 之下（作用域保护第二道；服务端已限 work_dir 子树）。 */
export function isWithinRoot(root, p) {
  const r = normPath(root)
  const n = normPath(p)
  if (!r || !n) return false
  if (n === r) return true
  return n.startsWith(r + '/')
}

/** 扁平清单 → path → 项 的映射（只认清单内路径；name 缺省补末级名）。 */
export function dirItemMap(dirs) {
  const map = new Map()
  for (const d of dirs || []) {
    if (!d || typeof d.path !== 'string' || d.path === '') continue
    const path = normPath(d.path)
    map.set(path, { path, name: d.name || baseName(path), depth: d.depth || 0 })
  }
  return map
}

/** current 的直接子目录（清单内、父目录 = current）。 */
export function childDirs(dirs, current) {
  const cur = normPath(current)
  const out = []
  for (const d of dirs || []) {
    if (!d || typeof d.path !== 'string' || d.path === '') continue
    const path = normPath(d.path)
    if (path !== cur && parentOf(path) === cur) {
      out.push({ path, name: d.name || baseName(path) })
    }
  }
  return out
}

/**
 * 面包屑（root → current 各层）。路径逐级向上取自清单，**不新造**；越界/断链则截断到已收层级。
 */
export function breadcrumb(dirs, root, current) {
  const r = normPath(root)
  const idx = dirItemMap(dirs)
  const crumbs = []
  let p = normPath(current)
  for (let guard = 0; guard < 64; guard++) {
    if (!isWithinRoot(r, p) || !idx.has(p)) break
    crumbs.unshift({ path: p, name: idx.get(p).name })
    if (p === r) break
    p = parentOf(p)
  }
  return crumbs
}

/**
 * 由清单构造某一层的浏览视图（供 UI 消费；纯函数便于单测）：
 * `{root, current, canUp, parent, crumbs, children}`。
 * current 不在清单内（含空）→ 回落到 root。
 */
export function buildDirLevels(dirs, workDir, current) {
  const root = normPath(workDir)
  const idx = dirItemMap(dirs)
  const cur = idx.has(normPath(current)) ? normPath(current) : root
  return {
    root,
    current: cur,
    canUp: cur !== root,
    parent: cur !== root ? parentOf(cur) : root,
    crumbs: breadcrumb(dirs, root, cur),
    children: childDirs(dirs, cur),
  }
}

/**
 * 目录选择分派（纯函数，便于单测）：
 *   - browser 形态 → `browserPick()`（服务端等价面 `GET /dirs` 选择器）；
 *   - 其余（GUI / native）→ `nativePick()`（`gui.dir.open-dialog`，**纯选择无副作用**）。
 * 返回选中路径（取消 = ''）。「以新窗口打开该目录」不在此 —— 由调用方另走 `gui.dir.open`。
 */
export function dispatchDirPick({ isBrowser, nativePick, browserPick }) {
  return isBrowser ? browserPick() : nativePick()
}
