let seq = 0

export function uid(prefix = 'n') {
  seq += 1
  return `${prefix}_${Date.now().toString(36)}${seq.toString(36)}`
}

export function createLeaf(view = null, title = '') {
  return { id: uid('leaf'), type: 'leaf', title, view }
}

/** direction: 'row'（左右分割） | 'column'（上下分割） */
export function createSplit(direction, children) {
  const size = children.length ? 1 / children.length : 1
  return {
    id: uid('split'),
    type: 'split',
    direction,
    sizes: children.map(() => size),
    children,
  }
}

export function isLeaf(node) {
  return !!node && node.type === 'leaf'
}

export function isSplit(node) {
  return !!node && node.type === 'split'
}

/** 深度优先遍历 */
export function walk(node, fn) {
  if (!node) return
  fn(node)
  if (isSplit(node)) node.children.forEach((c) => walk(c, fn))
}

export function collectLeaves(node) {
  const out = []
  walk(node, (n) => {
    if (isLeaf(n)) out.push(n)
  })
  return out
}

export function findNode(root, id) {
  if (!root || !id) return null
  if (root.id === id) return root
  if (isSplit(root)) {
    for (const child of root.children) {
      const found = findNode(child, id)
      if (found) return found
    }
  }
  return null
}

export function findLeaf(root, id) {
  if (!id) return null
  const node = findNode(root, id)
  return node && isLeaf(node) ? node : null
}

export function findParent(root, id) {
  if (isSplit(root)) {
    if (root.children.some((c) => c.id === id)) return root
    for (const child of root.children) {
      const found = findParent(child, id)
      if (found) return found
    }
  }
  return null
}

/** 从根到目标节点的路径（含目标节点） */
export function pathTo(root, id) {
  if (!root) return []
  if (root.id === id) return [root]
  if (isSplit(root)) {
    for (const child of root.children) {
      const sub = pathTo(child, id)
      if (sub.length) return [root, ...sub]
    }
  }
  return []
}

/**
 * 把 targetId 所在区域一分为二（就地修改传入的树，并返回新的根）。
 * - 目标是叶子：替换为一个 split 容器，产生两个区。
 * - 目标是容器：统一方向后追加一个新区域。
 * 返回新产生的区域 id；未找到目标时 newLeafId 为 null。
 */
export function splitNode(root, targetId, direction = 'row') {
  const newLeaf = createLeaf(null, '')
  let created = false

  /** 在树中查找目标并替换，返回替换后的节点（可能被包装成新容器） */
  const visit = (node) => {
    if (!node) return node
    if (node.id === targetId) {
      created = true
      if (isSplit(node)) {
        node.children.push(newLeaf)
        node.direction = direction
        node.sizes = rebalance(node.children.length)
        return node
      }
      return createSplit(direction, [node, newLeaf])
    }
    if (isSplit(node)) {
      for (let i = 0; i < node.children.length; i += 1) {
        const before = node.children[i]
        const after = visit(before)
        if (created) {
          if (after !== before) node.children[i] = after
          return node
        }
      }
    }
    return node
  }

  const nextRoot = visit(root)
  return { root: created ? nextRoot : root, newLeafId: created ? newLeaf.id : null }
}

function rebalance(count) {
  return Array.from({ length: count }, () => 1 / count)
}

/**
 * 删除区域；容器只剩一个子节点时自动塌陷。
 * 返回新的根；目标不存在时原样返回。
 */
export function removeNode(root, id) {
  if (!root) return root
  if (root.id === id) return null
  if (!isSplit(root)) return root

  const idx = root.children.findIndex((c) => c.id === id)
  if (idx >= 0) {
    root.children.splice(idx, 1)
    root.sizes = rebalance(root.children.length)
    if (root.children.length === 1) return root.children[0]
    if (root.children.length === 0) return null
    return root
  }

  for (let i = 0; i < root.children.length; i += 1) {
    const before = root.children[i]
    const after = removeNode(before, id)
    if (after === before) continue
    if (after === null) {
      root.children.splice(i, 1)
      root.sizes = rebalance(root.children.length)
      if (root.children.length === 1) return root.children[0]
      if (root.children.length === 0) return null
      return root
    }
    root.children[i] = after
    return root
  }
  return root
}

/** 找到所有承载指定 view 的区域（默认自由 tabs 区） */
export function findTabsHosts(root, viewKey = 'free-tabs') {
  return collectLeaves(root).filter((l) => l.view?.viewKey === viewKey)
}

export function cloneTree(value) {
  return JSON.parse(JSON.stringify(value))
}
