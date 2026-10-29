import { ref, computed, h, defineAsyncComponent } from 'vue'
import { dialog } from '../components/dialog'
import { message } from '../components/ui'
import { i18n } from '../plugins/i18n'
import mq from './mq'
import { EventNames } from '../events/event-names'
import { parseViewRoute, VIEW_MAIN } from './viewRoute.js'
import { windowForSession } from './chatWindows.js'
import { getSession, setActiveSessionID } from '../api/session'

// 懒加载：仅在出现 AskUser 问题弹窗时加载
const AskUserContent = defineAsyncComponent(() => import('../views/chat/AskUserContent.vue'))

// 选项目标形状 {value,label}（工具契约 options.items）；字符串容错（value=label=字符串本身）。
function optionValue(o) {
  if (o && typeof o === 'object') return String(o.value ?? o.label ?? '')
  return o == null ? '' : String(o)
}

// 域化（20-gui）：提问由 server `ask-user` 事件承载（AskUserDialog 订阅后
// enqueue 到本管理器）；回答经 ask-user-reply 事件直达后端（按 ask_id 权威路由）——
// 已删除 RespondAskUser RPC、前端 deadline 判断与 sendQueue 兜底（过期判断收归后端）。
class AskUserManager {
  queue = ref([])
  // current = the blocking "active" dialog (still within the pipe window).
  // At most one active dialog is shown at a time; it blocks the queue until
  // the user answers OR its pipe window closes (goes busy).
  current = ref(null)
  currentHandle = ref(null)
  // busy = dialogs whose pipe window has closed (deadline passed). They stay
  // open so the user can still answer later (answer becomes a new user message
  // via backend route), but they no longer block opening the next ask.
  busy = new Map()
  // deferred = 「跳过」的提问：不立即重排（否则单问题场景会立刻重开同一弹窗，
  // 用户观感 = 跳过无效）。暂存于此，等下一次新提问到达（enqueue）时再排回队列尾部
  // 「稍后重问」；无后续提问则保持关闭（等同忽略）。
  deferred = []
  // 批次计数（标题"第几个/共几个"）：新批次开始时重置
  batchSeq = 0
  batchTotal = 0
  _flushTimer = null

  active = computed(() => this.current.value !== null || this.busy.size > 0)
  pending = computed(() => this.queue.value.length)

  /**
   * 本窗口是否主窗口（AskUser S1-1）：判据复用既有「视图分派」叶子模块
   * （`utils/viewRoute.js` → URL 唯一判据；`#chat` + `session-id` = 对话窗口）。
   * 取不到 location（非浏览器环境）→ 按非主窗口处理（不渲染「聊一聊」）。
   */
  get canChat() {
    if (typeof window === 'undefined' || !window.location) return false
    return parseViewRoute(window.location.search, window.location.hash).view === VIEW_MAIN
  }

  enqueue(data) {
    // 新批次开始（无 current / 无 busy / 队列空 / 无 pending timer）：
    // 重置批次计数并启动 300ms 合并窗口，同一批连续到达的 ask-user 事件
    // （多问题语义）全部入队后才弹出第一条，使标题序号显示"第几个/共几个"。
    const batchPending = this._flushTimer !== null
    if (!this.current.value && this.busy.size === 0 && !batchPending && this.queue.value.length === 0) {
      this.batchSeq = 0
      this.batchTotal = 0
      // 上一批「跳过」的提问：新批次到达时排回队列尾部「稍后重问」，并重分配序号。
      // （不在 onSkip 立即重排 → 避免单问题场景跳过时立刻重开同一弹窗。）
      while (this.deferred.length > 0) {
        const d = this.deferred.shift()
        d.seq = ++this.batchSeq
        this.batchTotal += 1
        this.queue.value.push(d)
      }
      this._flushTimer = setTimeout(() => {
        this._flushTimer = null
        // 批次结束：回填总数（item.total 创建时是当时的 batchTotal，需刷新为最终值），
        // 使标题"第几个/共几个"在整个批次内正确递增。
        this.batchTotal = Math.max(this.batchTotal, this.queue.value.length)
        for (const it of this.queue.value) it.total = this.batchTotal
        this.processNext()
      }, 300)
    }
    this.batchTotal += 1
    const item = {
      id: Date.now().toString(36) + Math.random().toString(36).slice(2, 8),
      // 附录 A：路由键 = ask_id（= tool-id / task_id，已归一），跨 turn 语义正确。
      askId: data.askId || data.tool_id || '',
      question: data.question,
      options: data.options || [],
      custom: data.options && data.options.length === 0 ? true : !!data.custom,
      // 多选 + 推荐（附录 A 扩展）：multi=true 允许勾选多个选项；recommended 标注推荐项
      multi: !!data.multi,
      recommended: Array.isArray(data.recommended) ? data.recommended : [],
      subSessionId: data.subSessionId || data.sub_session_id || '',
      sessionId: data.sessionId || data.session_id || data.subSessionId || data.sub_session_id || '',
      // 会话标识（61 §4.3 session-ask.dialog_id，§0.6 必带；AskUser S1-3/S1-4）：
      // 提问下发时的 `dialog_id`，作答时**原样回带**（`ask-user-reply.dialog_id`），
      // 「聊一聊」也用它定位 (dialog_id, session)。
      dialogId: data.dialogId || data.dialog_id || '',
      // deadline 仅用于 UI 提示（超时后弹窗提示"回复将作为新消息发送"），不做路由判断。
      deadline: data.deadline && data.deadline > 0 ? data.deadline : 0,
      scenarioId: data.scenarioId || data.scenario_id || '',
      scenarioName: data.scenarioName || data.scenario_name || '',
      // 标题序号（FP L360）：入队时分配，同批次内递增
      seq: this.batchSeq + 1,
      total: this.batchTotal,
    }
    this.batchSeq += 1
    this.queue.value.push(item)
    // 非合并窗口内且无 current → 立即弹出（busy 释放后的后续、skip 重放等）
    if (!this.current.value && this._flushTimer === null && this.queue.value.length > 0) {
      this.processNext()
    }
  }

  processNext() {
    if (this.queue.value.length === 0) {
      this.current.value = null
      return
    }
    const item = this.queue.value.shift()
    this.current.value = item
    this.showDialog(item)
  }

  showDialog(item) {
    // 注意：dialog.show 渲染是同步的——deadline 立即到期时 AskUserContent onMounted
    // 会同步 emit('busy') → onBusy 闭包访问 handle。必须先用 let 声明（避免 TDZ），
    // 且同步触发时 handle 尚为 null（closeAndAdvance 已有可选链守卫）。
    let handle = null
    handle = dialog.show(h(AskUserContent, {
      uid: item.id,
      question: item.question,
      options: item.options,
      custom: item.custom,
      multi: item.multi,
      recommended: item.recommended,
      subSessionId: item.subSessionId,
      sessionId: item.sessionId,
      deadline: item.deadline,
      // 「聊一聊」出口（AskUser S1-1/§4.3）：**仅主窗口**提供 —— 该动作要在本窗口切到
      // 发起会话、把问题引用放进本窗口输入框；对话窗口（URL `#chat`，会话由 URL 固定绑定，
      // 24 §6.2）不提供。判定复用既有「视图分派」叶子模块（URL 唯一判据，不新造窗口判定）。
      canChat: this.canChat,
      onAnswer: (answer) => {
        this.submitAnswer(item, answer)
        this.closeAndAdvance(item)
      },
      onChat: () => {
        this.chatAbout(item)
      },
      onSkip: () => {
        // 跳过：不回答（不返回）。分两种场景（两难兼顾）：
        //   ① 队列非空（多问题，如 C8）→ 直接排到**队尾**（尾重排）；关闭当前后按序弹下一条，
        //      被跳过的稍后仍会重新弹出 —— 保 C8 / FP L359「放到队尾重新弹出」语义；
        //   ② 队列为空（单问题）→ 放入 deferred（**不重排**），避免 closeAndAdvance→processNext
        //      立刻重开同一弹窗（原「单问题即时重开、跳过无效」bug）；待后续新提问到达
        //      （enqueue 新批次）时再排回队尾「稍后重问」，无后续提问则保持关闭。
        if (this.queue.value.length > 0) {
          this.queue.value.push(item)
        } else {
          this.deferred.push(item)
        }
        this.closeAndAdvance(item)
        // busy 项的 closeAndAdvance 不推进队列（busy 分支直接 return），补推进
        if (!this.current.value && this.queue.value.length > 0) {
          this.processNext()
        }
      },
      onCancel: () => {
        // 取消：以"终止任务待讨论"文案作为回答提交（FP L363）
        const text = i18n.global.t('chat.ask_user_cancel_answer')
        this.submitAnswer(item, text)
        this.closeAndAdvance(item)
        if (!this.current.value && this.queue.value.length > 0) {
          this.processNext()
        }
      },
      onBusy: () => {
        // Pipe window closed (deadline passed) for the current dialog: keep it
        // open (still answerable — the reply becomes a new user message) but
        // release it as the blocking item so the next queued ask can pop on top.
        if (this.current.value?.id === item.id) {
          this.busy.set(item.id, { item, handle })
          this.current.value = null
          this.currentHandle.value = null
          this.processNext()
        }
      },
    }), {
      // 标题序号（FP L360）：第几个/共几个 = busy(已弹出未关闭) + 当前 + 队列
      title: this.buildTitle(item),
      modal: true,
      closable: false,
      collapsible: true,
      // 只保留折叠按钮：不做最小化（折叠态仍可拖拽，见 DialogShell）
      minimizable: false,
      resizable: true,
      // 例外：问题数不定（1..N），保持内容自适应高度，不套用弹窗默认高度
      height: 'auto',
      width: 480,
      // ask 弹窗居中于**屏幕工作区**（AskUser S1-6 / 设计 §4.6）：非窗口内居中、
      // 不记忆上次拖拽位置、每次弹出重新居中（纯计算见 utils/dialogPosition.js）。
      centerOnScreen: true,
    })
    this.currentHandle.value = handle
  }

  buildTitle(item) {
    const seq = item.seq || 1
    const total = item.total || 1
    const base = item.subSessionId
      ? i18n.global.t('chat.ask_user_subsession_title', { id: item.subSessionId.slice(0, 8) })
      : i18n.global.t('chat.ask_user_title')
    return base + ' ' + i18n.global.t('chat.ask_user_seq', { current: seq, total })
  }

  closeAndAdvance(item) {
    const busyEntry = this.busy.get(item.id)
    if (busyEntry) {
      busyEntry.handle?.close()
      this.busy.delete(item.id)
      return
    }
    if (this.current.value?.id === item.id) {
      this.currentHandle.value?.close()
      this.currentHandle.value = null
      this.current.value = null
      this.processNext()
    }
  }

  // 提交路径（20-gui）：发 ask-user-reply 事件（{ask_id, answer, custom, dialog_id}），
  // server 按 ask 状态权威路由——活跃回填当前 turn；已超时关闭则转为普通 user 消息（新 turn）。
  // 前端不再判断 deadline、不再 sendQueue 兜底、不再发 askUserQueued。
  submitAnswer(item, answer) {
    // 命中所选项（按 value 比较，兼容对象选项）→ custom 为空；否则 answer 即自定义文本。
    const custom = item.options.some(o => optionValue(o) === answer) ? '' : answer
    mq.emit(EventNames.askReply, {
      ask_id: item.askId,
      answer: answer,
      custom: custom,
      // 会话标识（61 §4.2 ask-user-reply.dialog_id，§0.6 必带；AskUser S1-4）：
      // **原样回带** `session-ask` 下发的同一值（不重算、不编造）。
      dialog_id: item.dialogId || '',
    })
  }

  /**
   * 「聊一聊」出口（AskUser S1-2 / 设计 §4.3 · §4.4 ②）：**仅主窗口**。
   *
   * 语义 =「先讨论，不直接作答」：
   *   ① 按 ask 的 `(dialog_id, session)` 定位发起会话；
   *   ② 当前窗口不是该会话 → 先切过去（复用既有链路：该会话已由独立对话窗口承载 →
   *      `gui.window.open-chat` 幂等激活该窗口（24 §1.1 I-1 归属语义）；否则主窗口切会话
   *      = `data-session-active-set` + `session-changed`，与 SessionsPane / ChatPanel 同一条）；
   *   ③ 以 **defer 语义**回填该 ask（文案 `chat.ask_user_chat_defer_answer`）：不把答案当最终
   *      作答，而是让本轮结束、把问题交回用户继续对话。**不改 `ask-user-reply` 字段**——
   *      设计 §8 未决项未经确认不得新增字段，defer 语义由 `answer` 文案表达；
   *   ④ 把**问题引用**放进输入框（`chat-insert-text`，可编辑；**不自动发送**）。
   *
   * 边界：会话已不存在 / 已被删除 → 不炸，可见提示并保留弹窗；重复点击幂等（`item.chatting`）。
   */
  async chatAbout(item) {
    if (!item || !this.canChat) return
    if (item.chatting) return
    item.chatting = true
    const t = (k, p) => i18n.global.t(k, p)
    try {
      const sid = item.sessionId || ''
      if (!sid) {
        message.error(t('chat.ask_user_chat_no_session'))
        return
      }
      let exists = false
      try {
        exists = !!(await getSession(sid))
      } catch (e) {
        console.warn('[askUserManager] 聊一聊：会话查询失败:', e)
        exists = false
      }
      if (!exists) {
        // 会话已不存在 / 已被删除：可见提示，弹窗保留（用户仍可选择其它出口）。
        message.error(t('chat.ask_user_chat_session_missing', { id: sid.slice(0, 8) }))
        return
      }
      // ② 切到发起会话（含对话窗口归属语义）
      await this.revealSession(sid)
      // ③ defer 语义回填（本轮结束，问题交回用户）
      this.submitAnswer(item, t('chat.ask_user_chat_defer_answer'))
      this.closeAndAdvance(item)
      // ④ 问题引用入输入框（不自动发送）
      mq.emit(EventNames.chatInsertText, {
        text: t('chat.ask_user_chat_quote', { question: item.question || '' }) + '\n',
      })
    } finally {
      item.chatting = false
    }
  }

  /**
   * 切到某会话（「聊一聊」第 ② 步）：已由独立对话窗口承载 → 激活该窗口（不抢主窗口，
   * I-1 ②）；否则在主窗口切换（`data-session-active-set` + `session-changed`，广播驱动
   * 会话树/任务区刷新）。任一步失败 → 仅告警，不阻断后续回填（用户仍能看到输入框）。
   */
  async revealSession(sessionId) {
    let windows = []
    try {
      const env = await mq.emit(EventNames.guiWindowList, {})
      const r = env && env.backend && env.backend.result
      windows = r && Array.isArray(r.windows) ? r.windows : []
    } catch (e) {
      console.warn('[askUserManager] 聊一聊：gui.window.list 失败（按主窗口切换）:', e)
    }
    if (windowForSession(windows, sessionId)) {
      try {
        await mq.emit(EventNames.guiWindowOpenChat, { session_id: sessionId })
        return
      } catch (e) {
        console.warn('[askUserManager] 聊一聊：激活对话窗口失败（回落主窗口切换）:', e)
      }
    }
    try {
      await setActiveSessionID(sessionId)
    } catch (e) {
      console.warn('[askUserManager] 聊一聊：data-session-active-set 失败:', e)
    }
    mq.emit(EventNames.sessionChanged, { session_id: sessionId })
  }

  cancelAll() {
    // 300ms 合并窗口内取消：必须置 null（仅 clearTimeout 不够）——否则 enqueue 的
    // 「新批次」判定（batchPending = _flushTimer !== null）恒真，后续新提问只入队不弹出。
    clearTimeout(this._flushTimer)
    this._flushTimer = null
    this.queue.value = []
    this.deferred = []
    for (const [id, entry] of this.busy) {
      entry.handle.close()
      this.busy.delete(id)
    }
    if (this.current.value) {
      this.currentHandle.value?.close()
      this.current.value = null
      this.currentHandle.value = null
    }
  }
}

export const askUserManager = new AskUserManager()
