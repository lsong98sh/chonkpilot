<template>
  <!-- 纯对话窗口（24 §6.2 / WIN-018）：整页**只有 chat**（无 toolbar/文件树/预览/任务/状态栏）。
       标题栏 = **原生标题栏**（宿主 `Frameless:false`）→ 前端不画自绘标题栏、不提供拖动/最小化/关闭。
       会话**绑定** URL 的 `session-id`（不提供会话切换）；不写 `window.*`/`layout.*`。 -->
  <div class="chat-only-view">
    <ChatPanel :session-id="sessionId" :chat-only="true" />
    <!-- ask-user 监听器（**无任何可视节点**，仅订阅 ask-user → 全局 AskUserManager）：
         对话窗口「可跑 tools」（WIN-018-S05）→ LLM 提问回路必需，否则该窗口无法应答提问。 -->
    <AskUserDialog />
  </div>
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import ChatPanel from './ChatPanel.vue'
import AskUserDialog from './AskUserDialog.vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { getSession } from '../../api/session'
import { claimedInstance } from '../../utils/instanceState'

const props = defineProps({
  /** URL 绑定的会话（`?session-id=…#chat`；由 App.vue 解析传入，固定不变）。 */
  sessionId: { type: String, required: true },
})

// ── 窗口标题（24 §6.3 / WIN-020）──
// 标题 = **会话摘要**（会话 title）；无摘要（新会话）→ 缺省「新会话 + <workdir 目录名>」。
// 仅**独立对话窗口**推送（`gui.window.set-title`），主窗口标题不变（61 §1）。
// workdir 取已认领实例（instance-claim 应答，零额外往返；24 §6.3 C7）。
// ChatOnlyView 受 App 的 `ready`（认领成功）门控 → 挂载时已认领，workDir 必可得。

/** 缺省标题：`新会话 <workdir 目录名>`（与宿主建窗缺省标题同形，避免标题跳变）。 */
function defaultTitle() {
  const wd = (claimedInstance() || {}).workDir || ''
  const base = String(wd).replace(/[\\/]+$/, '').split(/[\\/]/).filter(Boolean).pop() || ''
  return base ? '新会话 ' + base : '新会话'
}

/** 摘要是否有效：空标题、或标题仍等于 `session_id`（数据层 `SessionEnsure` 落库的占位值，
    即「尚未生成摘要」）→ 无效 → 回落缺省标题（24 §6.3 C7）。判据取「已知占位值」而非
    「非空即摘要」，因为 `title` 初值恒为 `session_id`，否则无摘要分支不可达。 */
function hasSummary(title) {
  const t = String(title || '').trim()
  return t !== '' && t !== props.sessionId
}

let lastTitle = ''

/** 标题变化才推送（幂等，避免重复消息）。 */
function applyTitle(summary) {
  const title = hasSummary(summary) ? String(summary).trim() : defaultTitle()
  if (title === lastTitle) return
  lastTitle = title
  mq.emit(EventNames.guiWindowSetTitle, { title })
}

/** 读会话摘要（data-session-get）；读取失败 → 回落缺省标题（不静默留空标题）。 */
async function refreshTitle() {
  try {
    const data = await getSession(props.sessionId)
    applyTitle(data && data.title)
  } catch (e) {
    console.warn('[ChatOnlyView] load session title failed:', e)
    applyTitle('')
  }
}

// 摘要变化来源：会话列表刷新 / 本会话首次落库 / 本轮结束（标题可能就绪）→ 重读比对。
const TITLE_REFRESH_EVENTS = [
  EventNames.sessionRefresh,
  EventNames.sessionNew,
  EventNames.llmComplete,
]

const _unsubs = []

onMounted(() => {
  refreshTitle()
  for (const ev of TITLE_REFRESH_EVENTS) {
    _unsubs.push(mq.on(ev, (d) => {
      if (ev === EventNames.sessionNew && d && d.session_id && d.session_id !== props.sessionId) return
      refreshTitle()
    }))
  }
  // 会话标题变更**下行广播**（61 §3.2 · G-48 ⑦ / #12）：主窗侧改本会话标题 → 本对话窗
  // 标题跟随。**不带 instance_id → 全局**（本窗是另一 instance，故也能收到）；`session_id`
  // 等于本窗绑定的会话才应用（其它会话改名不波及本窗）；载荷直接携带新标题 → 不再往返
  // `data-session-get`。无状态逻辑，故沿用本文件既有的 `mq.on` 订阅（不引入 `watch`）。
  _unsubs.push(mq.on(EventNames.sessionTitleChanged, (d) => {
    if (!d || d.session_id !== props.sessionId) return
    applyTitle(d.title)
  }))
})

onUnmounted(() => {
  _unsubs.forEach(fn => fn())
  _unsubs.length = 0
})
</script>

<style scoped>
.chat-only-view {
  height: 100vh;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
  color: var(--text-primary);
}
</style>
