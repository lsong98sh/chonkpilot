<template>
  <div class="ws-content">
    <div v-if="tabs.length" class="ws-tabs">
      <div
        v-for="tab in tabs"
        :key="tab.tabId"
        class="ws-tab"
        :class="{ active: tab.tabId === active.tabId }"
        :title="tab.workDir"
        @click="store.activateTab(tab.tabId)"
      >
        <span class="ws-tab-label">{{ tab.title || (tab.sessionId ? tab.sessionId.slice(0, 8) : $t('common.new_session')) }}</span>
        <Icon v-if="tab.closeable" name="close" class="ws-tab-close" @click.stop="store.closeTab(tab.tabId)" />
      </div>
    </div>
    <div class="ws-body">
      <ChatPanel
        v-if="activeTab"
        :key="activeTab.tabId"
        :session-id="activeTab.sessionId"
        :work-dir="activeTab.workDir"
        :data-dir="dataDirOf(activeTab.workDir)"
        bound
      />
      <ChatPanel v-else />
    </div>
  </div>
</template>

<script setup>
/**
 * 右侧内容区（一个窗口多 workdir · SESSION-SCOPE-1）：有活动页签 → 绑定页签的 ChatPanel；
 * 无页签（首启 / 旧入口）→ 未绑定 ChatPanel（跟随 `selection.activeSession`）。
 *
 * `session-changed` 的主窗口到页签桥接（2026-11-21）：
 *   页签模式下 ChatPanel bound 只读 props、不回写全局选中（17 §10 口径 5），
 *   而 App 启动 / Ask 中心「聊一聊」/ 项目切换等**既有生产者**仍只发 `session-changed`
 *   → 若未做桥接，一旦有任一页签存在（例如用户点过工作区树的会话），主对话区就
 *   不再响应这些切换（表现为「点了没反应」/ e2e 会话 tag 不更新）。
 *   本组件是页签的 owner，故在此把全局会话选择**翻译成页签动作**：
 *     - 非空 session_id：打开/激活该会话页签（workdir 取载荷 / 活动页签 / 全局工作目录）；
 *     - 空（新建会话）：开「草稿页签」（空身份、显示空态）；首条消息自分配 id 后经
 *       `session-changed` 回填该页签（`bindDraftSession`，不重建组件）。
 *   无页签时（未绑定面板在显示）不做动作——面板自身已按 store 跟随，且此时新建页签会
 *   卸载正在发送首条消息的组件。
 */
import { computed, onMounted, onUnmounted } from 'vue'
import Icon from '@ckfw/components/icon/Icon.vue'
import ChatPanel from '../chat/ChatPanel.vue'
import { useWorkspacesStore } from '../../stores/workspaces'
import { useSelectionStore } from '../../stores/selection'
import { EventNames } from '../../events/event-names'
import mq from '@ckfw/utils/mq.js'

defineOptions({ name: 'WorkspaceContent' })

const store = useWorkspacesStore()
const selection = useSelectionStore()
const tabs = computed(() => store.tabs)
const active = computed(() => store.active)
const activeTab = computed(() => store.activeTab)

function dataDirOf(workDir) {
  const p = store.projects[String(workDir || '').replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()]
  return (p && p.dataDir) || ''
}

/** 页签归属 workdir：载荷 → 该会话在已登记项目列表中的归属 → 活动页签 → 全局工作目录 → 首个项目。 */
function targetWorkDir(workDir, sessionId) {
  if (workDir) return String(workDir)
  const sid = String(sessionId || '')
  if (sid) {
    for (const p of store.projectList) {
      if ((p.sessions || []).some((s) => (s && (s.session_id || s.id)) === sid)) return p.workDir
    }
  }
  if (activeTab.value && activeTab.value.workDir) return activeTab.value.workDir
  if (active.value.workDir) return active.value.workDir
  if (selection.workDir) return selection.workDir
  const first = store.projectList[0]
  return (first && first.workDir) || ''
}

let unsubSession = null
onMounted(() => {
  unsubSession = mq.on(EventNames.sessionChanged, (p) => {
    const sid = String((p && p.session_id) || '')
    const wd = targetWorkDir(p && p.work_dir, sid)
    if (!store.tabs.length) return // 未绑定面板在显示：由它跟随 store，避免卸载发送中的组件
    if (!sid) {
      // 新建会话：空身份草稿页签（同一 workdir 复用）。
      if (activeTab.value) store.openDraft(wd)
      return
    }
    const cur = activeTab.value
    if (cur && cur.draft) {
      store.bindDraftSession(cur.tabId, sid) // 草稿页签首条消息：原地落成真实会话
      return
    }
    if (wd) store.openSession(wd, sid, '')
  })
})
onUnmounted(() => {
  if (unsubSession) unsubSession()
  unsubSession = null
})
</script>

<style scoped>
.ws-content { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.ws-tabs { display: flex; align-items: stretch; gap: 1px; overflow-x: auto; background: var(--bg-secondary); border-bottom: 1px solid var(--border); flex: none; }
.ws-tab { display: flex; align-items: center; gap: 4px; padding: 5px 8px; max-width: 220px; cursor: pointer; color: var(--text-secondary); font-size: 12px; border-right: 1px solid var(--border); }
.ws-tab.active { color: var(--text-primary); background: var(--bg-primary); }
.ws-tab-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ws-tab-close { width: 12px; height: 12px; opacity: .6; }
.ws-tab-close:hover { opacity: 1; }
.ws-body { flex: 1; min-height: 0; }
</style>