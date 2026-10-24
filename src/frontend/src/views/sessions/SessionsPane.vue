<template>
  <!-- 会话导航（P3-C1，2026-09-24）：原「会话抽屉」内容整体迁入左侧导航「会话」页签。
       两行布局（标题 / #id + 轮数，超出省略号）；hover → 操作图标显示在文本下方；
       操作 = 重命名 · 删除 · fork（派生对话，full 档）· 复制对话（json-array，不含 system）
              · 用新窗口打开（纯对话窗口，多窗口；T-3）。 -->
  <div ref="paneRef" class="sessions-pane">
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
          <div class="session-title-row">
            <!-- F8 · 回看入口（单聊 §3.4.6 / D32）：主会话行下挂**子聊天**子节点——展开时经
                 `subchat-list{parent_id}` **懒加载**（**含已结束**）；点击 → `gui.window.open-chat`。
                 仅 `kind ∈ {sub_chat, group_chat}`（`subchat-list` 已过滤；`sub_session` 仍走任务树）。 -->
            <button
              v-if="!subchatToggleHidden(session)"
              type="button"
              class="session-subchat-toggle"
              :disabled="!!subchatLoading[session.session_id]"
              :title="$t('chat.subchat_toggle')"
              @click.stop="toggleSubChats(session)"
            >
              <Icon :name="subchatsExpanded(session) ? 'arrow-down' : 'arrow-right'" :size="12" />
            </button>
            <div v-if="editingId !== session.session_id" class="session-title">{{ session.title || $t('chat.untitled') }}</div>
            <!-- 就地改名（与文件改名同范式）：Enter 提交 / Esc 取消 / 失焦提交；交互不触发会话选中 -->
            <Input
              v-else
              :model-value="editingValue"
              :maxlength="MAX_TITLE"
              @update:model-value="editingValue = $event"
              class="session-title-input"
              @keyup.enter="confirmRename"
              @keyup.escape="cancelRename"
              @blur="confirmRename"
              @mousedown.stop
              @click.stop
            />
          </div>
          <div class="session-meta">
            <span class="session-id">#{{ session.session_id?.slice(0, 8) }}</span>
          </div>
        </div>
        <div v-if="subchatsExpanded(session)" class="session-subchats">
          <div v-if="subchatsOf(session).length === 0" class="session-subchat-empty">
            {{ $t('chat.subchat_none') }}
          </div>
          <div
            v-for="c in subchatsOf(session)"
            :key="c.session_id"
            class="session-subchat"
            @click.stop="openSubChatNode(c)"
          >
            <!-- 单聊 / 群聊**图标区分**（群聊 §4 D41：下挂入口与单聊完全统一，唯一区别 = 图标） -->
            <Icon :name="isGroupChatKind(c.kind) ? 'group' : 'chat-dot-round'" :size="12" class="session-subchat-icon" />
            <span class="session-subchat-name" :class="{ ready: c.lifecycle === SUBCHAT_READY }">{{ subChatName(c) }}</span>
            <span class="session-subchat-state">{{ $t(subChatStateKey(c.lifecycle)) }}</span>
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
          <!-- fork（P4）：派生当前对话（full 档 = 整树拷贝 + id 重映射，`data-session-fork`）。
               保持既有按钮序位（第 1=重命名 / 第 2=删除 / 第 3=fork），回归用例依赖。 -->
          <Button
            size="small"
            text
            :title="$t('chat.fork_session')"
            @click.stop="handleFork(session)"
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
import { ref, nextTick, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Input, confirm, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import EmptyState from '../../components/common/EmptyState.vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgTopics, FieldKeys } from '../../events/msgkeys'
import * as sessionApi from '../../api/session'
import { buildCopyMessages, serializeCopyMessages } from '../../utils/sessionCopy'
import { useChatWindows } from '../../composables/useChatWindows'
import { windowForSession } from '../../utils/chatWindows'
import { listSubChats } from '../../composables/useSubChats'
import { SUBCHAT_READY, subChatName, subChatStateKey } from '../../utils/subchat.js'
import { isGroupChatKind } from '../../utils/groupchat.js'
import { useSelectionStore } from '../../stores/selection'
// INPUTVAL-1：会话标题 ≤128 字符（空 = 未命名，不提交空值）。
import { MAX_TITLE } from '../../utils/fieldRules'

defineOptions({ name: 'SessionsPane' })

const { t } = useI18n()

// 跨 view 共享状态（UILAYOUT-1 §3.7）：主会话唯一真相源 = store 的 `activeSession.sessionId`。
const selection = useSelectionStore()

const sessions = ref([])
// 本组件内的**副本**（会话行 active 高亮 / 「删除当前会话」判定 / 开窗入口态）；写入口径见 load()
// —— store 优先、后端仅兜底（裁决 1，2026-10-16）。
const currentSessionId = ref(null)
const copyingId = ref('')

// 就地改名状态（与文件树改名同范式）：editingId = 正在改名的会话 id；editingValue = 输入值。
const paneRef = ref(null)
const editingId = ref('')
const editingValue = ref('')

// ── F8 · 会话列表挂子聊天（单聊 §3.4.6 / D32）────────────────────────
// 主会话行下挂**子聊天**子节点：**展开时懒加载**（`subchat-list{parent_id}`，返回**含已结束**）；
// 点击子节点 → `gui.window.open-chat`（幂等）。子聊天属「子会话域」，不改 `data-session-list`。
const subchatChildren = ref({}) // parentId → 子聊天清单
const subchatExpanded = ref({}) // parentId → 是否展开
const subchatLoading = ref({})  // parentId → 取数中（防连点）

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

// ── F8 · 子聊天子节点 ────────────────────────────────────────────────
function subchatsExpanded(session) {
  return !!subchatExpanded.value[session && session.session_id]
}

function subchatsOf(session) {
  const sid = session && session.session_id
  return (sid && subchatChildren.value[sid]) || []
}

/** 已加载且为空 → 隐藏展开入口（懒加载未知前保留入口）。 */
function subchatToggleHidden(session) {
  const sid = session && session.session_id
  if (!sid) return true
  const list = subchatChildren.value[sid]
  return Array.isArray(list) && list.length === 0 && !subchatExpanded.value[sid]
}

/** 按父会话取子聊天（**含 `finished`** —— 会话列表是「已结束」的唯一回看入口，§3.4.6）。 */
async function loadSubChatsOf(sid) {
  if (!sid) return []
  try {
    const list = await listSubChats(sid)
    subchatChildren.value = { ...subchatChildren.value, [sid]: list }
    return list
  } catch (e) {
    console.warn('[SessionsPane] subchat-list failed:', e)
    return []
  }
}

/** 展开 / 收起：**首次展开时懒加载**（后续展开直接用已加载清单）。 */
async function toggleSubChats(session) {
  const sid = session && session.session_id
  if (!sid) return
  const next = !subchatExpanded.value[sid]
  subchatExpanded.value = { ...subchatExpanded.value, [sid]: next }
  if (next && !Array.isArray(subchatChildren.value[sid]) && !subchatLoading.value[sid]) {
    subchatLoading.value = { ...subchatLoading.value, [sid]: true }
    await loadSubChatsOf(sid)
    subchatLoading.value = { ...subchatLoading.value, [sid]: false }
  }
}

/** 子聊天子节点点击 → 打开 / 前置纯对话窗口（幂等）；撞窗口上限 → 明确提示（§6 E5；
    群聊为**独立额度** → 用群聊专属文案，群聊 §9 D28/D30）。 */
async function openSubChatNode(item) {
  const sid = item && item[FieldKeys.session_id]
  if (!sid) return
  const r = await openSessionChatWindow(sid)
  if (!r || !r.ok) {
    message.warning(t(isGroupChatKind(item[FieldKeys.kind])
      ? 'chat.group_chat_window_limit'
      : 'chat.chat_window_limit'))
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
    // UILAYOUT-1 裁决 1（2026-10-16）：store 优先、后端兜底。
    //  · store 非空 → 取 store 值（**共享会话已在飞**，如 ChatPanel 新建会话已置新 id、
    //    `setActiveSessionID` 尚未落库）；此时**不得**用后端 `session-active-get` 的旧值覆盖，
    //    否则会把共享会话清掉（与本组件已订阅的事件链语义冲突）。
    //  · store 为空 → 用后端活动会话兜底（首屏 / 前端尚未收到 `session-changed`）。
    const storeSid = selection.activeSessionId
    currentSessionId.value = storeSid || active?.session_id || null
    // F8：已加载过的子聊天随列表一并刷新（打开/切回「会话」页签即与最新状态对齐）。
    const pids = Object.keys(subchatChildren.value)
    if (pids.length) await Promise.all(pids.map((pid) => loadSubChatsOf(pid)))
  } catch (e) {
    console.warn('[SessionsPane] Failed to load sessions:', e)
  }
}

// 选中会话：置活动会话 + 广播（聊天气泡按新会话加载）；本页签保持打开（不再有关闭动作）
// T-4 / T-5（I-1 推论）：该会话**已被对话窗口绑定** → 主窗口**不切换**，改为**激活**该窗口。
async function handleSelect(session) {
  if (!session) return
  // 改名态中切换会话 → 先提交未决改名（避免编辑框残留）
  if (editingId.value && editingId.value !== session.session_id) confirmRename()
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

// fork（P4 已落地）：派生当前对话（full 档 = 整树拷贝 + id 重映射），完成后刷新列表。
async function handleFork(session) {
  if (!session) return
  const dlg = session.dialog_id || sessionApi.sessionDialogOf(session.session_id)
  if (!dlg) {
    message.warning(t('chat.fork_failed'))
    return
  }
  try {
    await sessionApi.forkSession(dlg)
    message.success(t('chat.fork_done'))
    await load()
  } catch (e) {
    console.error('[SessionsPane] fork FAILED:', e)
    message.error(t('chat.fork_failed') + ' ' + (e.message || e))
  }
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

// 就地改名（inplace-editor，与文件改名同范式）：右键/按钮触发 → 标题行内渲染 Input，
// Enter 提交、Esc 取消、失焦提交；提交仍走 sessionApi.updateSessionTitle。
function startRename(session) {
  if (!session) return
  editingId.value = session.session_id
  editingValue.value = session.title || ''
  nextTick(() => {
    const input = paneRef.value?.querySelector('.session-title-input')
    if (!input) return
    input.focus()
    input.select?.()
  })
}

function cancelRename() {
  if (!editingId.value) return
  editingId.value = ''
  editingValue.value = ''
}

// 提交改名（编辑态来自 editingId；会话对象按 id 在当前列表内查找）。
function confirmRename() {
  const id = editingId.value
  if (!id) return
  const s = sessions.value.find(x => x.session_id === id)
  const val = (editingValue.value || '').trim()
  editingId.value = ''
  editingValue.value = ''
  if (!val || val === ((s && s.title) || '')) return
  sessionApi.updateSessionTitle(id, val)
    .then(() => sessionApi.listSessions())
    .then(list => { sessions.value = list || [] })
    .catch(e => message.error(t('chat.rename_failed') + ' ' + (e.message || e)))
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
    if (session) startRename(session)
  }))
  _unsubs.push(mq.on(EventNames.sessionDelete, ({ session }) => {
    if (session) handleDelete(session)
  }))
  // F8：已加载的父会话 → 子聊天状态变化（`subchat-opened` / `subchat-state`）时刷新其子节点
  // （未加载过的父会话不在这里预取 —— 仍是展开时懒加载）。
  const refreshParent = (d) => {
    const pid = d && d[FieldKeys.parent_session_id]
    if (!pid) return
    if (Object.prototype.hasOwnProperty.call(subchatChildren.value, pid)) loadSubChatsOf(pid)
  }
  _unsubs.push(mq.on(MsgTopics.subchatOpened, refreshParent))
  _unsubs.push(mq.on(MsgTopics.subchatState, refreshParent))
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
/* 标题行：展开箭头（F8 子聊天）+ 标题/改名输入框 */
.session-title-row {
  display: flex;
  align-items: center;
  gap: 2px;
  min-width: 0;
}
.session-subchat-toggle {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}
.session-subchat-toggle:hover {
  color: var(--accent);
}
.session-title {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 子聊天子节点（F8）：缩进挂在父会话行下；点击打开对话窗口（含已结束 → 只读回看） */
.session-subchats {
  display: flex;
  flex-direction: column;
  gap: 1px;
  margin: 0 0 2px 18px;
  padding-left: 6px;
  border-left: 1px solid var(--border);
}
.session-subchat {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 4px;
  border-radius: 4px;
  cursor: pointer;
}
.session-subchat:hover {
  background: var(--bg-hover);
}
.session-subchat-icon {
  flex-shrink: 0;
  color: var(--text-muted);
}
.session-subchat-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--text-secondary);
}
.session-subchat-name.ready {
  color: var(--accent);
  font-weight: 600;
}
.session-subchat-state,
.session-subchat-empty {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
}
.session-subchat-empty {
  padding: 2px 4px;
}
/* 就地改名的输入框（Input 根节点即 <input>，class 直接落在它上面） */
.session-title-input {
  font-size: 13px;
  font-weight: 600;
  padding: 2px 6px;
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
