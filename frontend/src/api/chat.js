import { SendChatMessage, CancelChat } from '../../wailsjs/go/main/App'

export function sendChatMessage(sessionId, turnId, message, llmName, thinkEnabled, effort) {
  return SendChatMessage({
    session_id: sessionId,
    turn_id: turnId,
    q: message,
    llm: llmName || '',
    think: thinkEnabled || '',
    effort: effort || '',
  })
}

export function cancelChat(turnId) {
  return CancelChat({ turn_id: turnId })
}
