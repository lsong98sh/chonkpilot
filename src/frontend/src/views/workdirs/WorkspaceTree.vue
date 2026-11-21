<template>
  <div class="ws-tree">
    <div v-for="p in projects" :key="p.workDir" class="ws-project">
      <div class="ws-project-row" :title="p.workDir" @click="toggle(p.workDir)">
        <Icon :name="isExpanded(p.workDir) ? 'arrow-down' : 'arrow-right'" class="ws-caret" />
        <Icon name="folder-opened" class="ws-icon" />
        <span class="ws-label">{{ p.label }}</span>
        <span class="ws-count" v-if="runningCount(p)">{{ runningCount(p) }}</span>
        <Icon name="close" class="ws-remove" title="移除项目" @click.stop="removeProject(p.workDir)" />
      </div>
      <div v-show="isExpanded(p.workDir)" class="ws-sessions">
        <div
          v-for="s in p.sessions"
          :key="sessionIdOf(s)"
          class="ws-session"
          :class="{ active: isActive(p.workDir, s) }"
          :title="s.title || sessionIdOf(s)"
          @click="openSession(p, s)"
        >
          <Icon name="chat-dot-square" class="ws-icon" />
          <span class="ws-label">{{ s.title || sessionIdOf(s).slice(0, 8) }}</span>
          <span v-if="isRunning(p, s)" class="ws-dot" />
        </div>
        <div v-if="!p.sessions.length" class="ws-empty">{{ $t('fileTree.no_sessions') }}</div>
      </div>
    </div>
    <button type="button" class="ws-add" @click="pickAdd">
      <Icon name="plus" /> {{ $t('fileTree.add_workdir') }}
    </button>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import Icon from '@ckfw/components/icon/Icon.vue'
import { useDirPicker } from '../../composables/useDirPicker'
import { useSelectionStore } from '../../stores/selection'
import { loadInitDataPrefetched, setWorkDir } from '../../api/file'
import { useWorkspacesStore } from '../../stores/workspaces'
import { EventNames } from '../../events/event-names'
import { MsgTopics } from '../../events/msgkeys'
import mq from '@ckfw/utils/mq.js'

defineOptions({ name: 'WorkspaceTree' })

const store = useWorkspacesStore()
const selection = useSelectionStore()
const { pickDir } = useDirPicker('workspace-tree')
const running = ref([])
let unsubs = []

const projects = computed(() => store.projectList)
const expanded = computed(() => store.expanded)

function norm(p) {
  return String(p || '').replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()
}

// isExpanded：展开态在 store 里按 `norm(workDir)` 存（小写 + POSIX + 去尾分隔符），而
// `p.workDir` 来自宿主注册表（Windows 反斜杠原样）。模板若直接 `expanded[p.workDir]`
// 会恒为 undefined → 会话列表永不显示（v-show=false），点项目行也翻不到可见键。
function isExpanded(workDir) {
  return !!expanded.value[norm(workDir)]
}

function sessionIdOf(s) {
  return (s && (s.session_id || s.id)) || ''
}

function isActive(wd, s) {
  return norm(store.active.workDir) === norm(wd) && store.active.sessionId === sessionIdOf(s)
}

function isRunning(p, s) {
  const sid = sessionIdOf(s)
  return running.value.some((r) => String(r.session_id || '') === sid && (!r.work_dir || norm(r.work_dir) === norm(p.workDir)))
}

function runningCount(p) {
  return running.value.filter((r) => !r.work_dir || norm(r.work_dir) === norm(p.workDir)).length
}

function toggle(wd) {
  store.toggle(wd)
  if (store.expanded[norm(wd)]) {
    store.subscribeProject(wd)
    store.refreshSessions(wd)
  }
}

function openSession(p, s) {
  const sid = sessionIdOf(s)
  if (!sid) return
  store.openSession(p.workDir, sid, (s && s.title) || '')
}

async function pickAdd() {
  let path = ''
  try {
    path = await pickDir()
  } catch (e) {
    console.error('[workspace] pick dir failed:', e)
    return
  }
  if (!path) return
  try {
    await store.add(path)
  } catch (e) {
    console.error('[workspace] add project failed:', e)
  }
}

async function removeProject(workDir) {
  try {
    await store.remove(workDir)
  } catch (e) {
    console.error('[workspace] remove project failed:', e)
  }
}

async function loadRunning() {
  const res = await mq.emit(MsgTopics.sessionRunningList, {}, { timeout: 10000 }).catch(() => null)
  const list = res && res.backend && res.backend.result && res.backend.result.list
  running.value = Array.isArray(list) ? list : []
}

onMounted(async () => {
  unsubs = store.bindEvents()
  await store.load()
  let wd = selection.workDir
  if (!wd) {
    const init = await loadInitDataPrefetched().catch(() => null)
    wd = (init && init.workDir) || ''
    if (wd) setWorkDir(wd)
  }
  if (wd) {
    // 当前窗口项目也纳入树（宿主已有实例绑定，注册为 pinned 只是登记）。
    await store.add(wd).catch(() => {})
    store.expanded[norm(wd)] = true
  }
  await Promise.all(store.projectList.map((p) => store.refreshSessions(p.workDir)))
  await loadRunning()
  unsubs.push(mq.on(MsgTopics.sessionRunningList, (d) => {
    const list = d && d.list
    if (Array.isArray(list)) running.value = list
  }))
  unsubs.push(mq.on(MsgTopics.sessionTurnStart, () => loadRunning()))
  unsubs.push(mq.on(EventNames.llmComplete, () => loadRunning()))
  unsubs.push(mq.on(EventNames.sessionNew, () => {
    for (const p of store.projectList) store.refreshSessions(p.workDir)
  }))
  // 标题变更（data-session-title-changed，全局广播）：新建会话后先有 session-new（此时标题
  // 仍 = session_id）再改标题 → 树若只在 session-new 刷新，会长期显示会话 id 而非标题。
  // 就地更新（无需整表重拉；payload = {session_id, title}）。
  unsubs.push(mq.on(EventNames.sessionTitleChanged, (d) => {
    const sid = String((d && d.session_id) || '')
    if (!sid) return
    for (const p of store.projectList) {
      const row = (p.sessions || []).find((s) => sessionIdOf(s) === sid)
      if (row) {
        row.title = (d && d.title) || row.title
        return
      }
    }
  }))
})

onUnmounted(() => {
  // 先退订 store 级订阅并清空其缓存（否则重挂时 bindEvents 复用旧回调 → 泄漏 / 重复订阅）。
  store.unbindEvents()
  for (const fn of unsubs) fn()
  unsubs = []
})
</script>

<style scoped>
.ws-tree { height: 100%; overflow: auto; padding: 4px 0; font-size: 12px; }
.ws-project-row, .ws-session { display: flex; align-items: center; gap: 4px; padding: 3px 8px; cursor: pointer; white-space: nowrap; }
.ws-project-row:hover, .ws-session:hover { background: var(--bg-hover); }
.ws-session { padding-left: 24px; color: var(--text-secondary); }
.ws-session.active { color: var(--accent); background: var(--bg-active); }
.ws-caret { width: 12px; height: 12px; }
.ws-icon { width: 14px; height: 14px; flex: none; }
.ws-label { overflow: hidden; text-overflow: ellipsis; }
.ws-count { margin-left: auto; font-size: 10px; color: var(--text-secondary); }
.ws-remove { width: 12px; height: 12px; flex: none; opacity: 0; color: var(--text-secondary); }
.ws-project-row:hover .ws-remove { opacity: .6; }
.ws-remove:hover { opacity: 1; color: var(--danger, #e55); }
.ws-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--accent); margin-left: auto; }
.ws-empty { padding: 4px 8px 4px 28px; color: var(--text-secondary); font-size: 11px; }
.ws-add { display: flex; align-items: center; gap: 4px; width: calc(100% - 12px); margin: 6px; padding: 4px 6px; border: 1px dashed var(--border); background: transparent; color: var(--text-secondary); cursor: pointer; font-size: 11px; }
.ws-add:hover { color: var(--accent); border-color: var(--accent); }
</style>