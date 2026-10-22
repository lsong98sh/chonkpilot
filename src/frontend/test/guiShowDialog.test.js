/**
 * gui.show-dialog → 弹框 → gui.selected-dir 单测（🆕 2026-10-10）。
 *
 * 覆盖：① 事件载荷解析/归一（kind 过滤 + root = depth 最小项）；② 结果事件载荷构造
 *      （选中 = ok+path / 取消 = cancelled 无 path）；③ 常量（msgkeys）；④ 接线守卫
 *      （useGuiShowDialog 订阅 gui.show-dialog → dialog.show(DirPickerDialog) → 发 gui.selected-dir，
 *      无 watch / 无 window.go / 无字面量主题）；⑤ App.vue 全局接线；⑥ /dirs 路径已删除（无死路径）。
 *
 * 注：前端暂无组件级测试运行器（纯逻辑直调 + 源码守卫，与 dirPicker.test.js 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { parseShowDialog, selectedDirEvent, SHOW_DIALOG_KIND_SELECT_DIR } from '../src/utils/guiShowDialog.js'
import { MsgTopics, GuiShowDialogKeys, GuiSelectedDirKeys } from '../src/events/msgkeys.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const feRoot = join(here, '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

const ROOT = 'E:/proj/wd'

test('① parseShowDialog：kind="select-dir" → 归一 options，root = depth 最小项', () => {
  // options 乱序（depth 0 不在首位）→ root 仍取 depth 最小项
  const view = parseShowDialog({
    widget_id: 'w-1',
    kind: 'select-dir',
    options: [
      { path: ROOT + '/sub', name: 'sub', depth: 1 },
      { path: ROOT, name: 'wd', depth: 0 },
    ],
  })
  assert.equal(view.widgetId, 'w-1')
  assert.equal(view.root, ROOT)
  assert.deepEqual(view.options.map((o) => o.path), [ROOT + '/sub', ROOT])
})

test('① parseShowDialog：非 select-dir / 非法 payload / options 非数组 → null 或空清单', () => {
  assert.equal(parseShowDialog({ kind: 'other', options: [] }), null, '非 select-dir 须忽略')
  assert.equal(parseShowDialog(null), null, '空载荷须忽略')
  const v = parseShowDialog({ widget_id: 7, kind: SHOW_DIALOG_KIND_SELECT_DIR, options: 'bad' })
  assert.equal(v.widgetId, '7', 'widget_id 须字符串化（结果关联一致）')
  assert.deepEqual(v.options, [], 'options 非数组 → 空清单')
  assert.equal(v.root, '', '空清单 → root 空串')
  // 非法项（缺 path / 非字符串）被过滤
  const v2 = parseShowDialog({ kind: 'select-dir', options: [{ name: 'x' }, { path: 5 }, { path: ROOT, depth: 0 }] })
  assert.deepEqual(v2.options.map((o) => o.path), [ROOT])
})

test('② selectedDirEvent：选中 = {status:"ok", path}；取消 = {status:"cancelled"}（无 path）', () => {
  const ok = selectedDirEvent('w-1', ROOT + '/sub')
  assert.equal(ok[GuiSelectedDirKeys.widget_id], 'w-1')
  assert.equal(ok[GuiSelectedDirKeys.status], 'ok')
  assert.equal(ok[GuiSelectedDirKeys.path], ROOT + '/sub')

  const cancelled = selectedDirEvent('w-1', '')
  assert.equal(cancelled[GuiSelectedDirKeys.widget_id], 'w-1')
  assert.equal(cancelled[GuiSelectedDirKeys.status], 'cancelled')
  assert.equal(GuiSelectedDirKeys.path in cancelled, false, '取消不得带 path')
})

test('③ 常量：msgkeys 已生成 gui.show-dialog / gui.selected-dir 及字段键', () => {
  assert.equal(MsgTopics.guiShowDialog, 'gui.show-dialog')
  assert.equal(MsgTopics.guiSelectedDir, 'gui.selected-dir')
  assert.deepEqual(
    [GuiShowDialogKeys.widget_id, GuiShowDialogKeys.kind, GuiShowDialogKeys.options],
    ['widget_id', 'kind', 'options'],
  )
  assert.deepEqual(
    [GuiSelectedDirKeys.widget_id, GuiSelectedDirKeys.status, GuiSelectedDirKeys.path],
    ['widget_id', 'status', 'path'],
  )
})

test('④ 接线守卫：useGuiShowDialog 订阅 show-dialog → 弹 DirPickerDialog → 发 selected-dir', () => {
  const comp = read('composables/useGuiShowDialog.js')
  assert.match(comp, /mq\.on\(MsgTopics\.guiShowDialog/, '须用 MsgTopics 常量订阅 gui.show-dialog')
  assert.match(comp, /mq\.emit\(MsgTopics\.guiSelectedDir/, '须发 gui.selected-dir（MsgTopics 常量）')
  assert.match(comp, /dialog\.show\(/, '须经自研 DialogShell（dialog.show）承载')
  assert.match(comp, /DirPickerDialog/, '须弹目录选择器组件')
  assert.match(comp, /parseShowDialog\(/, '须经纯模块解析事件载荷')
  assert.match(comp, /openDialogs\.has\(widgetId\)/, '须按 widget_id 防二次弹框')
  assert.doesNotMatch(comp, /\bwatch(Effect)?\s*\(/, '不得使用 watch/watchEffect（用 mq.on 订阅）')
  assert.doesNotMatch(comp, /window\.go\./, '禁止直调 window.go.*')
  assert.doesNotMatch(comp, /mq\.(?:on|emit)\(\s*'/, '主/契题不得以字符串字面量书写（须 MsgTopics 常量）')
})

test('⑤ App.vue 全局接线 useGuiShowDialog（模块级单例，只订阅一次）', () => {
  const app = read('App.vue')
  assert.match(app, /import \{ useGuiShowDialog \} from '\.\/composables\/useGuiShowDialog'/, 'App.vue 须引入')
  assert.match(app, /useGuiShowDialog\(\)/, 'App.vue setup 须调用一次')
  const comp = read('composables/useGuiShowDialog.js')
  assert.match(comp, /let subscribed = false/, '模块级单例开关')
})

test('⑥ /dirs 死路径已删除：api/dirs.js 不再存在，前端无 fetchDirs 引用', () => {
  assert.equal(existsSync(join(srcDir, 'api', 'dirs.js')), false, 'api/dirs.js 应已删除')
  for (const rel of ['composables/useDirPicker.js', 'views/common/DirPickerDialog.vue', 'utils/dirPicker.js',
    'composables/useGuiShowDialog.js', 'utils/guiShowDialog.js', 'App.vue']) {
    assert.doesNotMatch(read(rel), /fetchDirs|'\/dirs'|GET \/dirs/, `${rel} 不得再引用 /dirs`)
  }
  // PRODSIM-1：browser 形态删除 → 上行/下行 shim（ck_http_bridge.js）与其测试一并删除
  assert.equal(existsSync(join(feRoot, 'test', 'httpBridgeShim.test.js')), false, 'browser shim 测试应已删除')
})
