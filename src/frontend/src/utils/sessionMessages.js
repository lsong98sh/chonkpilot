import { ref } from 'vue'
import { message } from '../components/ui'
import { i18n } from '../plugins/i18n'
import { classifyError } from './errorMessage'

function computeContentSize(callArgs, result) {
  let sz = 0
  if (callArgs) sz += typeof callArgs === 'string' ? callArgs.length : JSON.stringify(callArgs).length
  if (result) sz += result.length
  return sz
}

function computeBrief(tool, simplified, resultSimplified, status, purpose) {
  let brief = purpose || simplified || (tool + '(...)')
  if (status === 'done') {
    const rb = resultSimplified || 'ok'
    brief += '  ' + rb
  } else if (status === 'failed') {
    const rb = resultSimplified || 'failed'
    brief += '  ' + rb
  }
  if (brief.length > 160) {
    brief = brief.slice(0, 157) + '...'
  }
  return brief
}

function dbMsgToView(dbMsg) {
  if (dbMsg.type === 'tool_pair') {
    const simplified = dbMsg.simplified || ''
    const resultSimplified = dbMsg.result_simplified || ''
    const status = dbMsg.status || 'done'
    const resultSuccess = dbMsg.result_success !== false
    // brief 落库即 purpose（LLM 必填的调用理由），作为标题/摘要的基底
    const purpose = dbMsg.brief || ''
    const brief = computeBrief(dbMsg.tool, simplified, resultSimplified, status, purpose)
    return {
      id: 'tp_' + (dbMsg.tool_call_id || Math.random().toString(36).slice(2, 10)),
      role: dbMsg.role || 'assistant',
      type: 'tool_pair',
      tool_call_id: dbMsg.tool_call_id,
      task_id: dbMsg.task_id, // 任务节点 id（tk-*，content 落库值）：tool-pair 事件回退匹配键
      turn_id: dbMsg.turn_id, // 所属轮次（tool-retry 载荷 {session, turn} 的 turn 来源）
      tool: dbMsg.tool,
      purpose: purpose,
      simplified: simplified,
      result_simplified: resultSimplified,
      status: status,
      brief: brief,
      content_size: computeContentSize(dbMsg.arguments, dbMsg.result),
      arguments: dbMsg.arguments || '',
      result: dbMsg.result || '',
      has_more: !!dbMsg.has_more,
      result_success: resultSuccess,
      createdAt: dbMsg.created_at,
      _meta: dbMsg._meta || null, // 该工具自身的 meta 子集（I-60；来自落库记录）→ 本地判断裁决项
      fromHistory: true, // 来自 DB 的历史消息：reasoning 默认折叠
    }
  }
  return {
    id: dbMsg.message_id,
    role: dbMsg.role,
    type: dbMsg.type || 'text',
    content: dbMsg.content || '',
    has_more: !!dbMsg.has_more,
    createdAt: dbMsg.created_at,
    fromHistory: true, // 来自 DB 的历史消息：reasoning 默认折叠
  }
}

/**
 * Create a new session message state instance with paginated loading support.
 * Each chat view (ChatPanel, SessionChat) should have its own instance.
 */
export function createSessionMessages() {
  const messages = ref([])
  const turns = ref([])          // 最近加载批次的 turn 列表（正序，最后一个=最新；含 finish_reason）
  const turnActive = ref(false)
  const hasMore = ref(true)        // whether there are older turns to load
  const loadingMore = ref(false)   // whether a load-more is in progress
  const loadingMessages = ref(false) // 初始加载进行中：防止 onScroll/auto-preload 在加载未完成时误触发 loadMore
  let currentSection = null
  let loadedTurnIds = new Set()    // set of turn IDs already loaded
  let currentSessionId = null       // track which session is loaded

  // ── 「加载中」占位（发送后立即显示 LLM 处理进度）──
  // 独立状态，**不在 messages 数组内**：loadMessages 整体替换 messages（历史回填）
  // 不会把它冲掉（T5 方案 B）。显示 "处理上下文 → 发送 → 思考中" 进度；
  // 首个真实内容（文本/推理/工具调用）到达或轮次终态（complete/error）时收起。
  const pending = ref(false)
  const pendingStatus = ref('processing')

  function showPending(status = 'processing') {
    pending.value = true
    pendingStatus.value = status
  }

  function setPendingStatus(status) {
    pendingStatus.value = status
  }

  function hidePending() {
    pending.value = false
  }

  function handleToken(data) {
    turnActive.value = true

    // 任何真实内容（文本/推理/工具调用/工具结果）到达即收起占位
    hidePending()

    if (data.type === 'tool_call') {
      // 去重：DB 历史已含同 tool_call_id 的 pair（子会话切换/中途重载后仍有流事件时）
      if (messages.value.some(m => m.type === 'tool_pair' && m.tool_call_id === data.tool_call_id)) {
        currentSection = null
        return
      }
      const callSimplified = data.simplified || (data.tool + '(...)')
      const purpose = data.purpose || ''
      const msg = {
        role: 'assistant',
        type: 'tool_pair',
        tool_call_id: data.tool_call_id,
        tool: data.tool,
        purpose: purpose,
        simplified: callSimplified,
        result_simplified: null,
        status: 'pending',
        arguments: data.arguments,
        result: null,
        result_success: null,
        _meta: data._meta || null, // 该工具自身的 meta 子集（I-60；随 llm-receive tool-call 推送）
        id: 'tp_' + data.tool_call_id,
        createdAt: new Date().toISOString(),
      }
      msg.brief = computeBrief(msg.tool, msg.simplified, null, 'pending', purpose)
      msg.content_size = computeContentSize(data.arguments, null)
      messages.value.push(msg)
      currentSection = null
      return
    }

    if (data.type === 'tool_result') {
      const pair = messages.value.find(m => m.type === 'tool_pair' && m.tool_call_id === data.tool_call_id)
      if (pair) {
        pair.status = data.success === false ? 'failed' : 'done'
        pair.result_simplified = data.simplified || ''
        pair.result = data.content || data.result || ''
        pair.result_success = data.success !== false
        pair.brief = computeBrief(pair.tool, pair.simplified, pair.result_simplified, pair.status, pair.purpose)
        pair.content_size = computeContentSize(pair.arguments, pair.result)
      }
      currentSection = null
      return
    }

    const sectionKey = data.type === 'reasoning' ? 'reasoning' : 'text'

    // 追加条件：最后一条正是本 turn 的 assistant 文本/推理段。
    // 仅靠 currentSection 判断会被"turn 流式中途插入的用户消息"污染——
    // 此时 messages 末尾是 user 气泡，继续追加会把流式内容写进用户消息。
    // （busy 入队场景：LLM 忙碌时用户消息在落库回执后插入，流式继续）
    const last = messages.value[messages.value.length - 1]
    if (last && last.role === 'assistant' && last.type === sectionKey) {
      last.content += (data.content || '')
    } else {
      messages.value.push({
        id: Date.now().toString() + (sectionKey === 'reasoning' ? 'r' : 't'),
        role: 'assistant',
        type: sectionKey,
        content: data.content || '',
        createdAt: new Date().toISOString(),
      })
      currentSection = sectionKey
    }
  }

  /**
   * llm-receive 拆分（20-gui）：server 协议 llm-receive{type} 一个主题
   * 承载全部流内容，这里把 server 载荷映射为 handleToken 的旧输入格式：
   *   type=text      → {type:'text',      content: text}
   *   type=reason    → {type:'reasoning', content: text}
   *   type=tool-call → {type:'tool_call', tool_call_id, tool, arguments, simplified, purpose}
   */
  function handleReceive(data) {
    if (!data || !data.type) return
    const t = data.type
    if (t === 'text' || t === 'reason') {
      // 文本/推理另有兼容事件 llm-token 路径（compat.go）负责渲染，这里保持原样读取，
      // 不展开 payload，避免与 llm-token 重复追加。
      handleToken({ type: t === 'reason' ? 'reasoning' : 'text', content: data.text || '' })
    } else if (t === 'tool-call') {
      // server 定稿载荷为 {session, turn, type, payload}（61-消息一览 §4.3）；payload 缺失时
      // 兼容扁平形态。仅本分支展开（工具卡片无其它创建路径）。
      const p = (data.payload && typeof data.payload === 'object') ? data.payload : data
      handleToken({
        type: 'tool_call',
        tool_call_id: p.tool_call_id,
        tool: p.tool,
        arguments: p.arguments || '',
        simplified: p.simplified,
        purpose: p.purpose,
        _meta: p._meta, // 该工具自身的 meta 子集（I-60；server 随 tool-call 推送）
      })
    }
  }

  function handleDone() {
    // 轮次终态（正常/取消/错误都收敛于此）：收起「加载中」占位
    hidePending()
    currentSection = null
    turnActive.value = false
  }

  /**
   * 轮次错误气泡（批 2 – 错误呈现面）：原始串（`err.Error()`，如
   * `[timeout] … context deadline exceeded` / `-32602`）经 errorMessage 分类映射
   * → 展示层用人话文案（`errorKey`/`errorParams`），**原始串原样保留在 content**
   * （折叠可查、可复制，诊断不丢信息；同时维持 lastTurnIncomplete 启发式对
   * 「assistant 且带 content」的既有判定，见 MessageList.lastTurnIncomplete）。
   */
  function handleError(data) {
    hidePending()
    const cls = classifyError(data)
    messages.value.push({
      id: Date.now().toString() + 'e',
      role: 'assistant',
      type: 'error', // 专用类型：展示层渲染「人话文案 + 原始错误」
      errorKey: cls.key,
      errorParams: cls.params,
      content: cls.detail,
      createdAt: new Date().toISOString(),
    })
    currentSection = null
    turnActive.value = false
  }

  /**
   * Load the latest batch of messages for a session.
   * Replaces the entire messages array.
   *
   * 注：「加载中」占位是**独立状态**（pending/pendingStatus，不在 messages 内）→
   * 本函数的整体替换不会影响它（T5 方案 B）。
   */
  async function loadMessages(sessionId) {
    if (!sessionId) return
    sessionId = sessionId.trim()
    if (!sessionId) return

    messages.value = []
    turnActive.value = false
    currentSection = null
    hasMore.value = true
    loadingMore.value = false
    loadingMessages.value = true
    loadedTurnIds = new Set()
    currentSessionId = sessionId

    try {
      const { getTurnsPaginated } = await import('../api/session')
      // 目标消息数/字节数放大：普通对话 turn 单 turn 可达数十条消息，
      // 50 条/200KB 的旧参数会让每批只返回 2~10 个 turn，滚顶加载体验极差。
      const res = await getTurnsPaginated(sessionId, '', 300, 4 * 1024 * 1024)
      if (res && res.messages) {
        messages.value = res.messages.map(dbMsgToView)
      }
      hasMore.value = !!(res && res.has_more)
      turns.value = (res && res.turns) || []
      if (res && res.turns) {
        for (const t of res.turns) {
          loadedTurnIds.add(t.turn_id)
        }
      }
      // Auto-preload: if there are more turns, fetch one extra batch in background
      // so the user doesn't hit the top immediately.
      if (hasMore.value && messages.value.length > 0) {
        setTimeout(() => {
          loadMoreMessages().catch(() => {})
        }, 100)
      }
      return res
    } catch (e) {
      console.warn('[sessionMessages] Failed to load messages:', e)
      message.error(i18n.global.t('chat.load_messages_failed') + ': ' + (e.message || e))
    } finally {
      loadingMessages.value = false
    }
  }

  /**
   * Load older messages (before the current oldest loaded turn).
   * Prepends to the messages array.
   */
  async function loadMoreMessages() {
    // 初始加载进行中禁止 loadMore：loadMessages 尚未完成时 hasMore 被临时置 true、
    // loadedTurnIds 为空（beforeTurnId=''），此时并发 loadMore 会重复抓取最新批次并前置，
    // 造成消息重复显示（子会话首次打开/会话切换时可见）。
    if (loadingMessages.value || !hasMore.value || loadingMore.value || !currentSessionId) return
    loadingMore.value = true

    try {
      // 游标 = 当前已加载最老的 turn（turns.value 正序，loadMore 前置更早批次后
      // turns.value[0] 即为最新一批的最老 turn）。此前误用 loadedTurnIds 的
      // Set 首元素——Set 插入序固定为首次加载批次，游标不推进导致 loadMore
      // 反复拉取同一批（消息重复、hasMore 永不耗尽）。
      const beforeTurnId = turns.value.length > 0 ? (turns.value[0].turn_id || '') : ''

      const { getTurnsPaginated } = await import('../api/session')
      const res = await getTurnsPaginated(currentSessionId, beforeTurnId, 300, 4 * 1024 * 1024)

      if (res && res.messages && res.messages.length > 0) {
        const oldMsgs = res.messages.map(dbMsgToView)
        messages.value = [...oldMsgs, ...messages.value]
      }
      hasMore.value = !!(res && res.has_more)
      turns.value = [...((res && res.turns) || []), ...turns.value]
      if (res && res.turns) {
        for (const t of res.turns) {
          loadedTurnIds.add(t.turn_id)
        }
      }
    } catch (e) {
      // 上滑加载更多失败原先只 console（用户看不到任何反馈，界面静默停在顶部）→
      // 与首屏失败同口径给出可见提示（轻提示，可再上滑重试；消息不丢，下次触发即重试）。
      console.warn('[sessionMessages] Failed to load more messages:', e)
      message.warning(i18n.global.t('chat.load_more_failed'))
    } finally {
      loadingMore.value = false
    }
  }

  function teardown() {
    messages.value = []
    turnActive.value = false
    currentSection = null
    hasMore.value = true
    loadingMore.value = false
    loadingMessages.value = false
    pending.value = false
    pendingStatus.value = 'processing'
    loadedTurnIds = new Set()
    currentSessionId = null
  }

  return {
    messages,
    turns,
    turnActive,
    hasMore,
    loadingMore,
    loadingMessages,
    pending,
    pendingStatus,
    handleToken,
    handleReceive,
    handleDone,
    handleError,
    loadMessages,
    loadMoreMessages,
    teardown,
    showPending,
    setPendingStatus,
    hidePending,
  }
}
