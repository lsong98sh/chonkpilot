/**
 * frameless 主窗口边缘 resize 命中层单测（WIN-1 / G-59，2026-10-17）。
 *
 * 覆盖：① 8 个方位命中条（4 边 + 4 角）与 cursor 映射；② mousedown → gui.window.resize{hit}
 *      （fire-and-forget，不 await）；③ 仅 !isBrowserForm() 渲染 + `-webkit-app-region: no-drag`；
 *      ④ 挂载点唯一位于 MainLayout（不散落各 view）；⑤ msgkeys / event-names 契约值。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 与 guiShowDialog / uxLayoutSplit
 * 同法：直接读 SFC 源码做契约断言。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { MsgTopics, FieldKeys, GuiWindowResizeKeys } from '../src/events/msgkeys.js'
import { EventNames } from '../src/events/event-names.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC_DIR = join(HERE, '..', 'src')
const read = (rel) => readFileSync(join(SRC_DIR, rel), 'utf8')

const COMP = read('components/shell/WindowResizeHandles.vue')
const LAYOUT = read('views/layout/MainLayout.vue')
const HITS = ['left', 'right', 'top', 'bottom', 'topleft', 'topright', 'bottomleft', 'bottomright']

test('① 8 个方位命中条：4 边 + 4 角，顺序 = 边在角前（角落优先命中）', () => {
  const m = COMP.match(/const hits = \[([^\]]+)\]/)
  assert.ok(m, '须声明 hits 数组')
  const hits = m[1].split(',').map((s) => s.trim().replace(/'/g, ''))
  assert.deepEqual(hits, HITS)
  assert.match(COMP, /v-for="hit in hits"/, '8 条命中条须由 hits 渲染')
  assert.match(COMP, /@mousedown\.prevent="onHit\(hit\)"/, 'mousedown 须 preventDefault 后发消息')
})

test('① 尺寸与光标：边 6px / 角 12×12，cursor 与方位对应', () => {
  assert.match(COMP, /\.win-resize-left,\s*\n\.win-resize-right \{[^}]*width: 6px;/, '左右边须 6px 宽')
  assert.match(COMP, /\.win-resize-top,\s*\n\.win-resize-bottom \{[^}]*height: 6px;/, '上下边须 6px 高')
  assert.match(COMP, /\.win-resize-topleft,[\s\S]*?width: 12px;\s*\n\s*height: 12px;/, '四角须 12×12')

  const cursorOf = (cls) => {
    const m = COMP.match(new RegExp(`\\.win-resize-${cls}\\s*\\{[^}]*cursor:\\s*([a-z-]+)`))
    assert.ok(m, `${cls} 缺 cursor`)
    return m[1]
  }
  assert.equal(cursorOf('left'), 'ew-resize')
  assert.equal(cursorOf('right'), 'ew-resize')
  assert.equal(cursorOf('top'), 'ns-resize')
  assert.equal(cursorOf('bottom'), 'ns-resize')
  assert.equal(cursorOf('topleft'), 'nwse-resize')
  assert.equal(cursorOf('bottomright'), 'nwse-resize')
  assert.equal(cursorOf('topright'), 'nesw-resize')
  assert.equal(cursorOf('bottomleft'), 'nesw-resize')
})

test('② mousedown → gui.window.resize{hit}（fire-and-forget，不 await）', () => {
  assert.match(COMP, /mq\.emit\(EventNames\.guiWindowResize, \{ \[GuiWindowResizeKeys\.hit\]: hit \}\)/,
    '须发 EventNames.guiWindowResize 且 payload 用签约键 hit')
  assert.doesNotMatch(COMP, /await mq\.emit/, '本消息无返回（本地事件），不得 await')
  assert.match(COMP, /function onHit\(hit\)/, '须由 onHit 统一发送')
})

test('③ 唯一形态（desktop）→ 命中层无条件渲染 + 命中条退出拖拽区', () => {
  assert.doesNotMatch(COMP, /isBrowserForm|runtimeForm/, '形态判据已删（browser 形态不存在）')
  assert.doesNotMatch(COMP, /v-if="resizeEnabled"/, '命中层不得再按形态条件渲染')
  assert.match(COMP, /-webkit-app-region: no-drag/, '命中条须 no-drag（否则被标题栏拖拽区吃掉）')
  assert.match(COMP, /z-index:\s*2147483000/, '命中层须高于布局容器/弹层')
  assert.match(COMP, /pointer-events:\s*none/, '命中层本体不得拦内容区交互')
  assert.match(COMP, /\.win-resize-handle \{[^}]*pointer-events: auto/, '命中条须可命中')
})

test('④ 挂载点：MainLayout 唯一挂载（不散落各 view）', () => {
  assert.match(LAYOUT, /import WindowResizeHandles from '\.\.\/\.\.\/components\/shell\/WindowResizeHandles\.vue'/)
  const mounts = LAYOUT.match(/<WindowResizeHandles \/>/g) || []
  assert.equal(mounts.length, 1, 'MainLayout 须且仅须挂载一次')
})

test('⑤ 契约值：msgkeys 已生成 gui.window.resize / hit，event-names 已登记', () => {
  assert.equal(MsgTopics.guiWindowResize, 'gui.window.resize')
  assert.equal(GuiWindowResizeKeys.hit, 'hit')
  assert.equal(FieldKeys.hit, 'hit')
  assert.equal(EventNames.guiWindowResize, 'gui.window.resize')
})
