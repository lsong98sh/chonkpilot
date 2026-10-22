/**
 * 布局档案 · 宿主接线（UILAYOUT-2 / WIN-026）。
 *
 * 职责：档案 store（`layout.profiles` / `layout.activeProfile`）、切换、编辑目标、命令订阅、
 * 编辑器保存钩子。纯数据逻辑在 `profiles-core.js`（node:test 直跑）。
 */
import { defineStore } from 'pinia'
import { h, nextTick } from 'vue'
import {
  createProfile,
  deleteProfile,
  findProfile,
  isSeedId,
  parseProfiles,
  renameProfile,
  restoreSeed,
  seedProfiles,
  serializeProfiles,
  updateProfileDoc,
} from './profiles-core.js'
import { applyDocToConfig, defaultLayoutDoc, parseFreeDoc, toFreeString } from './persist.js'
import { normalize } from '../layout-free/config/config-normalize.js'
import { cloneTree, collectLeaves } from '../layout-free/utils/layout.js'
import { useConfigStore } from '../layout-free/stores/config.js'
import { useEditStore } from '../layout-free/stores/edit.js'
import { useRuntimeStore } from '../layout-free/stores/runtime.js'
import { captureViewRects, playViewTransitions } from '../layout-free/stores/viewPool.js'
import { EventNames } from '../layout-free/events/event-names.js'
import { saveLayoutState } from '../api/file'
import { dialog } from '../components/dialog'
import mq from '../utils/mq'
import LayoutNewDialog from './bar-views/LayoutNewDialog.vue'

export const useProfileStore = defineStore('layoutProfiles', {
  state: () => ({
    /** 档案数组（种子 + 用户自建；同权） */
    list: [],
    /** 当前活动档案 id（'' = 无） */
    activeId: '',
    loaded: false,
  }),
  getters: {
    active(state) {
      return findProfile(state.list, state.activeId)
    },
    /** 当前活动档案是否为种子（决定「恢复初始布局」是否显示） */
    activeIsSeed(state) {
      return isSeedId(state.activeId)
    },
    canCreate(state) {
      return state.list.length < PROFILE_LIMIT
    },
  },
  actions: {
    /** 启动装载：`layout.profiles` 缺失才写种子；已存在（含空数组）不再补 */
    load(layoutState) {
      const raw = layoutState && typeof layoutState === 'object' ? layoutState.profiles : undefined
      const missing = raw === undefined || raw === null
      this.list = missing ? seedProfiles() : parseProfiles(raw)
      const active = layoutState && typeof layoutState === 'object' ? layoutState.activeProfile : ''
      this.activeId = typeof active === 'string' ? active : ''
      if (this.activeId && !findProfile(this.list, this.activeId)) this.activeId = ''
      this.loaded = true
      if (missing) this.persist()
    },

    /** 落库：`gui.ui.save { layout: { profiles, activeProfile } }` */
    persist() {
      const payload = { profiles: serializeProfiles(this.list), activeProfile: this.activeId }
      Promise.resolve(saveLayoutState(payload)).then(
        (r) => {
          if (r && r.ok === false) useRuntimeStore().setHint(`布局档案未落库：${r.error || '未知原因'}`)
        },
        (e) => useRuntimeStore().setHint(`布局档案未落库：${(e && e.message) || e}`),
      )
    },

    /** 切换布局（视图池保活：只换树 / 搬 DOM，不重建单例 view） */
    apply(id) {
      const p = findProfile(this.list, id)
      if (!p) {
        useRuntimeStore().setHint('布局不存在或已删除')
        return false
      }
      const doc = parseFreeDoc(p.doc)
      if (!doc) {
        useRuntimeStore().setHint(`布局「${p.name}」内容损坏，无法切换`)
        return false
      }
      const config = useConfigStore()
      const runtime = useRuntimeStore()
      // 切换前先把当前工作区落盘（不自动写档案：档案只在「编辑当前」保存时更新）
      if (config.live) config.saveLiveLayout()
      const before = captureViewRects()
      applyDocToConfig(config, doc)
      this.activeId = id
      this.persist()
      config.saveLiveLayout()
      runtime.pruneTabs(collectLeaves(config.live.layout).map((l) => l.id))
      nextTick(() => playViewTransitions(before))
      runtime.setHint(`已切换到布局「${p.name}」`)
      return true
    },

    /** 新建（不切换）：返回新档案；调用方随后进编辑器 */
    create(name, from = 'blank') {
      const res = createProfile(this.list, { name, from })
      if (!res.ok) {
        useRuntimeStore().setHint(res.error)
        return res
      }
      this.list = res.list
      this.persist()
      return res
    },

    rename(id, name) {
      const res = renameProfile(this.list, id, name)
      if (!res.ok) {
        useRuntimeStore().setHint(res.error)
        return false
      }
      this.list = res.list
      this.persist()
      return true
    },

    remove(id) {
      const res = deleteProfile(this.list, id)
      if (!res.ok) {
        useRuntimeStore().setHint(res.error)
        return false
      }
      this.list = res.list
      if (this.activeId === id) this.activeId = ''
      this.persist()
      return true
    },

    /** 恢复种子档案：名称 + 内容一起恢复；不切换当前布局 */
    restore(id) {
      const target = id || this.activeId
      if (!isSeedId(target)) {
        useRuntimeStore().setHint('只有「开发 / 简洁」种子布局可恢复')
        return false
      }
      const res = restoreSeed(this.list, target)
      if (!res.ok) return false
      this.list = res.list
      this.persist()
      // 当前活动档案 → 立即把恢复后的内容应用到工作区（否则界面看不到变化）
      if (this.activeId === target) {
        const doc = parseFreeDoc(res.profile.doc)
        if (doc) {
          const config = useConfigStore()
          applyDocToConfig(config, doc)
          config.saveLiveLayout()
        }
      }
      useRuntimeStore().setHint(`布局「${res.profile.name}」已恢复初始内容`)
      return true
    },

    /**
     * 打开编辑器。
     * - `id` 为空 → 编辑当前工作区（传统路径）
     * - `id` 为「当前活动档案」→ 草稿 = 当前工作区（含运行态拖拽），保存后回写该档案
     * - 其它档案（新建 / 非当前）→ 草稿 = 该档案文档（不切换、不污染 live）
     */
    openEditor(id = '') {
      const config = useConfigStore()
      const edit = useEditStore()
      const p = id ? findProfile(this.list, id) : null
      if (p && p.id !== this.activeId) {
        const doc = parseFreeDoc(p.doc)
        if (!doc) {
          useRuntimeStore().setHint(`布局「${p.name}」内容损坏，无法编辑`)
          return false
        }
        config.draft = normalize(cloneTree(doc))
        edit.setProfileId(p.id)
      } else {
        config.draft = cloneTree(config.live)
        edit.setProfileId(p ? p.id : '')
      }
      config.messages = []
      config.dirty = false
      edit.openEditor('layout')
      return true
    },
  },
})

/** 打开「新建布局」对话框 */
function openNewDialog() {
  if (!dialog || typeof dialog.show !== 'function') {
    useRuntimeStore().setHint('对话框服务不可用，无法新建布局')
    return
  }
  let handle = null
  const done = () => {
    try {
      handle?.close?.()
    } catch {
      /* 已关闭 */
    }
  }
  handle = dialog.show(h(LayoutNewDialog, { onDone: done }), {
    title: '新建布局',
    modal: true,
    closable: true,
    width: 460,
    height: 'auto',
  })
}

/**
 * 编辑器保存钩子（注入机制层 `editorSaveHook`）。
 * - `profileId` 为空 → 返回 false，走传统保存（仅当前工作区）
 * - `profileId === activeId` → 应用 + 更新档案（保存 = 应用）
 * - 其它（新建 / 非当前）→ 只写档案，不切换
 */
export async function profileEditorSave({ profileId, draft, validate }) {
  if (!profileId) return false
  const errors = validate()
  if (errors.length) return { ok: false, errors }
  const store = useProfileStore()
  const config = useConfigStore()
  const edit = useEditStore()
  const runtime = useRuntimeStore()
  const snapshot = draft()
  const res = updateProfileDoc(store.list, profileId, toFreeString(snapshot))
  if (!res.ok) {
    runtime.setHint(res.error)
    return { ok: false, errors: [res.error] }
  }
  store.list = res.list

  if (profileId === store.activeId) {
    applyDocToConfig(config, snapshot)
    config.saveLiveLayout()
    runtime.pruneTabs(collectLeaves(config.live.layout).map((l) => l.id))
    runtime.setHint('布局已保存并应用')
  } else {
    runtime.setHint('布局已保存（未切换；在布局列表中选择后生效）')
  }
  store.persist()
  edit.closeEditor()
  return { ok: true }
}

/** 订阅档案命令（由 `layout/index.js` 装配一次） */
export function installProfileCommands() {
  const store = useProfileStore()
  mq.on(EventNames.layoutProfileNew, () => openNewDialog())
  mq.on(EventNames.layoutProfileCreate, (d) => {
    const res = store.create(d?.name, d?.from || 'blank')
    if (res.ok) store.openEditor(res.profile.id)
    return res.ok
  })
  mq.on(EventNames.layoutProfileSwitch, (d) => store.apply(d?.id))
  mq.on(EventNames.layoutEdit, (d) => store.openEditor(d?.id || store.activeId))
  mq.on(EventNames.layoutProfileRename, (d) => store.rename(d?.id, d?.name))
  mq.on(EventNames.layoutProfileDelete, (d) => store.remove(d?.id))
  mq.on(EventNames.layoutProfileRestore, (d) => store.restore(d?.id || store.activeId))
}

/** 默认布局文档（供宿主首次装配 / 测试引用） */
export { defaultLayoutDoc }
