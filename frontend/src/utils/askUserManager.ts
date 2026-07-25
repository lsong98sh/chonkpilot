import { ref, computed, h } from 'vue'
import { dialog } from '../components/dialog'
import AskUserContent from '../components/chat/AskUserContent.vue'
import { RespondAskUser } from '../../wailsjs/go/main/App'

interface AskUserItem {
  id: string
  question: string
  options: string[]
  custom: boolean
  pipeAddr: string
  subSessionId: string
  resolve: (answer: string) => void
  reject: (err: Error) => void
}

class AskUserManager {
  private queue = ref<AskUserItem[]>([])
  private current = ref<AskUserItem | null>(null)
  private dialogHandle = ref<any>(null)

  readonly active = computed(() => this.current.value !== null)
  readonly pending = computed(() => this.queue.value.length)

  enqueue(data: {
    question: string
    options?: string[]
    custom?: boolean
    pipeAddr?: string
    subSessionId?: string
  }): Promise<string> {
    return new Promise((resolve, reject) => {
      const item: AskUserItem = {
        id: Date.now().toString(36) + Math.random().toString(36).slice(2, 8),
        question: data.question,
        options: data.options || [],
        custom: data.options && data.options.length === 0 ? true : !!data.custom,
        pipeAddr: data.pipeAddr || '',
        subSessionId: data.subSessionId || '',
        resolve,
        reject,
      }
      this.queue.value.push(item)
      if (!this.current.value) {
        this.processNext()
      }
    })
  }

  private processNext() {
    if (this.queue.value.length === 0) {
      this.current.value = null
      return
    }
    const item = this.queue.value.shift()!
    this.current.value = item
    this.showDialog(item)
  }

  private showDialog(item: AskUserItem) {
    const handle = dialog.show(h(AskUserContent, {
      question: item.question,
      options: item.options,
      custom: item.custom,
      subSessionId: item.subSessionId,
      onAnswer: (answer: string) => {
        this.submitAnswer(item, answer)
        handle.close()
      },
    }), {
      title: item.subSessionId
        ? `🤔 Sub-Session #${item.subSessionId.slice(0, 8)} Asks`
        : '🤔 AI Asks You',
      modal: true,
      closable: false,
      collapsible: true,
      minimizable: false,
      resizable: false,
      width: 480,
      position: { x: undefined as any, y: window.innerHeight * 0.25 },
      onAction: (action) => {
        if (action === 'closed') {
          this.current.value = null
          this.processNext()
        }
      },
    })
    this.dialogHandle.value = handle
  }

  private async submitAnswer(item: AskUserItem, answer: string) {
    try {
      await RespondAskUser({
        answer: answer,
        custom: item.options.includes(answer) ? '' : answer,
        pipe_addr: item.pipeAddr,
      })
      item.resolve(answer)
    } catch (e) {
      console.error('Failed to send ask_user response:', e)
      item.reject(new Error('Failed to send response'))
    }
  }

  cancelSession(subSessionId: string) {
    this.queue.value = this.queue.value.filter(
      item => item.subSessionId !== subSessionId
    )
    if (this.current.value?.subSessionId === subSessionId) {
      this.current.value.reject(new Error('Session cancelled'))
      this.dialogHandle.value?.close()
      this.current.value = null
      this.processNext()
    }
  }

  cancelAll() {
    for (const item of this.queue.value) {
      item.reject(new Error('All asks cancelled'))
    }
    this.queue.value = []
    if (this.current.value) {
      this.current.value.reject(new Error('All asks cancelled'))
      this.dialogHandle.value?.close()
      this.current.value = null
    }
  }
}

export const askUserManager = new AskUserManager()
