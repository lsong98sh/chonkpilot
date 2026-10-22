/**
 * 布局档案 · 纯逻辑单测（UILAYOUT-2 / WIN-026）。
 *
 * 覆盖：种子、解析、名称校验、新建（空白 / 复制 / 上限 / 重名）、改名、删除、
 * 文档更新、恢复种子、默认名。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { register } from 'node:module'

// layout-free 内部导入无扩展名 → 先注册解析钩子（与 layoutAdapter.test.js 同口径）
register('./support/vue-sfc-hooks.mjs', import.meta.url)
await new Promise((r) => setTimeout(r, 0))

const {
  PROFILE_LIMIT,
  NAME_MAX,
  SEED_DEV_ID,
  SEED_SIMPLE_ID,
  seedProfiles,
  seedSimpleDoc,
  blankLayoutDoc,
  parseProfiles,
  serializeProfiles,
  validateName,
  nextDefaultName,
  createProfile,
  renameProfile,
  deleteProfile,
  updateProfileDoc,
  restoreSeed,
  isValidProfile,
} = await import('../src/layout/profiles-core.js')

test('种子：两套固定 ID，名称与文档可解析', () => {
  const seeds = seedProfiles(1000)
  assert.deepEqual(seeds.map((p) => p.id), [SEED_DEV_ID, SEED_SIMPLE_ID])
  assert.deepEqual(seeds.map((p) => p.name), ['开发', '简洁'])
  for (const p of seeds) {
    assert.ok(isValidProfile(p), `${p.id} 必须可用`)
    assert.equal(p.createdAt, 1000)
  }
  // 简洁 = 只有 chat
  const simple = seedSimpleDoc()
  assert.equal(simple.layout.view.viewKey, 'chat')
  // 空白 = 无 view
  assert.equal(blankLayoutDoc().layout.view, null)
})

test('解析：非法项丢弃、对象/数组两种形态兼容', () => {
  const good = seedProfiles(1)
  assert.equal(parseProfiles(serializeProfiles(good)).length, 2)
  assert.equal(parseProfiles(good).length, 2)
  assert.equal(parseProfiles('{bad json').length, 0)
  assert.equal(parseProfiles('').length, 0)
  assert.equal(parseProfiles(null).length, 0)
  const mixed = parseProfiles([
    ...good,
    { id: '', name: 'x', doc: good[0].doc },
    { id: 'bad', name: 'y', doc: '{bad' },
  ])
  assert.deepEqual(mixed.map((p) => p.id), [SEED_DEV_ID, SEED_SIMPLE_ID])
})

test('名称：空 / 超长 / 重名（trim + 大小写不敏感）', () => {
  const list = seedProfiles(1)
  assert.match(validateName(list, '   '), /不能为空/)
  assert.match(validateName(list, 'x'.repeat(NAME_MAX + 1)), /不能超过/)
  assert.match(validateName(list, ' 开发 '), /已存在同名/)
  assert.equal(validateName(list, '开发', SEED_DEV_ID), '', '改名排除自己')
  assert.equal(validateName(list, '新名字'), '')
})

test('新建：空白 / 复制 / 上限 / 重名', () => {
  const base = seedProfiles(1)
  const blank = createProfile(base, { name: '空白', from: 'blank' }, 2)
  assert.ok(blank.ok)
  assert.equal(blank.list.length, 3)
  assert.equal(JSON.parse(blank.profile.doc).layout.view, null)

  const copy = createProfile(base, { name: '复制开发', from: SEED_DEV_ID }, 3)
  assert.ok(copy.ok)
  assert.equal(copy.profile.doc, base[0].doc, '复制 = 原样文档')

  assert.equal(createProfile(base, { name: '开发' }).ok, false, '禁止重名')
  assert.equal(createProfile(base, { name: 'x', from: 'nope' }).ok, false, '来源不存在')

  const full = Array.from({ length: PROFILE_LIMIT }, (_, i) => ({
    id: `p${i}`,
    name: `L${i}`,
    doc: base[0].doc,
    createdAt: 0,
    updatedAt: 0,
  }))
  assert.match(createProfile(full, { name: '溢出' }).error, /最多/)
})

test('改名 / 删除 / 更新文档', () => {
  const list = seedProfiles(1)
  const rn = renameProfile(list, SEED_DEV_ID, '写代码', 2)
  assert.ok(rn.ok)
  assert.equal(rn.list[0].name, '写代码')
  assert.equal(rn.list[0].updatedAt, 2)
  assert.equal(renameProfile(list, 'nope', 'x').ok, false)

  const del = deleteProfile(list, SEED_DEV_ID)
  assert.ok(del.ok)
  assert.equal(del.list.length, 1)
  assert.equal(deleteProfile(list, 'nope').ok, false)

  const upd = updateProfileDoc(list, SEED_DEV_ID, blankLayoutDoc(), 3)
  assert.ok(upd.ok)
  assert.equal(JSON.parse(upd.list[0].doc).layout.view, null)
  assert.equal(updateProfileDoc(list, SEED_DEV_ID, '{bad').ok, false)
})

test('恢复种子：存在则覆盖名称+内容；已删除则重新插入；非种子拒绝', () => {
  const list = seedProfiles(1)
  const modified = updateProfileDoc(renameProfile(list, SEED_DEV_ID, '乱改').list, SEED_DEV_ID, blankLayoutDoc(), 5)
  const restored = restoreSeed(modified.list, SEED_DEV_ID, 9)
  assert.ok(restored.ok)
  assert.equal(restored.list[0].name, '开发')
  assert.equal(restored.list[0].doc, list[0].doc)
  assert.equal(restored.list[0].updatedAt, 9)

  const removed = deleteProfile(list, SEED_SIMPLE_ID).list
  const re = restoreSeed(removed, SEED_SIMPLE_ID, 9)
  assert.ok(re.ok)
  assert.equal(re.list.length, 2)
  assert.equal(restoreSeed(list, 'p_x').ok, false)
})

test('默认名：跳过已占用', () => {
  const list = seedProfiles(1)
  assert.equal(nextDefaultName(list), '新布局 1')
  list.push({ id: 'a', name: '新布局 1', doc: list[0].doc, createdAt: 0, updatedAt: 0 })
  assert.equal(nextDefaultName(list), '新布局 2')
})
