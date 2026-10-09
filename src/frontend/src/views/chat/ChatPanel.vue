<template>
  <div class="panel-inner">
    <!-- 标题条：主窗口 = 完整（标题/会话号/新建会话/新建对话窗口/场景 Tag）；
         纯对话窗口（chatOnly）= **精简**：只保留**场景 Tag** —— 它是该窗口唯一的场景切换入口（2026-09-26 用户裁决 c）；
         窗口控件（最小化/最大化/关闭）由宿主原生标题栏承担，不在页面内画（WIN-018）。 -->
    <div class="panel-header" :class="{ 'panel-header-mini': chatOnly }">
      <template v-if="!chatOnly">
        <Icon name="chat-dot-square" />
        <span>{{ $t('common.main_chat') }}</span>
        <span
          v-if="currentSessionId"
          class="chat-session-tag"
          :title="currentSessionId"
          style="cursor:pointer"
          v-mq:[EventNames.chatCopySessionId].click
        >#{{ currentSessionId.slice(0, 8) }}</span>
        <Button text class="new-session-btn icon-btn" v-mq:[EventNames.sessionChanged].emit="{ session_id: null }" :title="$t('common.new_session')">
          <Icon name="circle-plus" color="#555" />
        </Button>
        <!-- T-1「新建对话窗口」：**仅主窗口**可见（对话窗口内不设该入口，24 §6.4 / MW-T28）。
             目标 = **新会话**（session_id 由前端分配）；达上限（≤5 个独立对话窗口）→ 置灰。 -->
        <Button
          text
          class="new-chat-window-btn icon-btn"
          :disabled="atLimit"
          :title="atLimit ? $t('chat.chat_window_limit') : $t('chat.new_chat_window')"
          @click="handleNewChatWindow"
        >
          <Icon name="new-window" :size="14" :color="atLimit ? '#bbb' : '#555'" />
        </Button>
        <div class="header-spacer" />
      </template>
      <Popover placement="bottom-end" :width="160">
        <template #reference>
          <!-- 未选场景 = **通用模式**（合法态，非错误）：中性色，不用 danger（2026-09-26 用户裁决） -->
          <Tag size="small" type="info" style="cursor:pointer">
            {{ activeScenarioLabel }}
          </Tag>
        </template>
        <div class="popover-list">
          <!-- 通用场景（下拉固定首项）：选中 = 关闭场景 → 走「无场景」通用模式（不注入场景层）。
               可点 ★ 设为默认（写入 defaultScenario 保留值），重开/重启后仍为通用。 -->
          <div
            class="popover-item scenario-item"
            :class="{ active: !activeScenarioId }"
          >
            <span
              class="scenario-item-name"
              v-mq:[EventNames.scenarioSelect].click="{ id: '' }"
            >{{ $t('chat.general_scenario') }}</span>
            <span
              class="scenario-item-star"
              :class="{ on: defaultScenarioId === GENERAL_SCENARIO }"
              :title="$t('config.setDefaultScenario')"
              v-mq:[EventNames.scenarioSetDefault].stop.click="{ id: GENERAL_SCENARIO }"
            >★</span>
          </div>
          <div
            v-for="s in scenarioOptions"
            :key="s.id"
            class="popover-item scenario-item"
            :class="{ active: activeScenarioId === s.id }"
          >
            <span
              class="scenario-item-name"
              v-mq:[EventNames.scenarioSelect].click="{ id: s.id }"
            >{{ scenarioLabel(s) }}</span>
            <span
              class="scenario-item-star"
              :class="{ on: defaultScenarioId === s.id }"
              :title="$t('config.setDefaultScenario')"
              v-mq:[EventNames.scenarioSetDefault].stop.click="{ id: s.id }"
            >★</span>
          </div>
        </div>
      </Popover>
    </div>
    <div class="chat-panel">
    <MessageList ref="messageListRef" :session-id="currentSessionId" direction="right" />
    <!-- 发送失败后的可行动提示（口径 2026-09-20：本回合失败即提示，不限「未配置」）：非常驻，仅该次失败后出现；CTA 直达「设置 → LLM」。
         非阻塞（不弹模态、不阻断编辑）；同一次失败只提示一次（见 onLlmCompleteHint）；配置一变化即消失（useLLMSetup 回调）。 -->
    <div v-if="llmSetupFailed" class="llm-fail-hint">
      <span class="llm-fail-text">{{ $t('chat.llm_setup_failed_hint') }}</span>
      <button
        type="button"
        class="llm-fail-cta"
        v-mq:[EventNames.previewTabOpen].click="{ kind: 'settings-llm' }"
      >{{ $t('chat.llm_setup_failed_cta') }}</button>
    </div>
    <!-- 继续/重试操作区（输入区上方，**左对齐**）：turn 未完成 →"继续"（**文本按钮**）；存在中断工具 →"重试"；空回复提示。
         显示条件（A7，2026-09-24 用户口径）：正常回复后不显示 / 会话进行中（isLoading）不显示 —— 仅"需要继续"时出现。 -->
    <div
      v-if="(messageListRef?.showContinue || messageListRef?.lastInterruptedPair || messageListRef?.emptyReplyHint) && !messageListRef?.isLoading"
      class="turn-actions"
    >
      <span v-if="messageListRef?.emptyReplyHint" class="empty-reply-hint">{{ $t('chat.empty_reply') }}</span>
      <Button v-if="messageListRef?.showContinue" text size="small" class="continue-btn" v-mq:[EventNames.chatContinue].click>
        {{ $t('common.continue') }}
      </Button>
      <Button v-if="messageListRef?.lastInterruptedPair" class="retry-tool-btn" @click="messageListRef?.onRetryToolClick()">
        {{ $t('chat.retry_tool') }}
      </Button>
    </div>
    <div v-if="taskProgress" class="task-progress-bar">{{ taskProgress }}</div>
    <div v-if="compressing" class="compress-progress-bar">{{ $t('chat.compressing') }}</div>
    <InputBox
      @send="handleSend"
      @cancel="handleCancel"
      @queue="handleQueue"
      :loading="isLoading"
      :queue-count="queueCount"
      :queue-items="queueItems"
    >
      <template #controls>
        <Popover placement="top-end" :width="160">
          <template #reference>
            <Tag type="info" style="cursor:pointer">
              {{ selectedLLMLabel }}
            </Tag>
          </template>
          <div class="popover-list">
            <div
              v-for="(opt, i) in llmOptions"
              :key="opt.value + '#' + i"
              class="popover-item llm-item"
              :class="{ active: selectedLLM === opt.value }"
              @click="onPickLLM(opt)"
            >
              <span class="llm-item-name">{{ opt.label }}</span>
            </div>
          </div>
        </Popover>
        <Button
          text
          class="icon-btn"
          :title="$t('chat.thinking_mode')"
          v-mq:[EventNames.chatToggleThink].click
        >
          <Icon name="think" :size="14" :color="thinkEnabled ? '#1890ff' : '#999'" />
        </Button>
        <Button
          text
          class="icon-btn"
          :title="$t('chat.effort') + ': ' + effortLevel"
          v-mq:[EventNames.chatToggleEffort].click
        >
          <Icon name="effort" :size="14" :color="effortLevel === 'max' ? '#1890ff' : '#999'" />
        </Button>
        <!-- 截图按钮：隐藏本窗口 → GDI 全屏截图 → 附件（与粘贴/拖入图片同一链路）。
             仅在所选 LLM 声明「图形」（vision）能力时可用（否则禁用 + 说明 tooltip）。 -->
        <Button
          text
          class="icon-btn"
          :title="canScreenshot ? $t('chat.screenshot') : $t('chat.screenshot_no_vision')"
          :loading="screenshotting"
          :disabled="!canScreenshot"
          v-mq:[EventNames.chatScreenshot].click
        >
          <Icon name="screenshot" :size="14" :color="screenshotting ? '#1890ff' : '#999'" />
        </Button>
      </template>
    </InputBox>

    <!-- 截图模式：全屏预览 overlay（拖拽选区域裁剪） -->
    <ScreenshotOverlay
      v-if="screenshotImage"
      :image="screenshotImage"
      @done="onScreenshotDone"
      @cancel="closeScreenshot"
    />
  </div>
  </div>
</template>

<script setup>
import { ref, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'

import { getLatestSessionID, getSession, getActiveSessionID, setActiveSessionID } from '../../api/session'
import { getUserConfig, saveUserConfig } from '../../api/config'
import { getScenarioList } from '../../api/scenario'
import { newSessionId } from '../../api/chat'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgTopics, GuiUploadKeys, GuiCaptureKeys } from '../../events/msgkeys'
import MessageList from './MessageList.vue'
import InputBox from './InputBox.vue'
import ScreenshotOverlay from './ScreenshotOverlay.vue'
import Icon from '../../components/icon/Icon.vue'
import { Button, Tag, Popover, message } from '../../components/ui'
import { useTaskView } from '../../composables/useTaskView'
import { sendQueue } from '../../composables/useSendQueue'
import { useChatWindows } from '../../composables/useChatWindows'
import { useLLMSetup } from '../../composables/useLLMSetup'
import { useCompressStatus } from '../../composables/useCompressStatus'
import { needsLLMConfigHint } from '../../utils/llmSetup'
import { pluginFailureText } from '../../utils/pluginNotice'

const { t, te } = useI18n()

// 纯对话窗口适配（24 §6.2 / WIN-018）：
//   - `chatOnly=true` → 绑定 URL 传下来的 `sessionId`（**不切换会话**、不读/不写活动会话）；
//   - 主窗口（默认）→ 沿用既有「活动会话 / 最近会话」启动语义。
const props = defineProps({
  /** 绑定会话（对话窗口 = URL 的 session-id；主窗口不传）。 */
  sessionId: { type: String, default: '' },
  /** 是否纯对话窗口（隐藏 T-1 与「新建会话」等会话切换入口）。 */
  chatOnly: { type: Boolean, default: false },
})

// T-1 入口：新建对话窗口（仅主窗口；达上限置灰）—— 状态来自共享的 useChatWindows 单例。
const { atLimit, openNewChatWindow } = useChatWindows()

const boundSessionId = props.sessionId || ''

const currentSessionId = ref(null)
const messageListRef = ref(null)
const isInitializing = ref(true) // prevent rapid sends before initSession completes

// ── 发送失败后的可行动提示（问题 A，口径变更 2026-09-20）──
// 原「无可用 LLM → 聊天区常驻引导」已移除；改为**发送失败时**才提示（非常驻 UI）。
// 口径：**兜底可用**（D-30 后 = router 内置兜底 echo，无可用 provider 时启用；此前 = 内置项
// `echo`）→ 不以「usr llms 是否为空」作门槛，改为**本回合失败即提示**（`needsLLMConfigHint`，
// 空回复除外）；提示文案同时覆盖「未配置可用 LLM」与「当前 provider 调用失败」两种情形（同一 CTA）。
const llmSetupFailed = ref(false) // 「去配置 LLM」可行动提示是否可见（仅失败后，非常驻）
let llmSetupHintKey = ''          // 已提示过的失败标识（turn id）：同一次失败只提示一次

// useLLMSetup：订阅既有配置刷新（data-user-config-refresh + IDE config-refresh），
// 配置一变化（用户在设置页配好 LLM / 改了 provider）→ **提示立即消失**（不必等下一次发送）。
useLLMSetup(() => { llmSetupFailed.value = false })

// llm-complete{status:error} → 提示去配置（空回复、非 error 终态不提示，见 needsLLMConfigHint）。
function onLlmCompleteHint(d) {
  const sid = currentSessionId.value
  const es = d && (d.session || d.session_id)
  if (sid && es && es !== sid) return
  if (!needsLLMConfigHint(d)) return
  const key = (d && (d.turn || d.turn_id)) || ''
  if (key && key === llmSetupHintKey) return // 同一次失败（同 turn，含自动续写重试）不再重复提示
  llmSetupHintKey = key
  llmSetupFailed.value = true
}

// ── TaskView：会话切换时刷新任务快照 ──
const { refresh: refreshTasks } = useTaskView()

// ── Scenario selector (moved from MainLayout) ──
const activeScenarioId = ref(null)
const scenarioOptions = ref([])
const scenarioPopoverVisible = ref(false)
// 用户配置的"默认选中场景"（defaultScenario = 场景目录名/key；空=未配置 → 回退列表第一个）。
// GENERAL_SCENARIO = 保留值：默认选中「通用场景」（无场景/通用模式）——与"未配置"区分开，
// 否则"显式选通用"会被 loadScenarioOptions 当作未配置而自动选中第一个场景。
const GENERAL_SCENARIO = '__general__'
// 与内置"默认场景"（目录 default）区分：这里是"启动默认选中哪个场景"。
const defaultScenarioId = ref('')

// 未选场景 = **通用模式**（仅全局层提示词 + 全量 hot 工具），是**合法态**（25 §3）；
// UI 文案 = 「通用场景」（下拉固定首项，2026-09-26）——不显示「选择场景」。
const activeScenarioLabel = computed(() => {
  if (!activeScenarioId.value) return t('chat.general_scenario')
  const found = scenarioOptions.value.find(s => s.id === activeScenarioId.value)
  return found ? scenarioLabel(found) : t('chat.general_scenario')
})

// 场景显示名：**统一标注级别**（P5，2026-10-01；原仅同名跨级并存时加后缀）
function scenarioLabel(s) {
  if (!s) return ''
  const n = s.name || s.id
  return `${n} -${t('scenario.level.' + (s.level || 'user'))}`
}

async function loadScenarioOptions() {
  try {
    // data-scenario-list 消息面（20-gui），替代 GetScenarioList RPC
    const res = await getScenarioList()
    scenarioOptions.value = res.scenarios || []
    // 默认选中：当前有效则保持；否则优先"用户配置的默认场景"（defaultScenario），
    // 未配置/已删除则回退列表第一个（与后端 resolveScenario(0) 语义一致）。
    // 例外：defaultScenario = GENERAL_SCENARIO（显式选「通用场景」）→ 保持通用，不自动选场景。
    const currentValid = activeScenarioId.value && scenarioOptions.value.some(s => s.id === activeScenarioId.value)
    if (!currentValid) {
      if (defaultScenarioId.value === GENERAL_SCENARIO) {
        activeScenarioId.value = null
      } else {
        const def = scenarioOptions.value.find(s => s.id === defaultScenarioId.value)
        activeScenarioId.value = def ? def.id : (scenarioOptions.value[0]?.id || null)
      }
    }
  } catch (e) {
    console.error('[ChatPanel] Failed to load scenarios:', e)
    scenarioOptions.value = []
  }
}

// 设为"默认选中场景"（defaultScenario）：保存到用户配置 → 更新星标 → 立即选中。
// id = GENERAL_SCENARIO 时为「通用场景」（保留值）：默认选中通用 → activeScenarioId 置空。
async function setDefaultScenario(id) {
  if (!id) return
  const general = id === GENERAL_SCENARIO
  try {
    await saveUserConfig({ defaultScenario: general ? GENERAL_SCENARIO : id })
    defaultScenarioId.value = general ? GENERAL_SCENARIO : id
    activeScenarioId.value = general ? null : id
    message.success(t('config.defaultScenarioSet'))
  } catch (e) {
    console.warn('[ChatPanel] set default scenario failed:', e)
    message.error(String((e && e.message) || e))
  }
}

function selectScenario(id) {
  // 空 id = 下拉首项「通用场景」→ 归一为 null（无场景，走通用模式）
  activeScenarioId.value = id || null
}

// ── Scroll helpers (exposed to parent) ──
function scrollTop() {
  messageListRef.value?.scrollTop()
}
function scrollBottom() {
  messageListRef.value?.scrollBottom()
}
defineExpose({ scrollTop, scrollBottom })

// ── LLM controls ──
const llmList = ref([]) // usr llms（data-user-config-load，可编辑）
const selectedLLM = ref('')
const thinkEnabled = ref(true)
const effortLevel = ref('high')
const screenshotting = ref(false) // 截图进行中（按钮 loading）
// 截图模式：全屏预览 overlay（截图 dataURL，非空 = 截图模式）
const screenshotImage = ref('')
async function onScreenshotDone(dataUrl) {
  try {
    // gui.upload（61-消息一览 §1）：附件落盘数据根 tmp/uploads → result {url, path, file_id, name}
    const env = await mq.emit(MsgTopics.guiUpload, { name: 'screenshot.png', data: dataUrl, kind: 'image' })
    const res = env && env.backend && env.backend.result
    if (res && res[GuiUploadKeys.path]) {
      mq.emit(EventNames.chatInsertAttachment, {
        kind: 'image',
        name: res[GuiUploadKeys.name] || 'screenshot.png',
        path: res[GuiUploadKeys.path],
        url: res[GuiUploadKeys.url],
        fileId: res[GuiUploadKeys.file_id],
      })
    }
  } catch (e) {
    // 上传失败必须可见（调试日志保留）
    console.warn('[ChatPanel] screenshot upload failed:', e)
    message.error(t('chat.screenshot_failed'))
  } finally {
    screenshotImage.value = ''
  }
}
function closeScreenshot() {
  screenshotImage.value = ''
}
const llmPopoverVisible = ref(false)

// LLM 选项列表 = usr llms 记录（value/label = provider name，发给 llm-start.llm）。
const llmOptions = computed(() => [
  ...llmList.value.map(l => ({ value: l.name || '', label: l.name || '', kind: 'user', model: l.model || '' })),
])

// 选中项 → llm-start.llm 传值（provider name）。
function selectedLLMPayloadName() {
  return selectedLLM.value
}

const selectedLLMLabel = computed(() => {
  const opt = llmOptions.value.find(o => o.value === selectedLLM.value)
  return opt ? opt.label : t('chat.default_llm')
})

// 模型能力（usr llms 记录的 capabilities；未声明 → 空数组）
const selectedLLMCaps = computed(() => {
  const rec = llmList.value.find(l => (l.name || '') === selectedLLM.value)
  return rec && Array.isArray(rec.capabilities) ? rec.capabilities : []
})
// 截图前提 = 所选 LLM 声明「图形」（vision）能力；未声明 → 按钮禁用（不静默失败）
const canScreenshot = computed(() => selectedLLMCaps.value.includes('vision'))

// 选择 LLM（本组件选择器）：更新选中态 + 广播当前 LLM（MessageList 的 currentLlm / 待发送队列跟随）。
// 广播值 = llm-start.llm 的 provider name。
function onPickLLM(opt) {
  selectLLM(opt.value)
  mq.emit(EventNames.chatSelectLlm, { name: selectedLLMPayloadName() })
}

function selectLLM(name) {
  selectedLLM.value = name
  llmPopoverVisible.value = false
}

// 点击 session id Tag → 复制完整 id（FP：悬停显示完整 id，点击复制）
async function copySessionId() {
  if (!currentSessionId.value) return
  try {
    await navigator.clipboard.writeText(currentSessionId.value)
    message.success(t('common.copied'))
  } catch (e) {
    console.warn('[ChatPanel] copy session id failed:', e)
  }
}

// T-1：新建对话窗口（**新会话**；先 session-ensure 再 open-chat，24 §6.4）。
// 失败 = 达上限（宿主 ok=false）或建会话失败 → 明确提示，不静默。
async function handleNewChatWindow() {
  if (atLimit.value) return
  const r = await openNewChatWindow()
  if (r && r.ok) return
  message.warning(t(r && r.reason === 'ensure-failed' ? 'chat.chat_window_open_failed' : 'chat.chat_window_limit'))
}

function toggleEffort() {
  effortLevel.value = effortLevel.value === 'high' ? 'max' : 'high'
}

// ── Computed loading state (from MessageList) ──
const isLoading = computed(() => messageListRef.value?.isLoading ?? false)

// ── Task progress (kept in ChatPanel for display; non-message events) ──
const taskProgress = ref('')

// ── 上下文压缩指示（OP-03）：compress-start/compress-done 驱动（会话级）──
const { isCompressing, handleNotice: handleCompressNotice } = useCompressStatus()
const compressing = computed(() => isCompressing(currentSessionId.value))

// ── Tool task notifications (executor tool-notify events) ──
const pendingNotifications = ref([])

function sendNotify(text) {
  if (!text) return
  // 仅前端提示，不再转发为 LLM 消息：LLM 已通过该工具的 tool_result 得知
  // task_id 与 running 状态，把系统通知伪装成用户消息会造成双发与上下文污染。
  message.info(text)
}

function flushPendingNotifications() {
  if (pendingNotifications.value.length === 0) return
  const items = pendingNotifications.value.splice(0)
  items.forEach((text) => sendNotify(text))
}

// ── 待发队列（当前 LLM）指示：分桶键 = provider name（附录 A：队列只承载用户消息）──
// 用 selectedLLMPayloadName() 而非 selectedLLM：MessageList 的 currentLlm 存的是广播出去的
// provider name（「系统默认（启动参数）」= 空串），两边必须同键才不会漏计。
const queueCount = computed(() => sendQueue.countOf(selectedLLMPayloadName()))
const queueItems = computed(() => sendQueue.itemsOf(selectedLLMPayloadName()))

// ── 发送消息（LLM 空闲时）──
// busy 时无【发送】按钮（只有【取消】【入队】），此入口仅在空闲态可用；
// 直接 doSend（MessageList onMessageSend → sendChatMessage），不入队。
async function handleSend(text) {
  if (!text.trim() || isInitializing.value) return
  llmSetupFailed.value = false // 新一次发送：上一次失败的提示不再保留（本次若失败会重新提示）
  const cfgErr = llmConfigReady()
  if (cfgErr) {
    message.warning(t(cfgErr))
    // 发送被拦截：InputBox 已同步清空 textarea，下一 tick 把用户输入写回（复用 chat-queue-restore 通道）
    nextTick(() => mq.emit(EventNames.chatQueueRestore, { text }))
    return
  }
  const sid = await ensureSessionId()
  if (!sid) return
  // Wait for next tick to ensure prop has propagated to MessageList
  await nextTick()
  mq.emit(EventNames.messageSend, buildMessagePayload(sid, text))
}

// ── 入队（LLM 忙碌时）──
// 仅前端操作：MessageList onMessageQueue 把文本放入待发队列（不写 DB、不启动 turn、
// 不显示气泡），等 LLM 结束后由 drain 发送（docs/spec/00-overview/01-端到端数据流.md §5.3）。
async function handleQueue(text) {
  if (!text.trim() || isInitializing.value) return
  llmSetupFailed.value = false // 同上：新一次发送（入队）清掉上一次失败的提示
  const cfgErr = llmConfigReady()
  if (cfgErr) {
    message.warning(t(cfgErr))
    nextTick(() => mq.emit(EventNames.chatQueueRestore, { text }))
    return
  }
  const sid = await ensureSessionId()
  if (!sid) return
  await nextTick()
  mq.emit(EventNames.messageQueue, buildMessagePayload(sid, text))
}

// S25 前端发送前校验 LLM 配置（35-错误处理与恢复 S25）：
// 无可选项 / 未选中任何 LLM / 选中项缺 model → 返回 i18n key 提示去配置，不发起请求
// （发送前校验，避免空配置发请求后 401/失败）。兜底由 router 承担：无可用 provider 时后端
// 仍能回话（内置 echo），失败则走 needsLLMConfigHint 提示。
// apiKey 不在此校验（配置系统本就允许空 key，本地模型无需 key；缺 key 的 401 走 S16 分类）。
function llmConfigReady() {
  if (!llmOptions.value || llmOptions.value.length === 0) return 'chat.no_llm_configured'
  const llm = llmOptions.value.find(l => l.value === selectedLLM.value)
  if (!llm) return 'chat.no_llm_selected'
  if (llm.kind === 'user' && !llm.model) return 'chat.llm_model_missing'
  return ''
}

// 构建消息 payload（send / queue 共用）：携带当前会话、LLM 与场景配置。
function buildMessagePayload(sid, text) {
  const thinkFlag = thinkEnabled.value ? 'on' : 'off'
  return {
    sessionId: sid,
    text: text,
    llm: selectedLLMPayloadName(),
    thinkFlag: thinkFlag,
    effortLevel: effortLevel.value,
    scenarioId: activeScenarioId.value,
  }
}

// 确保存在会话；无则前端自分配 session uuid 直切（21-llm-server 客户端分配语义，
// 不再调 CreateSession RPC；send / queue 共用）。
// 对话窗口：会话由 URL 固定绑定 → **绝不**自分配/切换（24 §6.2）。
async function ensureSessionId() {
  if (currentSessionId.value) return currentSessionId.value
  if (props.chatOnly) return boundSessionId
  const sid = newSessionId()
  currentSessionId.value = sid
  setActiveSessionID(sid).catch(e => console.warn('[ChatPanel] setActiveSessionID error:', e))
  mq.emit(EventNames.sessionChanged, { session_id: sid })
  return sid
}

// ── Cancel current LLM turn ──
function handleCancel() {
  mq.emit(EventNames.messageCancel)
  flushPendingNotifications()
}

// ── Cancel LLM handler (from SessionsPane via session-cancel-llm) ──
function handleCancelLLM() {
  mq.emit(EventNames.messageCancel)
  flushPendingNotifications()
  import('../../utils/askUserManager').then(({ askUserManager }) => {
    askUserManager.cancelAll()
  })
}

// 解析 defaultLLM（迁移期两种形态，见 64-配置项一览 §3）→ 选择器 value：
//   - 字符串 = provider name（usr llms 名）
//   - 数字 = 旧记录 int 索引（llms[v]；persist 未配置时补 0 / 无 llms 补 -1）
//   - 名字已失效（改名/删除）/ int 越界 → 回落首个可用 usr LLM（既有语义）；无 usr LLM → ''（未选中）
function resolveDefaultLLM(v, llist) {
  if (typeof v === 'string') {
    if (llmOptions.value.some(o => o.value === v)) return v
  } else if (typeof v === 'number' && v >= 0 && v < llist.length) {
    return llist[v].name || ''
  }
  return llist.length ? (llist[0].name || '') : ''
}

// ── Init session on mount ──
async function initSession() {
  try {
    try {
      const ures = await getUserConfig()
      const uc = ures.config || ures
      // 用户配置的"默认选中场景"（defaultScenario；与内置默认场景区分）
      defaultScenarioId.value = (uc && uc.defaultScenario) || ''
      llmList.value = Array.isArray(uc.llms) ? uc.llms : []
      const picked = resolveDefaultLLM(uc.defaultLLM, llmList.value)
      if (picked) {
        selectedLLM.value = picked
        // usr 记录 → think/effort 跟随其配置
        const rec = llmList.value.find(l => l.name === picked)
        if (rec) {
          thinkEnabled.value = rec.thinking !== false
          if (rec.reasoningEffort) {
            effortLevel.value = rec.reasoningEffort
          }
        }
        // 广播当前 LLM（MessageList 的 currentLlm / 待发送队列跟随；哨兵不外发，见 onPickLLM）
        mq.emit(EventNames.chatSelectLlm, { name: selectedLLMPayloadName() })
      }

      // 对话窗口：会话**由 URL 固定绑定** → 不读活动会话、不取最近会话、不写活动会话
      // （24 §6.2 / §3.3 C5）。
      if (props.chatOnly && boundSessionId) {
        currentSessionId.value = boundSessionId
        return
      }

      const activeRes = await getActiveSessionID()
      let targetSessionID = activeRes?.session_id || null

      if (targetSessionID) {
        try {
          const sessionRes = await getSession(targetSessionID)
          if (sessionRes && !sessionRes.parent_id) {
            currentSessionId.value = targetSessionID
            setActiveSessionID(targetSessionID).catch(e => console.warn('[ChatPanel] setActiveSessionID error:', e))
            return
          }
        } catch (e) {
          console.error('[ChatPanel] getSession error:', e)
          // 失败回退到最近会话 → 按可恢复告警可见（E-41，windowsgui 下 console 不可见）
          message.warning(t('chat.session_load_failed'))
        }
      }

      const res = await getLatestSessionID()
      const topSessionID = res?.session_id
      if (topSessionID) {
        currentSessionId.value = topSessionID
        setActiveSessionID(topSessionID).catch(e => console.warn('[ChatPanel] setActiveSessionID error:', e))
        return
      }
    } catch (e) {
      console.warn('[ChatPanel] Failed to init session:', e)
      // 会话初始化失败 → 可见错误提示（E-41，windowsgui 下 console 不可见）
      message.error(t('chat.session_init_failed'))
    }
    currentSessionId.value = null
    setActiveSessionID('').catch(e => console.warn('[ChatPanel] setActiveSessionID error:', e))
  } finally {
    isInitializing.value = false
  }
}

// ── Lifecycle ──
const permanentUnsubs = []

onMounted(() => {
  initSession().then(() => {
    refreshTasks(currentSessionId.value || '', '', { initOnly: true })
  })

  // 工具区按钮事件化（v-mq 触发 → 本地执行 + /publish 通知后端）
  permanentUnsubs.push(mq.on(EventNames.chatCopySessionId, () => {
    if (currentSessionId.value) {
      navigator.clipboard.writeText(currentSessionId.value).then(() => {
        message.success(t('common.copied'))
      }).catch(e => console.warn('[ChatPanel] copy session id failed:', e))
    }
  }))
  permanentUnsubs.push(mq.on(EventNames.chatSelectLlm, ({ name }) => {
    if (name) selectLLM(name)
  }))
  permanentUnsubs.push(mq.on(EventNames.chatToggleThink, () => {
    thinkEnabled.value = !thinkEnabled.value
  }))
  permanentUnsubs.push(mq.on(EventNames.chatToggleEffort, () => {
    toggleEffort()
  }))

  const unsubSessionChanged = mq.on(EventNames.sessionChanged, ({ session_id }) => {
    // 对话窗口：会话由 URL 固定绑定，**不跟随**主窗口/其它入口的会话切换（24 §6.2）。
    if (props.chatOnly) return
    const sid = session_id || null
    if (sid !== currentSessionId.value) {
      currentSessionId.value = sid
    }
    refreshTasks(sid || '', '', { initOnly: true })
  })
  permanentUnsubs.push(unsubSessionChanged)

  const unsubRefresh = mq.on(EventNames.configRefresh, async () => {
    try {
      const ures = await getUserConfig()
      const uc = ures.config || ures
      // 用户配置的"默认选中场景"（defaultScenario；与内置默认场景区分）
      defaultScenarioId.value = (uc && uc.defaultScenario) || ''
      if (uc.llms && uc.llms.length > 0) {
        llmList.value = uc.llms
      }
      // LLM 配置变更（增删/改名/改能力）后：当前选中项已失效或尚未选中 → 重新解析默认 LLM
      // （否则 llmList 已更新、选择器与「模型能力」判定仍停在旧值，须重载页面才生效）。
      if (!llmOptions.value.some(o => o.value === selectedLLM.value)) {
        const picked = resolveDefaultLLM(uc.defaultLLM, llmList.value)
        if (picked) selectLLM(picked)
      }
    } catch (e) { console.warn('[ChatPanel] config:refresh error:', e) }
  })
  permanentUnsubs.push(unsubRefresh)

  // Scenario select & open
  const unsubScenarioSelect = mq.on(EventNames.scenarioSelect, (data) => selectScenario(data.id))
  permanentUnsubs.push(unsubScenarioSelect)
  // 设为"默认选中场景"（defaultScenario → SaveUserConfig）
  const unsubScenarioSetDefault = mq.on(EventNames.scenarioSetDefault, (data) => setDefaultScenario(data && data.id))
  permanentUnsubs.push(unsubScenarioSetDefault)
  const unsubScenarioOpen = mq.on(EventNames.scenarioOpen, loadScenarioOptions)
  const unsubScenarioReload = mq.on(EventNames.scenarioReload, loadScenarioOptions)
  permanentUnsubs.push(unsubScenarioReload)
  permanentUnsubs.push(unsubScenarioOpen)
  loadScenarioOptions()

  // Keep session-cancel-llm listener for SessionsPane integration
  permanentUnsubs.push(mq.on(EventNames.sessionCancelLlm, handleCancelLLM))

  // 消息中心：按精确类型订阅（无 _event_type 转换）
  const sessionGuard = (data) => {
    if (currentSessionId.value && data.session_id && data.session_id !== currentSessionId.value) return false
    return true
  }

  const unsubNotify = mq.on(EventNames.toolNotify, (data) => {
    if (!sessionGuard(data)) return
    // 压缩进度（OP-03，notice=compress-start/compress-done）：驱动会话级「正在压缩上下文」指示，
    // **不弹 toast**（与 tool-notify 其它取值的提示路径分流）。
    if (data.notice === 'compress-start' || data.notice === 'compress-done') {
      handleCompressNotice(data)
      return
    }
    // 完成通知（notice=completion）：chat 窗口已有 user-notify 消息，不再弹 toast（去重）。
    if (data.notice === 'completion') return
    // 插件失败（notice=plugin-failure）：按 plugin/kind 映射 i18n 文案（I-117；zh-CN/en-US 双语），
    // 未登记插件回落宿主 `message`（Go 侧中文兜底，见 utils/pluginNotice.js）。
    const msg = data.notice === 'plugin-failure'
      ? pluginFailureText(data, t, te)
      : (data.message || data.text || '')
    if (msg) {
      if (isLoading.value) {
        pendingNotifications.value.push(msg)
      } else {
        sendNotify(msg)
      }
    }
  })
  permanentUnsubs.push(unsubNotify)

  const unsubProgress = mq.on(EventNames.toolProgress, (data) => {
    if (!sessionGuard(data)) return
    if (data?.task_id) {
      // 进度文案走 i18n（键既有；zh/en 双语，参数 done/total/failed）
      taskProgress.value = t('chat.task_running', { done: data.completed || 0, total: data.total || '?' })
      if (data.failed > 0) taskProgress.value += t('chat.task_failed_suffix', { failed: data.failed })
    }
  })
  permanentUnsubs.push(unsubProgress)

  const unsubLlmError = mq.on(EventNames.llmError, (data) => {
    if (!sessionGuard(data)) return
    const retryable = data.retryable === true
    const attempt = data.retry_attempt || 1
    const maxRetries = data.retry_count || 1
    if (retryable && attempt <= maxRetries) {
      taskProgress.value = t('chat.llm_error_retrying', { attempt, max: maxRetries })
    } else {
      taskProgress.value = ''
    }
  })
  permanentUnsubs.push(unsubLlmError)

  const unsubLlmRetry = mq.on(EventNames.llmRetry, (data) => {
    if (!sessionGuard(data)) return
    const attempt = data.retry_attempt || 1
    const maxRetries = data.retry_count || 1
    const waitSec = data.wait_seconds || 5
    // 重试文案走 i18n（键既有；zh/en 双语，参数 attempt/max/wait）
    taskProgress.value = t('chat.llm_retry', { attempt, max: maxRetries, wait: waitSec })
  })
  permanentUnsubs.push(unsubLlmRetry)

  // 截图：gui.capture（窗口隐藏 + 全屏截图）→ 全屏预览 overlay 拖拽选区域 → 裁剪上传附件。
  // 前置：所选 LLM 须声明「图形」能力（按钮已禁用，此处兜底防事件旁路）。
  const unsubScreenshot = mq.on(EventNames.chatScreenshot, async () => {
    if (!canScreenshot.value) return
    screenshotting.value = true
    try {
      const env = await mq.emit(MsgTopics.guiCapture, {})
      const res = env && env.backend && env.backend.result
      if (res && res[GuiCaptureKeys.b64]) screenshotImage.value = 'data:image/png;base64,' + res[GuiCaptureKeys.b64]
    } catch (e) {
      // 截图失败必须可见（调试日志保留）
      console.warn('[ChatPanel] screenshot failed:', e)
      message.error(t('chat.screenshot_failed'))
    } finally {
      screenshotting.value = false
    }
  })
  permanentUnsubs.push(unsubScreenshot)

  // Clear progress on complete/error, and flush notifications queued during
  // the turn (turn end signal replaces the removed isLoading watch)
  const clearTaskProgress = () => {
    taskProgress.value = ''
    flushPendingNotifications()
  }
  permanentUnsubs.push(mq.on(EventNames.complete, clearTaskProgress))
  permanentUnsubs.push(mq.on(EventNames.error, clearTaskProgress))
  // LLM turn 结束（llm-complete）也是 turn 边界：flush 忙碌期间暂存的通知；
  // 同时判定本轮失败是否「未配置可用 LLM」→ 给出可行动提示（去配置）。
  permanentUnsubs.push(mq.on(EventNames.llmComplete, (d) => {
    clearTaskProgress()
    onLlmCompleteHint(d)
  }))

  const unsubSessionRefresh = mq.on(EventNames.sessionRefresh, async () => {
    // 对话窗口：绑定会话不随列表刷新清空（会话行若被删，窗口保持只读现状，不切会话）。
    if (props.chatOnly) return
    if (currentSessionId.value) {
      try {
        const { getSession } = await import('../../api/session')
        const sessionRes = await getSession(currentSessionId.value)
        if (!sessionRes) {
          currentSessionId.value = null
        }
      } catch (_) {
        currentSessionId.value = null
      }
    }
  })
  permanentUnsubs.push(unsubSessionRefresh)
})

onUnmounted(() => {
  permanentUnsubs.forEach(fn => fn())
  permanentUnsubs.length = 0
})
</script>

<style scoped>
.chat-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.task-progress-bar {
  flex-shrink: 0;
  padding: 3px 12px;
  font-size: 11px;
  color: var(--accent);
  background: var(--bg-surface);
  border-top: 1px solid var(--border);
  text-align: center;
}
/* 上下文压缩指示（OP-03）：会话级「正在压缩上下文」，compress-done（或切换会话）后消失 */
.compress-progress-bar {
  flex-shrink: 0;
  padding: 3px 12px;
  font-size: 11px;
  color: var(--text-muted);
  background: var(--bg-surface);
  border-top: 1px solid var(--border);
  text-align: center;
}

/* ── 发送失败（未配置可用 LLM）：可行动提示（非常驻，仅失败后出现；CTA 直达设置 → LLM）── */
.llm-fail-hint {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 6px 8px 0;
  padding: 6px 10px;
  font-size: 11px;
  line-height: 1.4;
  color: var(--text-secondary);
  border: 1px solid var(--warning-border);
  background: var(--warning-bg);
  border-radius: var(--border-radius);
}
.llm-fail-text {
  flex: 1;
  min-width: 0;
}
.llm-fail-cta {
  flex-shrink: 0;
  border: none;
  background: none;
  padding: 0;
  font-size: 11px;
  font-weight: 600;
  color: var(--accent);
  text-decoration: underline;
  cursor: pointer;
}
.llm-fail-cta:hover {
  opacity: 0.8;
}

:deep(.popover-list) {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
:deep(.popover-item) {
  padding: 6px 10px;
  font-size: 12px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-primary, #333);
  transition: background 0.12s;
}
/* 场景项：名称 + "设为默认"星标 */
:deep(.scenario-item) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
:deep(.scenario-item-name) {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
:deep(.scenario-item-star) {
  color: var(--text-muted, #999);
  cursor: pointer;
  font-size: 13px;
  line-height: 1;
  opacity: 0.5;
}
:deep(.scenario-item-star:hover) {
  opacity: 1;
  color: var(--warning, #e6a23c);
}
:deep(.scenario-item-star.on) {
  opacity: 1;
  color: var(--warning, #e6a23c);
}
/* LLM 项：名称 + 来源标记（系统内置 / 系统默认（启动参数）；用户项无标记） */
:deep(.llm-item) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}
:deep(.llm-item-name) {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
:deep(.popover-item:hover) {
  background: var(--bg-hover, #f0f0f0);
}
:deep(.popover-item.active) {
  background: var(--accent, #409eff);
  color: #fff;
}
.panel-inner {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.panel-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  min-height: 28px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.8px;
  color: var(--text-muted);
  text-transform: uppercase;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  flex-shrink: 0;
}
.header-spacer {
  flex: 1;
  min-width: 0;
}
/* 精简标题条（纯对话窗口 chatOnly）：只含场景 Tag，左对齐（无 header-spacer 撑开） */
.panel-header-mini {
  justify-content: flex-start;
}
.chat-session-tag {
  font-weight: 500;
  text-transform: none;
  letter-spacing: 0;
}

/* ── 继续/重试操作区（输入区上方）：**左对齐**（A7）── */
.turn-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 8px;
  padding: 4px 8px;
}
.empty-reply-hint {
  font-size: 12px;
  color: var(--text-muted, #999);
  padding: 2px 8px;
}
/* 继续 = **文本按钮**（A7）：无底色/无描边，仅强调色文字（.b-btn.is-text 同口径） */
.continue-btn {
  font-size: 12px;
}
.retry-tool-btn:hover {
  opacity: 0.9;
}
.retry-tool-btn {
  padding: 2px 20px;
  border-radius: 14px;
  font-size: 12px;
  background: var(--warning, #e6a23c);
  color: #fff;
  border: none;
  cursor: pointer;
}
</style>
