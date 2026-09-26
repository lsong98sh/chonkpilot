// 简易行级 unified diff 生成器（替代 Monaco diff editor 的渲染输入）
// 输出标准 Git 风格 unified diff 文本，供 flyfish renderer-text 的 patch
// 渲染（diff2html）消费。只读预览场景够用，不做合并/补丁应用。

const CONTEXT = 3

function splitLines(text) {
  const arr = (text == null ? '' : String(text)).replace(/\r\n/g, '\n').split('\n')
  while (arr.length && arr[arr.length - 1] === '') arr.pop()
  return arr
}

// 中间片段 LCS 行 diff（超大片段降级为整块替换）
function lcsOps(a, b) {
  const n = a.length
  const m = b.length
  if (n > 1200 || m > 1200) {
    const ops = []
    for (const t of a) ops.push({ type: 'del', text: t })
    for (const t of b) ops.push({ type: 'add', text: t })
    return ops
  }
  // dp[i][j]: a[i..] 与 b[j..] 的 LCS 长度
  const dp = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1))
  for (let i = n - 1; i >= 0; i--) {
    const row = dp[i]
    const next = dp[i + 1]
    const ai = a[i]
    for (let j = m - 1; j >= 0; j--) {
      row[j] = ai === b[j] ? next[j + 1] + 1 : (next[j] >= row[j + 1] ? next[j] : row[j + 1])
    }
  }
  const ops = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      ops.push({ type: 'same', text: a[i] })
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      ops.push({ type: 'del', text: a[i] })
      i++
    } else {
      ops.push({ type: 'add', text: b[j] })
      j++
    }
  }
  while (i < n) { ops.push({ type: 'del', text: a[i] }); i++ }
  while (j < m) { ops.push({ type: 'add', text: b[j] }); j++ }
  return ops
}

/**
 * 生成 unified diff 文本。
 * @param {string} oldText 旧版本内容
 * @param {string} newText 新版本内容
 * @param {string} filename 文件名（仅用于 ---/+++ 头，默认 'file'）
 * @returns {string} 标准 unified diff；无差异返回 ''
 */
export function buildUnifiedDiff(oldText, newText, filename = 'file') {
  const a = splitLines(oldText)
  const b = splitLines(newText)

  // 前后缀裁剪，缩小 LCS 范围
  let start = 0
  while (start < a.length && start < b.length && a[start] === b[start]) start++
  let endA = a.length
  let endB = b.length
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) { endA--; endB-- }

  const ops = []
  for (let k = 0; k < start; k++) ops.push({ type: 'same', text: a[k] })
  ops.push(...lcsOps(a.slice(start, endA), b.slice(start, endB)))
  for (let k = endA; k < a.length; k++) ops.push({ type: 'same', text: a[k] })

  // 给每个 op 标注新旧行号（1-based）
  let aNo = 1
  let bNo = 1
  const numbered = ops.map((op) => {
    const o = { ...op, aNo, bNo }
    if (op.type !== 'add') aNo++
    if (op.type !== 'del') bNo++
    return o
  })

  // 收集 change 块（带上下文），合并相邻块
  const blocks = []
  let i = 0
  while (i < numbered.length) {
    if (numbered[i].type === 'same') { i++; continue }
    let s = i
    while (i < numbered.length && numbered[i].type !== 'same') i++
    let e = i
    // 尾部补上下文；若上下文后紧邻新的 change，一并并入
    while (e < numbered.length && numbered[e].type === 'same') {
      const ctxEnd = Math.min(numbered.length, e + CONTEXT)
      let hasChange = false
      for (let k = e; k < ctxEnd; k++) {
        if (numbered[k].type !== 'same') { hasChange = true; break }
      }
      if (!hasChange) break
      e = ctxEnd
      while (e < numbered.length && numbered[e].type !== 'same') e++
    }
    blocks.push([s, e])
  }
  // 合并重叠块（相邻块的上下文重叠）
  const merged = []
  for (const [s, e] of blocks) {
    if (merged.length && s <= merged[merged.length - 1][1] + 1) {
      merged[merged.length - 1][1] = e
    } else {
      merged.push([s, e])
    }
  }

  if (!merged.length) return ''

  const lines = []
  lines.push(`--- a/${filename}`)
  lines.push(`+++ b/${filename}`)
  for (const [s, e] of merged) {
    const seg = numbered.slice(s, e)
    const first = seg[0]
    let aLen = 0
    let bLen = 0
    for (const op of seg) {
      if (op.type !== 'add') aLen++
      if (op.type !== 'del') bLen++
    }
    lines.push(`@@ -${first.aNo},${aLen} +${first.bNo},${bLen} @@`)
    for (const op of seg) {
      if (op.type === 'same') lines.push(` ${op.text}`)
      else if (op.type === 'del') lines.push(`-${op.text}`)
      else lines.push(`+${op.text}`)
    }
  }
  return lines.join('\n') + '\n'
}
