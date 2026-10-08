import { ref, computed, h, defineAsyncComponent } from 'vue'
import { dialog } from '../components/dialog'
import { i18n } from '../plugins/i18n'
import mq from './mq'
import { EventNames } from '../events/event-names'

// 懒加载：仅在出现 AskUser 问题弹窗时加载
const AskUserContent = defineAsyncComponent(() => import('../views/chat/AskUserContent.vue'))

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
  // 批次计数（标题"第几个/共几个"）：新批次开始时重置
  batchSeq = 0
  batchTotal = 0
  _flushTimer = null

  active = computed(() => this.current.value !== null || this.busy.size > 0)
  pending = computed(() => this.queue.value.length)

  enqueue(data) {
    // 新批次开始（无 current / 无 busy / 队列空 / 无 pending timer）：
    // 重置批次计数并启动 300ms 合并窗口，同一批连续到达的 ask-user 事件
    // （多问题语义）全部入队后才弹出第一条，使标题序号显示"第几个/共几个"。
    const batchPending = this._flushTimer !== null
    if (!this.current.value && this.busy.size === 0 && !batchPending && this.queue.value.length === 0) {
      this.batchSeq = 0
      this.batchTotal = 0
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
      onAnswer: (answer) => {
        this.submitAnswer(item, answer)
        this.closeAndAdvance(item)
      },
      onSkip: () => {
        // 跳过：不回答（不返回），关闭弹窗后把该 ask 放到队列尾部稍后重问
        this.queue.value.push(item)
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

  // 提交路径（20-gui）：发 ask-user-reply 事件（{ask_id, answer, custom}），
  // server 按 ask 状态权威路由——活跃回填当前 turn；已超时关闭则转为普通 user 消息（新 turn）。
  // 前端不再判断 deadline、不再 sendQueue 兜底、不再发 askUserQueued。
  submitAnswer(item, answer) {
    const custom = item.options.includes(answer) ? '' : answer
    mq.emit(EventNames.askReply, {
      ask_id: item.askId,
      answer: answer,
      custom: custom,
    })
  }

  cancelSession(subSessionId) {
    this.queue.value = this.queue.value.filter(
      item => item.subSessionId !== subSessionId
    )
    for (const [id, entry] of this.busy) {
      if (entry.item.subSessionId === subSessionId) {
        entry.handle.close()
        this.busy.delete(id)
      }
    }
    if (this.current.value?.subSessionId === subSessionId) {
      this.currentHandle.value?.close()
      this.current.value = null
      this.currentHandle.value = null
      this.processNext()
    }
  }

  cancelAll() {
    clearTimeout(this._flushTimer)
    this.queue.value = []
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
