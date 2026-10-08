<template>
  <div class="input-box">
    <!-- 附件区：图片缩略图 + 文件 chip（上传中显示状态，就绪后可删除） -->
    <div v-if="attachments.length" class="attach-list">
      <div v-for="(a, i) in attachments" :key="a.uid || i" class="attach-chip" :class="['attach-' + a.kind, { pending: a.pending }]">
        <img v-if="a.kind === 'image'" :src="a.url" class="attach-thumb" alt="" />
        <Icon v-else name="document" :size="13" />
        <span class="attach-name" :title="a.name">{{ a.name }}</span>
        <span v-if="a.pending" class="attach-status">{{ $t('chat.uploading') }}</span>
        <span v-else class="attach-remove" :title="$t('common.delete')" @click.stop="removeAttachment(i)">✕</span>
      </div>
    </div>

    <!-- 输入区上方一行：选择提示词（单选，25 §7）+ 已选 prompt 的 tag（`/<prompt-name>`，可移除）。
         选择器按钮**常驻**（未选中也显示）；提示词内容发送时并入 user 消息（零契约变更）。
         下方工具栏（.input-actions-left）不再承载提示词选择。 -->
    <div class="prompt-row">
      <Popover placement="top-start" :width="260">
        <template #reference>
          <Button text size="mini" class="prompt-pick-btn" :title="$t('chat.prompt_pick')" @click="loadPrompts">
            <Icon name="magic-stick" :size="13" />
            <span>{{ selectedPrompt ? promptTagOf(selectedPrompt.name) : $t('chat.prompt_pick') }}</span>
          </Button>
        </template>
        <div class="prompt-popover">
          <div v-if="promptsLoading" class="prompt-empty">{{ $t('common.loading') }}</div>
          <div v-else-if="promptOptions.length === 0" class="prompt-empty">{{ $t('chat.prompt_empty') }}</div>
          <template v-else>
            <!-- 「无」项：单选清除（等价 remove）；当前未选择时高亮为选中态 -->
            <div class="prompt-item prompt-item-none" :class="{ active: !selectedPrompt }" @click="onPickNone">
              <span class="prompt-item-name">{{ $t('chat.prompt_none') }}</span>
            </div>
            <div
              v-for="p in promptOptions"
              :key="p.path"
              class="prompt-item"
              :class="{ active: selectedPrompt && selectedPrompt.path === p.path }"
              @click="onPickPrompt(p)"
            >
              <span class="prompt-item-name">{{ promptTagOf(p.name) }}</span>
              <span v-if="p.description" class="prompt-item-desc" :title="p.description">{{ p.description }}</span>
              <span class="prompt-item-level">{{ $t('scenario.level.' + (p.level || 'user')) }}</span>
            </div>
          </template>
        </div>
      </Popover>
      <div v-if="selectedPrompt" class="prompt-tags">
        <Tag size="small" type="warning" :title="selectedPrompt.path">
          <span class="prompt-tag-name">{{ promptTagOf(selectedPrompt.name) }}</span>
          <span class="prompt-tag-remove" :title="$t('chat.prompt_remove')" @click.stop="removePrompt">✕</span>
        </Tag>
      </div>
    </div>

    <!-- 富文本输入区（contenteditable 自研轻量，不引入重型编辑器库）：
         普通文本自动增高；Enter 发送，Shift/Ctrl/Alt/Meta+Enter 换行；
         粘贴/拖入图片 → 附件；拖入文件 → chip；data-drop-target 供文件树拖入路径插入 -->
    <div
      ref="editRef"
      class="b-textarea--autosize richtext-input"
      contenteditable="true"
      data-drop-target="chat-input"
      :data-placeholder="$t('chat.input_placeholder')"
      @input="onEdit"
      @keydown="handleKeydown"
      @paste="onPaste"
      @drop.prevent="onDrop"
      @dragover.prevent
    ></div>

    <div class="input-actions">
      <div class="input-actions-left">
        <slot name="controls" />
        <!-- 待发队列指示器：LLM 忙碌时入队的消息计数；点击展开 popover 列表（单项可撤回/删除） -->
        <Popover v-if="queueCount > 0" placement="top-end" :width="320">
          <template #reference>
            <div class="queue-indicator">
              <Icon name="chat-dot-square" :size="14" />
              <span class="queue-badge">{{ queueCount }}</span>
            </div>
          </template>
          <div class="queue-popover">
            <div v-if="queueItems.length === 0" class="queue-empty">{{ $t('chat.queue_empty') }}</div>
            <div v-for="it in queueItems" :key="it.id" class="queue-item">
              <span class="queue-item-label">{{ itemLabel() }}</span>
              <span class="queue-item-content" :title="rawText(it)">{{ itemPreview(it) }}</span>
              <span class="queue-item-actions">
                <Button
                  text
                  size="mini"
                  :title="$t('chat.queue_recall')"
                  v-mq:[EventNames.chatQueueAction].click="{ id: it.id, action: 'recall' }"
                >
                  ↩
                </Button>
                <Button
                  text
                  size="mini"
                  :title="$t('chat.queue_delete')"
                  v-mq:[EventNames.chatQueueAction].click="{ id: it.id, action: 'delete' }"
                >
                  ✕
                </Button>
              </span>
            </div>
          </div>
        </Popover>
      </div>
      <div class="input-actions-right">
        <template v-if="loading">
          <Button
            type="danger"
            @click="$emit('cancel')"
          >
            {{ $t('common.cancel') }}
          </Button>
          <Button
            type="primary"
            :disabled="!canSend"
            @click="handleQueue"
          >
            {{ $t('chat.queue') }}
          </Button>
        </template>
        <template v-else>
          <Button
            type="primary"
            :disabled="!canSend"
            v-mq:[EventNames.chatSend].click
          >
            {{ $t('common.send') }}
          </Button>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Popover, Tag, message as uiMessage } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import mq from '../../utils/mq'
import { MsgTopics } from '../../events/msgkeys'
import { getFileUrl } from '../../api/file'
import { EventNames } from '../../events/event-names'
import { formatItemText } from '../../composables/useSendQueue'
import { useChatPrompts } from '../../composables/useChatPrompts'
import { composeUserText, promptTagOf } from '../../utils/chatPrompt'

const { t } = useI18n()

const props = defineProps({
  loading: { type: Boolean, default: false },
  // 待发队列（当前 LLM）指示
  queueCount: { type: Number, default: 0 },
  queueItems: { type: Array, default: () => [] },
})

const emit = defineEmits(['send', 'cancel', 'queue'])

// 富文本状态：text = 纯文本视图（innerText 同步）；attachments = 附件（图片/文件，含上传中）
const text = ref('')
const attachments = ref([])
const editRef = ref(null)

// ── prompt 选择（知识库 `*.prompt.md`，25 §7）──
// 单选（文档未明示多选 → 置单选，属可扩展点）；选中后内容**并入 user 消息文本**（零契约变更），
// HTML 上以 `/<prompt-name>` 的 tag 显示（可移除）；发送 / 入队后随输入区一并清空（一次性注入）。
const {
  available: promptOptions,
  selected: selectedPrompt,
  loading: promptsLoading,
  load: loadPrompts,
  pick: pickPrompt,
  remove: removePrompt,
} = useChatPrompts()

async function onPickPrompt(p) {
  const ok = await pickPrompt(p)
  if (!ok) uiMessage.error(t('chat.prompt_load_failed', { name: promptTagOf(p && p.name) }))
}

// 「无（不使用提示词）」项：单选清除 —— 等价 remove()（未选 → 不注入任何提示词）。
function onPickNone() {
  removePrompt()
}

// 上传中（gui.upload 未回执）：禁止发送——避免把尚未落盘的附件引用发出去（图片发给 LLM 依赖落盘路径）。
// 已选 prompt（内容非空、即使未输入文本）也可发送：prompt 内容本身就是一条 user 消息
// （空内容 prompt 不构成可发送内容 → 与 MessageList 的「空文本不入轮」一致）。
const uploading = computed(() => attachments.value.some(a => a.pending))
const hasPromptContent = computed(() => !!(selectedPrompt.value && String(selectedPrompt.value.content || '').trim()))
const canSend = computed(() => !uploading.value && !!(text.value.trim() || attachments.value.length || hasPromptContent.value))

// popover 列表项预览：每行一条（类型前缀 + 内容截断）；队列只承载用户消息（附录 A）
function itemLabel() {
  return '✉'
}

function itemPreview(it) {
  const content = formatItemText(it)
  return content.length > 40 ? content.slice(0, 40) + '…' : content
}

// 原始文本（title 提示完整内容）
function rawText(it) {
  return it.payload?.text || it.type
}

// ── 编辑区 ──

function onEdit() {
  text.value = editRef.value ? editRef.value.innerText : ''
  autoResize()
}

function autoResize() {
  const el = editRef.value
  if (!el) return
  el.style.height = 'auto'
  const minH = 40
  const maxH = 240
  const h = el.scrollHeight
  el.style.height = Math.min(Math.max(h, minH), maxH) + 'px'
  el.style.overflowY = h > maxH ? 'auto' : 'hidden'
}

// 序列化发送内容为 markdown 纯文本（20-gui）：
//   图片 → ![名](路径)；文件 → [名](路径)；随后接文本（附件在前）
//   + 已选 prompt（25 §7）：内容并入 user 消息文本头部（**零契约变更**：消息面字段不变）
function serialize() {
  const parts = []
  for (const a of attachments.value) {
    if (a.kind === 'image') parts.push(`![${a.name}](${a.path})`)
    else parts.push(`[${a.name}](${a.path})`)
  }
  const t = (text.value || '').trim()
  if (t) parts.push(t)
  return composeUserText(parts.join('\n'), selectedPrompt.value)
}

// 忙碌（loading）时不拦截：消息先入发送队列（MessageList onMessageSend → useSendQueue），
// turn 结束后由 drainQueue 自动发送（§3.4.1 send 按钮始终有效）。
function handleSend() {
  if (!canSend.value) return
  emit('send', serialize())
  clearEdit()
}

// 忙碌（loading）时显示【入队】：序列化内容 → 清空 → emit queue。
function handleQueue() {
  if (!canSend.value) return
  emit('queue', serialize())
  clearEdit()
}

function clearEdit() {
  text.value = ''
  attachments.value = []
  removePrompt() // 已选 prompt 为**一次性注入**（内容已并入本次 user 消息）→ 发送/入队后清空
  if (editRef.value) editRef.value.innerHTML = ''
  nextTick(() => autoResize())
}

// Enter 发送；Shift/Ctrl/Alt/Meta+Enter 换行（contenteditable 中 Ctrl/Alt/Meta+Enter 需手动插行）
function handleKeydown(e) {
  if (e.key !== 'Enter') return
  if (e.shiftKey || e.ctrlKey || e.altKey || e.metaKey) {
    if (!e.shiftKey) {
      e.preventDefault()
      document.execCommand('insertLineBreak')
    }
    return
  }
  e.preventDefault()
  handleSend()
}

// 粘贴：图片 → 上传附件；纯文本 → 光标处插入（防带样式 HTML）
function onPaste(e) {
  const files = e.clipboardData && e.clipboardData.files
  if (files && files.length) {
    e.preventDefault()
    for (const f of Array.from(files)) {
      if (f.type.startsWith('image/')) upload(f)
    }
    return
  }
  e.preventDefault()
  const t = e.clipboardData && e.clipboardData.getData('text/plain')
  if (t) insertAtCursor(t)
}

// 拖入：有文件 → 上传附件（本组件处理，stopPropagation 阻止冒泡到 document）；
// 无文件（文件树拖拽路径）→ 不处理，冒泡给 document onDocDrop 插入路径
function onDrop(e) {
  const files = e.dataTransfer && e.dataTransfer.files
  if (files && files.length) {
    e.stopPropagation()
    for (const f of Array.from(files)) upload(f)
  }
}

// 附件唯一标识自增序号（上传回执按 uid 定位，避免删除其它附件导致下标漂移）
let attachSeq = 0

// ── 附件上传（gui.upload → 数据根 tmp/uploads/，result 含 /show/ 预览 url/path/file_id）──
// 先插入「上传中」chip（图片用本地 dataUrl 作缩略图即时可见），回执后补 path/url/fileId 转为可发送；
// 失败 → 移除该 chip 并明确报错（不静默丢）。
function upload(file) {
  const kind = file.type && file.type.startsWith('image/') ? 'image' : 'file'
  const uid = 'att-' + (++attachSeq)
  attachments.value.push({ uid, kind, name: file.name, path: '', url: '', fileId: '', pending: true })
  const find = () => attachments.value.find(a => a.uid === uid)
  readAsDataURL(file).then(dataUrl => {
    const a = find()
    if (a && kind === 'image') a.url = dataUrl // 本地预览（落盘前的缩略图）
    nextTick(() => autoResize())
    return mq.emit(MsgTopics.guiUpload, { name: file.name, data: dataUrl, kind })
  }).then((env) => {
    const res = env && env.backend && env.backend.result
    const a = find()
    if (!a) return
    if (!res || !res.path) throw new Error('upload: empty result')
    a.path = res.path
    a.url = res.url || a.url
    a.fileId = res.file_id || ''
    a.pending = false
    nextTick(() => autoResize())
  }).catch(e => {
    const i = attachments.value.findIndex(a => a.uid === uid)
    if (i >= 0) attachments.value.splice(i, 1)
    uiMessage.error(t('chat.upload_failed', { name: file.name }))
    console.warn('[InputBox] upload failed:', e)
  })
}

function readAsDataURL(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(r.result)
    r.onerror = reject
    r.readAsDataURL(file)
  })
}

function removeAttachment(i) {
  attachments.value.splice(i, 1)
}

// ── 光标位置插入（预览区选中文本 → 对话 / 文件树拖入路径）──
function insertAtCursor(insertText) {
  const el = editRef.value
  if (!el) {
    text.value += insertText
    return
  }
  el.focus()
  const sel = window.getSelection()
  if (sel && sel.rangeCount) {
    const range = sel.getRangeAt(0)
    range.deleteContents()
    const tn = document.createTextNode(insertText)
    range.insertNode(tn)
    range.setStartAfter(tn)
    range.collapse(true)
    sel.removeAllRanges()
    sel.addRange(range)
  } else {
    el.appendChild(document.createTextNode(insertText))
  }
  onEdit()
}

function onInsertText(payload) {
  // mq 回调直接收 payload（{text}），非事件对象
  if (payload && typeof payload === 'object' && payload.text) {
    insertAtCursor(payload.text)
  } else if (typeof payload === 'string' && payload) {
    insertAtCursor(payload)
  }
}

// canceled 队列简化 / 撤回：待发内容写回（含附件标记的 md 文本；缩略图回显为文本标记）
function onQueueRestore({ text: restored }) {
  if (!restored) return
  setEditText(text.value ? restored + '\n' + text.value : restored)
}

// 外部注入附件（截图按钮 / 后续拖入）：与粘贴/拖入图片同一链路
function onInsertAttachment(payload) {
  if (!payload || !payload.path) return
  attachments.value.push({
    kind: payload.kind || 'file', name: payload.name || 'attachment',
    path: payload.path, url: payload.url || '', fileId: payload.fileId || '',
  })
  nextTick(() => autoResize())
}

function setEditText(t) {
  // 解析 md 附件标记（写回内容）→ 恢复为缩略图/chip + 纯文本
  const { attachments: atts, text: clean } = parseAttachmentMarkers(t)
  text.value = clean
  attachments.value = atts.map(a => ({
    kind: a.kind, name: a.name, path: a.path,
    url: a.path && !a.path.startsWith('/') ? getFileUrl(a.path) : (a.url || ''),
  }))
  if (editRef.value) {
    editRef.value.innerHTML = ''
    editRef.value.appendChild(document.createTextNode(clean))
  }
  placeCaretAtEnd()
  nextTick(() => autoResize())
}

// 从 md 文本提取附件标记：![名](路径) → image；[名](路径) → file（仅本地路径，排除 http 链接）
function parseAttachmentMarkers(md) {
  const atts = []
  let clean = md
  clean = clean.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (m, name, path) => {
    if (isLocalPath(path)) atts.push({ kind: 'image', name: name || 'image', path })
    return ''
  })
  clean = clean.replace(/\[([^\]]*)\]\(([^)\s]+)\)/g, (m, name, path) => {
    if (isLocalPath(path)) atts.push({ kind: 'file', name: name || 'file', path })
    return ''
  })
  return { attachments: atts, text: clean.trim() }
}

function isLocalPath(p) {
  return !/^https?:/i.test(p) && /^[A-Za-z]:[\\/]/.test(p)
}

function placeCaretAtEnd() {
  const el = editRef.value
  if (!el) return
  el.focus()
  const range = document.createRange()
  range.selectNodeContents(el)
  range.collapse(false)
  const sel = window.getSelection()
  sel.removeAllRanges()
  sel.addRange(range)
}

let unsubInsertText = null
let unsubChatSend = null
let unsubQueueRestore = null
let unsubInsertAttachment = null

onMounted(() => {
  unsubInsertText = mq.on(EventNames.chatInsertText, onInsertText)
  unsubChatSend = mq.on(EventNames.chatSend, handleSend)
  unsubQueueRestore = mq.on(EventNames.chatQueueRestore, onQueueRestore)
  unsubInsertAttachment = mq.on(EventNames.chatInsertAttachment, onInsertAttachment)
  nextTick(() => autoResize())
})

onUnmounted(() => {
  if (unsubInsertText) unsubInsertText()
  if (unsubChatSend) unsubChatSend()
  if (unsubQueueRestore) unsubQueueRestore()
  if (unsubInsertAttachment) unsubInsertAttachment()
})
</script>

<style scoped>
.input-box {
  padding: 8px;
  border-top: 1px solid var(--border);

  flex-shrink: 0;
}

.b-textarea--autosize {
  width: 100%;
  padding: 5px 11px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  font-size: var(--font-size-sm);
  background: var(--bg-primary, #fff);
  color: var(--text-primary);
  outline: none;
  transition: border-color 0.2s;
  box-sizing: border-box;
  line-height: 20px;
  font-family: inherit;
  min-height: 40px;
  max-height: 240px;
}
.b-textarea--autosize:focus {
  border-color: var(--accent);
}
/* 预览区选中文本拖拽悬停时的高亮提示 */
.b-textarea--autosize.drag-over {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px rgba(64, 158, 255, 0.18);
}

/* 富文本编辑区：纯文本换行保留、空态 placeholder */
.richtext-input {
  white-space: pre-wrap;
  word-break: break-word;
  overflow-y: auto;
  user-select: text;
}
.richtext-input:empty::before {
  content: attr(data-placeholder);
  color: var(--text-muted, #999);
  pointer-events: none;
}

/* 附件区 */
.attach-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 6px;
}
.attach-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 220px;
  padding: 3px 8px 3px 4px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-secondary, #f5f5f5);
  font-size: 12px;
  color: var(--text-primary);
}
.attach-thumb {
  width: 28px;
  height: 28px;
  object-fit: cover;
  border-radius: 4px;
  flex-shrink: 0;
}
.attach-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 上传中：chip 降透明度 + 状态文案（发送按钮同时禁用，见 canSend） */
.attach-chip.pending {
  opacity: 0.6;
}
.attach-status {
  flex-shrink: 0;
  color: var(--text-muted, #999);
  white-space: nowrap;
}
.attach-remove {
  flex-shrink: 0;
  cursor: pointer;
  color: var(--text-muted, #999);
  padding: 0 2px;
}
.attach-remove:hover {
  color: var(--danger, #f56c6c);
}

/* ── 输入区上方一行：提示词选择器 + 已选 tag（`/<prompt-name>`；25 §7）── */
.prompt-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 6px;
}
.prompt-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.prompt-tag-name {
  font-weight: 600;
}
.prompt-tag-remove {
  margin-left: 6px;
  cursor: pointer;
  opacity: 0.7;
}
.prompt-tag-remove:hover {
  opacity: 1;
}

/* ── prompt 选择器（输入区上方；popover 内容）── */
.prompt-pick-btn {
  font-size: 12px;
}
.prompt-popover {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 260px;
  overflow-y: auto;
}
.prompt-empty {
  font-size: 12px;
  color: var(--text-muted, #999);
  padding: 4px 0;
}
.prompt-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 6px;
  border-radius: 4px;
  cursor: pointer;
}
.prompt-item:hover {
  background: var(--bg-hover, #f0f0f0);
}
.prompt-item.active {
  background: var(--accent, #409eff);
  color: #fff;
}
/* 「无」项：置于列表顶部，用分隔线与可选提示词区分 */
.prompt-item-none {
  border-bottom: 1px solid var(--border);
  border-radius: 4px 4px 0 0;
  margin-bottom: 4px;
}
.prompt-item-name {
  flex-shrink: 0;
  font-size: 12px;
  font-weight: 600;
}
.prompt-item-desc {
  flex: 1;
  min-width: 0;
  font-size: 11px;
  color: var(--text-muted, #999);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.prompt-item.active .prompt-item-desc {
  color: #fff;
  opacity: 0.85;
}
.prompt-item-level {
  flex-shrink: 0;
  font-size: 10px;
  color: var(--text-muted, #999);
}
.prompt-item.active .prompt-item-level {
  color: #fff;
  opacity: 0.85;
}

.input-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  /* P0-2：chat 列压到最小 280px 时左侧控件（场景/LLM Tag + 图标 + prompt + 队列）曾溢出被裁；
     允许换行 + 组间 gap → 右侧发送键始终可见可用。 */
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.input-actions-left {
  display: flex;
  align-items: center;
  gap: 6px;
  /* 左侧组自身可换行（控件逐个折行），min-width: 0 允许收缩，不撑破父容器 */
  flex-wrap: wrap;
  min-width: 0;
}

.input-actions-right {
  display: flex;
  align-items: center;
  gap: 6px;
  /* 换行到第二行时仍靠右（左组占据首行）；且不因 flex 收缩而挤压发送键 */
  margin-left: auto;
  flex-shrink: 0;
}

.queue-indicator {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  border: 1px solid var(--border);
  border-radius: 10px;
  font-size: 12px;
  color: var(--accent);
  cursor: pointer;
  user-select: none;
}

.queue-indicator .queue-badge {
  min-width: 16px;
  height: 16px;
  line-height: 16px;
  text-align: center;
  border-radius: 8px;
  /* 实心填充：文字用主题最底层色（light = 纯白，与历史 #fff 一致；dark/nord = 深色） */
  background: var(--accent);
  color: var(--bg-secondary);
  font-size: 11px;
  padding: 0 4px;
}

.queue-popover {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 240px;
  overflow-y: auto;
}

.queue-empty {
  font-size: 12px;
  color: var(--text-muted, #999);
  padding: 4px 0;
}

.queue-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 6px;
  border-radius: 4px;
}

.queue-item:hover {
  background: var(--bg-hover, #f0f0f0);
}

.queue-item-label {
  flex-shrink: 0;
  font-size: 12px;
}

.queue-item-content {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.queue-item-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 2px;
  font-size: 13px;
  color: var(--text-muted, #999);
}
</style>
