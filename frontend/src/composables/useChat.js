import { ref } from 'vue'
import * as chatApi from '../api/chat'

export function useChat() {
  const messages = ref([])
  const isStreaming = ref(false)

  async function send(text, sessionId, turnId) {
    messages.value.push({ role: 'user', content: text })
    isStreaming.value = true
    try {
      const result = await chatApi.sendChatMessage(sessionId, turnId, text)
      messages.value.push({ role: 'assistant', content: result.a || result.message })
    } catch (e) {
      messages.value.push({ role: 'assistant', content: `Error: ${e.message}` })
    } finally {
      isStreaming.value = false
    }
  }

  function clearMessages() {
    messages.value = []
  }

  return { messages, isStreaming, send, clearMessages }
}
