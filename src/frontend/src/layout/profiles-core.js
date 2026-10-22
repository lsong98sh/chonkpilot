/**
 * 布局档案 · 纯逻辑（UILAYOUT-2 / WIN-026）。
 *
 * 只做数据：种子、解析、校验、增删改。**零 Vue / 零 IO** → node:test 直跑。
 * 宿主存储与命令装配见 `layout/profiles.js`。
 */
import { defaultLayoutDoc, parseFreeDoc, toFreeString } from './persist.js'

/** 种子档案保留 ID（稳定；「恢复初始布局」按 ID 找回） */
export const SEED_DEV_ID = 'seed:dev'
export const SEED_SIMPLE_ID = 'seed:simple'

/** 档案总数上限（种子计入） */
export const PROFILE_LIMIT = 10
/** 名称最大长度 */
export const NAME_MAX = 24

/** 简洁模式文档：整棵布局只有一个对话区域 */
export function seedSimpleDoc() {
  const base = defaultLayoutDoc()
  return {
    ...base,
    layout: {
      id: 'leaf_chat',
      type: 'leaf',
      kind: 'single',
      view: { viewKey: 'chat', instanceKey: null },
      minWidth: 280,
      maxWidth: 800,
    },
    toolbars: [],
    drawers: [],
  }
}

/** 新建「空白布局」文档：一个空白单区域（无 view），栏 / 抽屉用默认 */
export function blankLayoutDoc() {
  const base = defaultLayoutDoc()
  return {
    ...base,
    layout: { id: 'leaf_blank', type: 'leaf', kind: 'single', view: null },
    toolbars: [],
    drawers: [],
  }
}

/** 两个种子档案（首次初始化写入 DB；之后与普通档案同权） */
export function seedProfiles(now = Date.now()) {
  return [
    { id: SEED_DEV_ID, name: '开发', doc: toFreeString(defaultLayoutDoc()), createdAt: now, updatedAt: now },
    { id: SEED_SIMPLE_ID, name: '简洁', doc: toFreeString(seedSimpleDoc()), createdAt: now, updatedAt: now },
  ]
}

/** 是否是种子档案 id */
export function isSeedId(id) {
  return id === SEED_DEV_ID || id === SEED_SIMPLE_ID
}

function normName(name) {
  return String(name ?? '').trim()
}

/** 档案是否可用：结构齐备且 doc 可解析 */
export function isValidProfile(p) {
  return Boolean(p && typeof p.id === 'string' && p.id && typeof p.name === 'string' && parseFreeDoc(p.doc))
}

/** 解析 `layout.profiles`（字符串或数组）→ 档案数组；非法项丢弃 */
export function parseProfiles(raw) {
  let arr = raw
  if (typeof raw === 'string') {
    if (!raw) return []
    try {
      arr = JSON.parse(raw)
    } catch {
      return []
    }
  }
  if (!Array.isArray(arr)) return []
  return arr.filter(isValidProfile).map((p) => ({
    id: p.id,
    name: normName(p.name) || '未命名布局',
    doc: typeof p.doc === 'string' ? p.doc : JSON.stringify(p.doc),
    createdAt: Number(p.createdAt) || 0,
    updatedAt: Number(p.updatedAt) || 0,
  }))
}

export function serializeProfiles(list) {
  return JSON.stringify(list ?? [])
}

export function findProfile(list, id) {
  return (list ?? []).find((p) => p.id === id) ?? null
}

/** 名称校验：空 / 超长 / 重名（trim + 大小写不敏感；exceptId = 改名时排除自己） */
export function validateName(list, name, exceptId = '') {
  const n = normName(name)
  if (!n) return '布局名称不能为空'
  if (n.length > NAME_MAX) return `布局名称不能超过 ${NAME_MAX} 个字符`
  const hit = (list ?? []).find(
    (p) => p.id !== exceptId && p.name.trim().toLowerCase() === n.toLowerCase(),
  )
  if (hit) return `已存在同名布局「${hit.name}」`
  return ''
}

/** 下一个默认名：新布局 1 / 新布局 2 …（跳过已占用） */
export function nextDefaultName(list) {
  const used = new Set((list ?? []).map((p) => p.name.trim().toLowerCase()))
  let i = 1
  while (used.has(`新布局 ${i}`.toLowerCase())) i += 1
  return `新布局 ${i}`
}

function newId() {
  return `p_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`
}

/**
 * 新建档案（不切换）。
 * @returns {{ok:true, list:Array, profile:object} | {ok:false, error:string}}
 */
export function createProfile(list, { name, from = 'blank', doc } = {}, now = Date.now()) {
  const cur = list ?? []
  if (cur.length >= PROFILE_LIMIT) return { ok: false, error: `布局档案最多 ${PROFILE_LIMIT} 个` }
  const err = validateName(cur, name)
  if (err) return { ok: false, error: err }
  let docStr
  if (typeof doc === 'string' && parseFreeDoc(doc)) {
    docStr = doc
  } else if (from && from !== 'blank') {
    const src = findProfile(cur, from)
    if (!src || !parseFreeDoc(src.doc)) return { ok: false, error: '复制来源布局不可用' }
    docStr = src.doc
  } else {
    docStr = toFreeString(blankLayoutDoc())
  }
  const profile = { id: newId(), name: normName(name), doc: docStr, createdAt: now, updatedAt: now }
  return { ok: true, list: [...cur, profile], profile }
}

/** 改名 */
export function renameProfile(list, id, name, now = Date.now()) {
  const hit = findProfile(list, id)
  if (!hit) return { ok: false, error: '布局不存在' }
  const err = validateName(list, name, id)
  if (err) return { ok: false, error: err }
  return {
    ok: true,
    list: (list ?? []).map((p) => (p.id === id ? { ...p, name: normName(name), updatedAt: now } : p)),
  }
}

/** 删除 */
export function deleteProfile(list, id) {
  if (!findProfile(list, id)) return { ok: false, error: '布局不存在' }
  return { ok: true, list: (list ?? []).filter((p) => p.id !== id) }
}

/** 更新档案文档（编辑器保存用；不切换） */
export function updateProfileDoc(list, id, doc, now = Date.now()) {
  const hit = findProfile(list, id)
  if (!hit) return { ok: false, error: '布局不存在' }
  const docStr = typeof doc === 'string' ? doc : toFreeString(doc)
  if (!parseFreeDoc(docStr)) return { ok: false, error: '布局文档不合法' }
  return {
    ok: true,
    list: (list ?? []).map((p) => (p.id === id ? { ...p, doc: docStr, updatedAt: now } : p)),
  }
}

/** 恢复单个种子档案（名称 + 内容）；已被删除 → 重新插入 */
export function restoreSeed(list, id, now = Date.now()) {
  const seed = seedProfiles(now).find((p) => p.id === id)
  if (!seed) return { ok: false, error: '只能恢复种子布局' }
  const exists = Boolean(findProfile(list, id))
  return {
    ok: true,
    list: exists ? (list ?? []).map((p) => (p.id === id ? seed : p)) : [...(list ?? []), seed],
    profile: seed,
  }
}
