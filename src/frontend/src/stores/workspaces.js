/**
 * workspaces —— 一个窗口内的多 workdir 工作区（SESSION-SCOPE-1）。
 *
 * 真相源：
 *   - 项目列表 = 宿主项目注册表（`gui.project.list` + workdir-opened/closed 事件）；
 *   - 页签/活动项 = 本 store（单窗口 UI 状态）。
 *
 * 约束：
 *   - 一个页签绑定一个 `{workDir, sessionId}`；不订阅多个 session。
 *   - 点击 workdir 根只切项目；点击 session 才开/激活页签。
 *   - 激活页签 = 更新 active（ChatPanel 据此带 work_dir 发请求）。
 */
import { defineStore } from 'pinia'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../events/event-names'
import { MsgTopics } from '../events/msgkeys'
import { addProject, listProjects, removeProject } from '../api/project'
import { listSessions } from '../api/session'
import { setWorkDir } from '../api/file'

function norm(p) {
  return String(p || '').replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()
}

// refreshSeq：per-workdir 的会话列表请求序号（非响应式；只用于丢弃乱序的旧响应）。
const refreshSeq = new Map()

function tabKey(workDir, sessionId) {
  return norm(workDir) + '\u0000' + String(sessionId || '')
}

/** scope 订阅/退订载荷：草稿页签（无会话身份）只订 workdir，不带空 session_id。 */
function scopePayload(workDir, sessionId) {
  const p = { work_dir: workDir }
  if (sessionId) p.session_id = sessionId
  return p
}

// 项目列表的本地持久化（宿主注册表是内存态；重启后按此列表重新 register，恢复工作区树）。
const SAVED_PROJECTS_KEY = 'chonkpilot.workspaces.projects'

function readSavedProjects() {
  try {
    const raw = globalThis.localStorage && globalThis.localStorage.getItem(SAVED_PROJECTS_KEY)
    const list = raw ? JSON.parse(raw) : []
    return Array.isArray(list) ? list.filter((x) => typeof x === 'string' && x) : []
  } catch {
    return []
  }
}

function writeSavedProjects(list) {
  try {
    if (globalThis.localStorage) {
      globalThis.localStorage.setItem(SAVED_PROJECTS_KEY, JSON.stringify(list))
    }
  } catch {
    // 隐私模式/配额失败不影响运行时
  }
}

export const useWorkspacesStore = defineStore('workspaces', {
  state: () => ({
    projects: {}, // norm(workDir) -> { workDir, dataDir, label, sessions: [] }
    expanded: {}, // norm(workDir) -> bool
    tabs: [], // [{ tabId, workDir, sessionId, title, closeable }]
    active: { workDir: '', sessionId: '', tabId: '' },
    loaded: false,
  }),

  getters: {
    projectList: (s) => Object.values(s.projects),
    activeWorkDir: (s) => s.active.workDir,
    activeSessionId: (s) => s.active.sessionId,
    activeTab: (s) => s.tabs.find((t) => t.tabId === s.active.tabId) || null,
  },

  actions: {
    /** 从宿主注册表拉取项目（不打开窗口）。 */
    async load() {
      let list = []
      try {
        list = await listProjects()
      } catch (e) {
        console.warn('[workspaces] list projects failed:', e)
      }
      for (const p of list) {
        this.upsertProject(p)
        this.subscribeProject(p.work_dir || p.workDir)
      }
      // 还原上次的显式项目（宿主注册表重启后为空；目录已不存在则跳过）。
      for (const wd of readSavedProjects()) {
        if (this.projects[norm(wd)]) continue
        try {
          await this.add(wd, '', false)
        } catch (e) {
          console.warn('[workspaces] restore project failed:', wd, e)
        }
      }
      this.loaded = true
    },

    /** 保存当前项目列表（只存 work_dir；data_dir 由注册表/布局解析）。 */
    saveProjectList() {
      writeSavedProjects(Object.values(this.projects).map((p) => p.workDir).filter(Boolean))
    },

    upsertProject(p) {
      const wd = p && (p.work_dir || p.workDir)
      if (!wd) return
      const key = norm(wd)
      const old = this.projects[key] || {}
      this.projects[key] = {
        workDir: wd,
        dataDir: (p && (p.data_dir || p.dataDir)) || old.dataDir || '',
        label: (p && (p.label || p.name)) || old.label || wd.split(/[\\/]/).filter(Boolean).pop() || wd,
        sessions: old.sessions || [],
      }
    },

    dropProject(workDir) {
      const key = norm(workDir)
      for (const t of this.tabs) {
        if (norm(t.workDir) === key) {
          mq.emit(MsgTopics.guiScopeUnsubscribe, scopePayload(t.workDir, t.sessionId)).catch(() => {})
        }
      }
      this.unsubscribeProject(workDir)
      delete this.projects[key]
      delete this.expanded[key]
      this.tabs = this.tabs.filter((t) => norm(t.workDir) !== key)
      if (norm(this.active.workDir) === key) this.active = { workDir: '', sessionId: '', tabId: '' }
      this.saveProjectList()
    },

    /** 添加 workdir（native picker 在调用方完成；这里只注册）。persist=false 用于启动还原。 */
    async add(workDir, dataDir = '', persist = true) {
      const r = await addProject(workDir, dataDir)
      if (r && r.project) this.upsertProject(r.project)
      this.expanded[norm(workDir)] = true
      this.subscribeProject(workDir)
      await this.refreshSessions(workDir)
      if (persist) this.saveProjectList()
      return r
    },

    async remove(workDir) {
      const r = await removeProject(workDir)
      if (r && r.removed === false) {
        throw new Error('项目仍被窗口占用，无法移除: ' + workDir)
      }
      this.dropProject(workDir)
    },

    toggle(workDir) {
      const k = norm(workDir)
      this.expanded[k] = !this.expanded[k]
    },

    /** 拉取某 workdir 的会话（data-session-list 带 work_dir）。
     *
     * 竞态保护（2026-11-21）：首屏 `join()` 的 refresh 与 `session-new` 触发的 refresh 可能
     * 并发；先发后到的旧响应会把新列表覆盖回空/旧值（实测：新建会话后工作区树仍空）。
     * 以 per-workdir 序号只采纳**最后一次**发起的请求结果。
     */
    async refreshSessions(workDir) {
      const key = norm(workDir)
      const project = this.projects[key]
      if (!project) return []
      const seq = (refreshSeq.get(key) || 0) + 1
      refreshSeq.set(key, seq)
      let list = []
      try {
        list = await listSessions({ workDir: project.workDir, dataDir: project.dataDir })
      } catch (e) {
        console.warn('[workspaces] list sessions failed:', e)
      }
      if (refreshSeq.get(key) !== seq) return project.sessions
      project.sessions = Array.isArray(list) ? list : []
      return project.sessions
    },

    /** 打开/复用「新建会话」草稿页签（session_id 尚未分配）。
     *
     * 页签模式（`ChatPanel bound`）下「新建会话」= 一个**空身份页签**：标签条不显示会话 tag、
     * 对话区呈「选择会话」空态；首条消息发送时 ChatPanel 自分配 id 并发 `session-changed`
     * → 由 `WorkspaceContent` 调 `bindDraftSession` 原地落成真实会话（不重建页签，
     * 避免发送中的组件被卸载）。同一 workdir 只保留一个草稿页签（重复点新建 = 激活既有）。
     */
    openDraft(workDir) {
      const wd = String(workDir || '')
      if (!wd) return ''
      const found = this.tabs.find((t) => t.draft && norm(t.workDir) === norm(wd))
      if (found) {
        this.activateTab(found.tabId)
        return found.tabId
      }
      const tabId = 'wt-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7)
      this.tabs.push({ tabId, workDir: wd, sessionId: '', title: '', draft: true, closeable: true })
      this.activateTab(tabId)
      return tabId
    },

    /** 草稿页签 → 真实会话（ChatPanel 首条消息自分配 id 后经 `session-changed` 回填）。 */
    bindDraftSession(tabId, sessionId) {
      const tab = this.tabs.find((t) => t.tabId === tabId)
      const sid = String(sessionId || '')
      if (!tab || !tab.draft || !sid) return false
      tab.sessionId = sid
      tab.draft = false
      tab.title = tab.title || sid.slice(0, 8)
      return true
    },

    /** 打开/复用会话页签。 */
    openSession(workDir, sessionId, title = '') {
      const wd = String(workDir || '')
      const sid = String(sessionId || '')
      if (!wd || !sid) return ''
      const key = tabKey(wd, sid)
      const found = this.tabs.find((t) => tabKey(t.workDir, t.sessionId) === key)
      if (found) {
        this.activateTab(found.tabId)
        return found.tabId
      }
      const tabId = 'wt-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7)
      this.tabs.push({ tabId, workDir: wd, sessionId: sid, title: title || sid.slice(0, 8), closeable: true })
      this.activateTab(tabId)
      return tabId
    },

    activateTab(tabId) {
      const tab = this.tabs.find((t) => t.tabId === tabId)
      if (!tab) return
      this.active = { workDir: tab.workDir, sessionId: tab.sessionId, tabId: tab.tabId }
      // 右侧内容区切项目时同步全局工作目录（文件树/编辑器等非 bound 面板跟随当前页签）。
      setWorkDir(tab.workDir)
      // 后端只向显式 scope 订阅者投递；会话页签单独订阅自己（workdir 树已订阅 workdir）。
      mq.emit(MsgTopics.guiScopeSubscribe, scopePayload(tab.workDir, tab.sessionId)).catch(() => {})
    },

    closeTab(tabId) {
      const idx = this.tabs.findIndex((t) => t.tabId === tabId)
      if (idx < 0) return
      const [tab] = this.tabs.splice(idx, 1)
      mq.emit(MsgTopics.guiScopeUnsubscribe, scopePayload(tab.workDir, tab.sessionId)).catch(() => {})
      if (this.active.tabId === tabId) {
        const next = this.tabs[idx] || this.tabs[idx - 1] || null
        if (next) this.activateTab(next.tabId)
        else this.active = { workDir: '', sessionId: '', tabId: '' }
      }
    },

    /** 工作区树订阅某项目的事件。 */
    subscribeProject(workDir) {
      const wd = String(workDir || '')
      if (!wd) return
      mq.emit(MsgTopics.guiScopeSubscribe, { work_dir: wd }).catch(() => {})
    },

    unsubscribeProject(workDir) {
      const wd = String(workDir || '')
      if (!wd) return
      mq.emit(MsgTopics.guiScopeUnsubscribe, { work_dir: wd }).catch(() => {})
    },

    /** 宿主 workdir-opened/closed 事件入口。 */
    onWorkdirOpened(p) {
      const wd = p && (p.work_dir || p.workDir)
      if (!wd) return
      this.upsertProject({ work_dir: wd, data_dir: p.data_dir || p.dataDir })
      this.subscribeProject(wd)
      this.saveProjectList()
    },

    onWorkdirClosed(p) {
      const wd = p && (p.work_dir || p.workDir)
      if (wd) this.dropProject(wd)
    },

    /** 事件订阅（组件 mount 一次；不在 store 内 watch）。
     * 返回**副本**：调用方可继续 push 自己的本地订阅，不会污染 store 缓存（否则重挂时
     * 会把已退订的本地回调当作 store 缓存再次返回 / 造成重复订阅）。 */
    bindEvents() {
      if (!this._unsubs) {
        this._unsubs = [
          mq.on(EventNames.workdirOpened, (p) => this.onWorkdirOpened(p)),
          mq.on(EventNames.workdirClosed, (p) => this.onWorkdirClosed(p)),
        ]
      }
      return this._unsubs.slice()
    },

    /** 退订 store 级事件并清空缓存（幂等）。组件卸载必须调用，否则 workdir-opened/closed
     * 回调会绑定在已销毁的 store 上，且重新挂载时 bindEvents 复用旧订阅（泄漏）。 */
    unbindEvents() {
      if (!this._unsubs) return
      const subs = this._unsubs
      this._unsubs = null
      for (const fn of subs) {
        try {
          fn()
        } catch (e) {
          console.warn('[workspaces] 退订事件失败:', e)
        }
      }
    },
  },
})

export { norm as workspaceKey }