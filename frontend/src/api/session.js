import { ListSessions, ListAllSessions, CreateSession, GetSession, DeleteSession, UpdateSessionTitle, GetTurnsBySession, GetTurnsPaginated, GetLatestSessionID, GetActiveSessionID, SetActiveSessionID, GetMessageContent, SubscribeSession, UnsubscribeSession } from '../../wailsjs/go/main/App'

export function listSessions() {
  return ListSessions()
}

export function listAllSessions() {
  return ListAllSessions()
}

export function createSession(workDir, title) {
  return CreateSession({ workDir, title })
}

export function getSession(id) {
  return GetSession(id)
}

export function deleteSession(id) {
  return DeleteSession(id)
}

export function updateSessionTitle(id, title) {
  return UpdateSessionTitle(id, title)
}

export function getTurnsBySession(sessionId, brief = true) {
  return GetTurnsBySession(sessionId, brief)
}

export function getTurnsPaginated(sessionId, beforeTurnId, targetMessages = 50, targetBytes = 200 * 1024) {
  return GetTurnsPaginated(sessionId, beforeTurnId, targetMessages, targetBytes)
}

export function getLatestSessionID() {
  return GetLatestSessionID()
}

export function getActiveSessionID() {
  return GetActiveSessionID()
}

export function setActiveSessionID(sessionId) {
  return SetActiveSessionID(sessionId)
}

export function subscribeSession(sessionId) {
  return SubscribeSession(sessionId)
}

export function unsubscribeSession(sessionId) {
  return UnsubscribeSession(sessionId)
}
