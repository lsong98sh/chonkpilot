// 回归守卫：顶部检索下拉「选中命中项」必须**打开预览区**（2026-09-27 修 bug）
//
// 原实现只发 `file-search`（FileTree 侧仅把它写进树内过滤词 `searchQuery`），
// 结果：点下拉项后预览区不显示任何内容。现改为同时发
//   - `file-open`  { path, temporary: true } → 预览区打开（临时页签）
//   - `file-search`{ path }                  → 文件树定位（过滤/展开）
// 且命中项路径由后端 `gui.search` 归一为 **workdir 相对**（与 file-open 契约一致）。
//
// 口径参考：docs/spec/60-reference/61-消息一览.md `gui.search` / `file-open`。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (p) => readFileSync(join(HERE, '..', 'src', p), 'utf8')

function fnBody(src, name) {
  const s = src.replace(/\r\n/g, '\n') // 源码可能为 CRLF，统一后再定位
  const i = s.indexOf(`function ${name}(`)
  assert.ok(i >= 0, `未找到函数 ${name}`)
  const j = s.indexOf('\n}\n', i)
  assert.ok(j > i, `未找到函数 ${name} 的结束`)
  return s.slice(i, j + 3)
}

test('顶部检索：选中命中项 → 预览区打开（file-open）+ 树内定位（file-search）', () => {
  const src = read('views/toolbar/Toolbar.vue')

  const sel = fnBody(src, 'handleSearchSelect')
  assert.match(sel, /EventNames\.fileOpen/, 'handleSearchSelect 必须发 file-open（否则预览区不显示）')
  assert.match(sel, /path:\s*item\.path/, 'file-open 需带命中项 path')
  assert.match(sel, /temporary:\s*true/, '检索打开应为临时页签（与树内单击同口径）')
  assert.match(sel, /EventNames\.fileSearch/, 'tree 定位仍保留')

  const ent = fnBody(src, 'handleSearchEnter')
  assert.match(ent, /EventNames\.fileOpen/, '回车打开首个命中项同样必须走 file-open')
  assert.match(ent, /temporary:\s*true/)
})

test('gui.search 结果路径 = workdir 相对（与 file-open 契约一致）', () => {
  // 后端三源（file / vfts / codegraph）都必须经 searchRelPath 归一；
  // 绝对路径直接塞给 file-open 会与树内打开的同一文件产生两个页签。
  const go = readFileSync(
    join(HERE, '..', '..', 'lib', 'gui', 'bridge', 'local.go'), 'utf8'
  )
  const hits = go.match(/"path":\s*searchRelPath\(/g) || []
  assert.equal(hits.length, 3, `file/vfts/codegraph 三源都必须归一（实得 ${hits.length}）`)
  assert.match(go, /func searchRelPath\(workDir, p string\) string/, 'searchRelPath 助手须存在')
})
