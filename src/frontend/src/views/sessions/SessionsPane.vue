<template>
  <!-- 会话导航（P3-C1，2026-09-24）：原「会话抽屉」内容整体迁入左侧导航「会话」页签。
       两行布局（标题 / #id + 轮数，超出省略号）；hover → 操作图标显示在文本下方；
       操作 = 重命名 · 删除 · fork（占位「敬请期待」）· 复制对话（json-array，不含 system）
              · 用新窗口打开（纯对话窗口，多窗口；T-3）。 -->
  <div class="sessions-pane">
    <!-- 第一行：新建会话 / 新建对话窗口（图标按钮） -->
    <div class="sp-row">
      <span class="sp-heading">{{ $t('chat.session_list') }}</span>
      <span class="sp-actions">
        <Button
          text
          size="small"
          class="sp-new"
          :title="$t('chat.new_session')"
          v-mq:[EventNames.sessionCreate].click
        >
          <Icon name="circle-plus" :size="14" />
        </Button>
        <!-- T-2「新建对话窗口」（会话列表标题栏，24 §6.4）：目标 = **新会话**（session_id 前端分配）；
             达上限（≤5 个独立对话窗口）→ 置灰（`open-chat` 亦会返回 ok=false）。 -->
        <Button
          text
          size="small"
          class="sp-new-chat"
          :disabled="atLimit"
          :title="atLimit ? $t('chat.chat_window_limit') : $t('chat.new_chat_window')"
          @click="handleNewChatWindow"
        >
          <Icon name="new-window" :size="14" />
        </Button>
      </span>
    </div>

    <div class="sp-list">
      <EmptyState v-if="sessions.length === 0" :message="$t('chat.no_sessions')" />

      <div
        v-for="session in sessions"
        :key="session.session_id"
        class="session-card"
        :class="{ active: session.session_id === currentSessionId }"
        v-mq:[EventNames.sessionSelect].click="{ session }"
      >
        <div class="session-info">
          <div class="session-title">{{ session.title || $t('chat.untitled') }}</div>
          <div class="session-meta">
            <span class="session-id">#{{ session.session_id?.slice(0, 8) }}</span>
          </div>
        </div>
        <div class="session-actions">
          <Button
            size="small"
            text
            :title="$t('common.rename_session')"
            v-mq:[EventNames.sessionRename].click.stop="{ session }"
          >
            <Icon name="edit" :size="13" />
          </Button>
          <Button
            size="small"
            text
            type="danger"
            :title="$t('common.delete_session')"
            v-mq:[EventNames.sessionDelete].click.stop="{ session }"
          >
            <Icon name="delete" :size="13" />
          </Button>
          <!-- fork（A8 占位）：真实分支会话功能未实现，点击仅提示「敬请期待」。
               保持既有按钮序位（第 1=重命名 / 第 2=删除 / 第 3=fork），回归用例依赖。 -->
          <Button
            size="small"
            text
            :title="$t('chat.fork_coming_soon')"
            @click.stop="handleFork"
          >
            <Icon name="fork" :size="13" />
          </Button>
          <!-- 复制对话（P3-C1）：不含 system、仅对话、全部对话（非压缩后）、与发给 LLM 一致（json-array） -->
          <Button
            size="small"
            text
            :title="$t('chat.copy_session_dialog')"
            :loading="copyingId === session.session_id"
            @click.stop="handleCopy(session)"
          >
            <Icon name="copy-document" :size="13" />
          </Button>
          <!-- T-3「用新窗口打开」（24 §6.4）：主窗当前会话 → **置灰**（I-1 ①）；已达上限 → **置灰**；
               已开窗 → 不置灰，点击**激活**该窗口（幂等，I-1 ②）。 -->
          <Button
            size="small"
            text
            class="open-chat-window-btn"
            :disabled="isChatEntryDisabled(session)"
            :title="chatEntryTitle(session)"
            @click.stop="handleOpenChatWindow(session)"
          >
            <Icon name="new-window" :size="13" />
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, confirm, promptInput, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import EmptyState from '../../components/common/EmptyState.vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import * as sessionApi from '../../api/session'
import { buildCopyMessages, serializeCopyMessages } from '../../utils/sessionCopy'
import { useChatWindows } from '../../composables/useChatWindows'
import { windowForSession } from '../../utils/chatWindows'

defineOptions({ name: 'SessionsPane' })

const { t } = useI18n()

const sessions = ref([])
const currentSessionId = ref(null)
const copyingId = ref('')

// 纯对话窗口入口（T-2…T-5，24 §6.4 / 不变量 I-1）：已开窗口清单 + 新会话开窗。
// `windows` **只含对话窗口**（主窗口不入注册表）→ 「主窗当前会话」由本地 currentSessionId 判定。
const {
  windows,
  atLimit,
  refreshWindows,
  openNewChatWindow,
  openSessionChatWindow,
  entryStateFor,
} = useChatWindows()

/** 该会话开新窗入口态（'open' / 'activate' / 'disabled'；main-active 判定在前端）。 */
function entryStateOf(session) {
  return entryStateFor(session && session.session_id, currentSessionId.value)
}

function isChatEntryDisabled(session) {
  return entryStateOf(session).action === 'disabled'
}

/** 入口提示（置灰原因 / 激活提示 / 建窗提示；24 §6.4「置灰 / 拒绝条件」）。 */
function chatEntryTitle(session) {
  const st = entryStateOf(session)
  if (st.reason === 'main-active') return t('chat.chat_window_main_active')
  if (st.reason === 'limit') return t('chat.chat_window_limit')
  if (st.reason === 'opened') return t('chat.activate_chat_window')
  return t('chat.open_in_new_window')
}

/** T-3：对已有会话开窗（已开 → 宿主幂等**激活**；不新建、不切主窗口）。 */
async function handleOpenChatWindow(session) {
  if (!session || isChatEntryDisabled(session)) return
  const r = await openSessionChatWindow(session.session_id)
  if (!r || !r.ok) message.warning(t('chat.chat_window_limit'))
}

/** T-2：新会话开窗（session_id 前端分配 + session-ensure；达上限由入口置灰拦下）。 */
async function handleNewChatWindow() {
  if (atLimit.value) return
  const r = await openNewChatWindow()
  if (!r || !r.ok) {
    message.warning(t(r && r.reason === 'ensure-failed' ? 'chat.chat_window_open_failed' : 'chat.chat_window_limit'))
  }
}

// load：取会话列表 + 当前活动会话（用于 active 高亮与「删除当前会话」判定）+ 已开对话窗口
// （T-3 置灰依据 = gui.window.list，只含对话窗口）。
async function load() {
  try {
    const [res, active] = await Promise.all([
      sessionApi.listSessions(),
      sessionApi.getActiveSessionID(),
      refreshWindows(),
    ])
    sessions.value = res || []
    currentSessionId.value = active?.session_id || null
  } catch (e) {
    console.warn('[SessionsPane] Failed to load sessions:', e)
  }
}

// 选中会话：置活动会话 + 广播（聊天气泡按新会话加载）；本页签保持打开（不再有关闭动作）
// T-4 / T-5（I-1 推论）：该会话**已被对话窗口绑定** → 主窗口**不切换**，改为**激活**该窗口。
async function handleSelect(session) {
  if (!session) return
  if (windowForSession(windows.value, session.session_id)) {
    await openSessionChatWindow(session.session_id)
    return
  }
  currentSessionId.value = session.session_id
  sessionApi.setActiveSessionID(session.session_id).catch(e => console.warn('[SessionsPane] setActiveSessionID error:', e))
  mq.emit(EventNames.sessionChanged, { session_id: session.session_id })
}

async function handleDelete(session) {
  try {
    await confirm(
      t('chat.confirm_delete_session_msg', { id: session.session_id?.slice(0, 8) }),
      t('chat.confirm_delete_session_title')
    )
  } catch (e) {
    return
  }

  const wasCurrent = session.session_id === currentSessionId.value
  if (wasCurrent) {
    // 删除当前会话前先取消其 LLM 运行
    mq.emit(EventNames.sessionCancelLlm)
  }

  let ok = false
  try {
    await sessionApi.deleteSession(session.session_id)
    ok = true
  } catch (e) {
    console.error('[SessionsPane] deleteSession FAILED:', e)
    message.error(t('chat.session_delete_failed') + ' ' + (e.message || e))
  }
  if (!ok) return

  sessions.value = sessions.value.filter(s => s.session_id !== session.session_id)
  message.success(t('chat.session_deleted'))

  if (wasCurrent) {
    // 删除当前会话 → 进入「新会话」状态（与新建动作一致：清空当前会话，回到空白输入）
    currentSessionId.value = null
    sessionApi.setActiveSessionID('').catch(e => console.warn('[SessionsPane] setActiveSessionID error:', e))
    mq.emit(EventNames.sessionChanged, { session_id: null })
  }
}

// fork 占位（A8）：真实分支会话功能未实现，仅提示「敬请期待」
function handleFork() {
  message.info(t('chat.fork_coming_soon'))
}

// 复制对话（P3-C1 口径）：逐轮读**原始**消息（非压缩后的快照）→ 去 system → 线格式
// json-array 写剪贴板。轮次缺失/为空 → 提示无内容，不写空数组。
async function handleCopy(session) {
  if (!session || copyingId.value) return
  copyingId.value = session.session_id
  try {
    const turns = await sessionApi.listAllTurns(session.session_id)
    const perTurn = []
    for (const turn of turns) {
      const tid = turn && turn.turn_id // 轮次主键 = turn_id（wire.TurnToWire）
      if (!tid) continue
      perTurn.push(await sessionApi.getTurnMessages(tid))
    }
    const messages = buildCopyMessages(perTurn)
    if (messages.length === 0) {
      message.warning(t('chat.session_copy_empty'))
      return
    }
    await navigator.clipboard.writeText(serializeCopyMessages(messages))
    message.success(t('chat.session_copy_done'))
  } catch (e) {
    console.error('[SessionsPane] copy session failed:', e)
    message.error(t('chat.session_copy_failed') + ' ' + (e.message || e))
  } finally {
    copyingId.value = ''
  }
}

async function handleRename(session) {
  const newName = await promptInput(t('chat.rename_prompt'), session.title || '')
  if (newName !== null && newName.trim() !== '' && newName !== session.title) {
    try {
      await sessionApi.updateSessionTitle(session.session_id, newName.trim())
      sessions.value = await sessionApi.listSessions()
    } catch (e) {
      message.error(t('chat.rename_failed') + ' ' + (e.message || e))
    }
  }
}

async function handleCreate() {
  currentSessionId.value = null
  sessionApi.setActiveSessionID('').catch(e => console.warn('[SessionsPane] setActiveSessionID error:', e))
  mq.emit(EventNames.sessionChanged, { session_id: null })
}

// 交互事件化：v-mq 触发 → 本地执行（原抽屉的会话操作主题不变）
const _unsubs = []
onMounted(() => {
  load()
  // 打开/切到「会话」页签（工具栏 Sessions 按钮 = sessions-open；左侧导航 = filetree-mode-select）→ 重载
  _unsubs.push(mq.on(EventNames.sessionsOpen, load))
  _unsubs.push(mq.on(EventNames.filetreeModeSelect, (d) => {
    if (d && d.mode === 'sessions') load()
  }))
  _unsubs.push(mq.on(EventNames.sessionCreate, handleCreate))
  // 主会话（无父）行首次落库（session-new）→ 即时刷新列表（子会话有 parent_session_id，不进列表）
  _unsubs.push(mq.on(EventNames.sessionNew, ({ parent_session_id }) => {
    if (parent_session_id) return
    sessionApi.listSessions().then(list => { sessions.value = list }).catch(() => {})
  }))
  _unsubs.push(mq.on(EventNames.sessionSelect, ({ session }) => {
    if (session) handleSelect(session)
  }))
  _unsubs.push(mq.on(EventNames.sessionRename, ({ session }) => {
    if (session) handleRename(session)
  }))
  _unsubs.push(mq.on(EventNames.sessionDelete, ({ session }) => {
    if (session) handleDelete(session)
  }))
})
onUnmounted(() => _unsubs.forEach(fn => fn()))
</script>

<style scoped>
.sessions-pane {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
/* 第一行：标题 + 新建（图标） */
.sp-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 4px 8px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--border);
}
.sp-heading {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 标题栏动作组（新建会话 / 新建对话窗口） */
.sp-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}
.sp-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
}
/* 每个会话：两行（标题 / #id + 轮数），超出省略号；hover → 操作图标显示在文本下方 */
.session-card {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px 8px;
  border-radius: 6px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  cursor: pointer;
  transition: border-color var(--transition-fast);
}
.session-card:hover,
.session-card.active {
  border-color: var(--accent);
  background: var(--bg-hover);
}
.session-info {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.session-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.session-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  color: var(--text-muted);
  overflow: hidden;
  white-space: nowrap;
}
.session-id {
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
}
/* 操作图标：hover 时显示在文本下方（一整行，右对齐） */
.session-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 2px;
  opacity: 0;
  transition: opacity var(--transition-fast);
}
.session-card:hover .session-actions {
  opacity: 1;
}
</style>
