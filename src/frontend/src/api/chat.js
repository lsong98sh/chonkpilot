import { EventNames } from '../events/event-names'
import mq from '../utils/mq'

// 域化改造（阶段三，20-gui）：send/队列出队不再走 SendChatMessage /
// SendNoticeTurn RPC，改为发布 llm-start 事件。turn 由前端自分配 uuid（客户端分配
// 语义，server llm-start 的 session/turn 必填）；桥把 llm-start 拆两步：
//   llm-start{session,turn,llm} + llm-send{type:text-user, content}
// 取消走 llm-cancel{session,turn}（21-llm-server），CancelChat RPC 已移除。

// 生成客户端自分配 id（uuid）：session / turn 均由前端分配（21-llm-server）。
function newUuid(prefix) {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return prefix + '-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10)
}

// newTurnId 生成客户端自分配 turn id（uuid）。
export function newTurnId() {
  return newUuid('turn')
}

// newSessionId 生成客户端自分配 session id（uuid）。
export function newSessionId() {
  return newUuid('sess')
}

// 发布 llm-start（前端 send/队列出队时调用；载荷对齐 server 协议：
// session/turn 必填 + llm；q 文本随事件携带，由桥拆为 llm-send{text-user}）。
// scenarioId = 场景目录名/key（空 = 默认场景）。
// 返回 { turn, ack }：
//   - turn：前端自分配的 turn id（MessageList 用它过滤流事件，必须**同步**取用）；
//   - ack：/publish 应答 promise（桥同步受理：server 落库 session/turn 后才返回）
//     —— T5 方案 B 的「落库回执」，user 气泡据此渲染（不再乐观插入）。
// E-25：ack 同为请求-响应面，补 30s 超时（后端卡死 → resolve(null) → 发送端按未受理兜底）。
export function publishLLMStart(sessionId, text, llmName, thinkEnabled, effort, scenarioId) {
  const turn = newTurnId()
  const ack = mq.emit(EventNames.llmStart, {
    session: sessionId,
    turn: turn,
    llm: llmName || '',
    think: thinkEnabled || '',
    effort: effort || '',
    scenario_id: scenarioId || '',
    q: text,
  }, { timeout: 30000 })
  return { turn, ack }
}

// 发布「同轮次继续」llm-start（continue=true）：复用既有 turn（不新开 uuid turn），
// server 侧把注入的 user 消息标 Kind=continue（非新轮边界，拼接视为同一轮）。
// turn 为空时由 server 按 session 取最近一轮（桥不再为 continue 分配 turn）。
// 返回 { turn, ack }（turn 为空表示由后端解析；ack 语义同 publishLLMStart）。
export function publishLLMContinue(sessionId, text, turnId, llmName, thinkEnabled, effort, scenarioId) {
  const ack = mq.emit(EventNames.llmStart, {
    session: sessionId,
    turn: turnId || '',
    llm: llmName || '',
    think: thinkEnabled || '',
    effort: effort || '',
    scenario_id: scenarioId || '',
    continue: true,
    q: text,
  }, { timeout: 30000 })
  return { turn: turnId || '', ack }
}
