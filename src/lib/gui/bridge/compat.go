package bridge

// compat.go：旧 chonkpilot 协议事件面兼容（20-gui：gui 参考 chonkpilot 工程构建对外壳）。
//
// server 保持定稿协议（llm-receive / llm-complete / tasks.*，见 61-消息一览 §4.3），
// 不在 server 侧为旧事件名加发。gui 桥在把 server 事件转发给前端（新协议名原样）的同时，
// 兼发旧 IDE 事件名（llm-started/llm-token/llm-tool-call/complete/llm-error/task-started/task-ended/
// tool-pair/tool-result），供旧回归脚本（run_llm.py 等，基于 testplan 旧协议）与旧订阅方使用。
//
// llm-started 语义（I-33）：由 server-starting（插件全部加载完成 = 服务就绪）兼发，作**服务就绪 ack**。
// 旧 llm-start.reply 无发布方（llm-start 走 promise result，无 -reply 广播），该死分支已删除；
// 每轮受理确权请用 turn-start（session-turn-start）。
//
// 数据源边界：仅转换 server 已发布的事件。tool-notify 由 server 直接发布（相对主题原名直通，
// 不经本兼容层，见 MessageList.onCompletionNotice）；llm-retry 无源，均不在此转换。

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// compatEmit 根据 server 事件（typ = 前端 type，如 llm-receive）兼发旧协议事件。
// 仅在 raw 可解析且字段齐备时发，失败静默。
// 兼发事件统一经 emit 注入当前实例 id（61-消息一览 §0 实例字段必带；已存在不覆盖，
// 复用 injectInstance 的幂等口径）。
func (b *Bridge) compatEmit(typ string, raw []byte) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	emit := func(name, payload string) { b.evalEmit(name, b.injectInstance(payload), "mq") }
	sid := sval(m["session"])
	tid := sval(m["turn"])
	switch typ {
	case msgkeys.TopicServerStarting:
		// 服务就绪 ack（取代旧 llm-start.reply 死分支，见文件头）：插件全部加载完成 →
		// 旧协议 llm-started。payload 与既有兼容契约一致 {session_id, turn_id, sub, notify}
		// （就绪无轮次归属，session/turn 留空）+ 兼容层统一 injectInstance 注入 instance_id；
		// 不附 started_at（兼容契约未定义该字段，旧订阅方按 {session_id, turn_id} 解析）。
		emit("llm-started", mustJSON(map[string]any{
			"session_id": sid, "turn_id": tid, "sub": false, "notify": false,
		}))
	case "llm-receive":
		// 流内容单主题 → 旧协议拆分流事件（llm-token / llm-tool-call）
		switch sval(m["type"]) {
		case "text":
			p := payloadMap(m)
			emit("llm-token", mustJSON(map[string]any{
				"session_id": sid, "turn_id": tid, "type": "text",
				"content": sval(p["text"]),
			}))
		case "tool-call":
			p := payloadMap(m)
			emit("llm-tool-call", mustJSON(map[string]any{
				"session_id": sid, "turn_id": tid,
				"tool":         sval(p["tool"]),
				"arguments":    p["arguments"],
				"tool_call_id": sval(p["tool_call_id"]),
			}))
		}
	case "llm-complete":
		// 唯一终态 → 旧 complete（终结 ack）；error → 旧 llm-error（诊断/订阅方）
		st := sval(m["status"])
		emit("complete", mustJSON(map[string]any{
			"session_id": sid, "turn_id": tid, "status": st,
		}))
		if st == "error" {
			// retryable：**透传 llm-complete 的真实分类**（S21 定稿新增可选字段，
			// 来源 = 该 turn 的 *LLMError.Retryable：超时/网络/429/5xx → true；
			// 协议/鉴权/EMPTY_REPLY 等 → false）。字段缺失（旧发布方）→ false（旧兼容契约语义）。
			retryable := false
			if v, ok := m["retryable"].(bool); ok {
				retryable = v
			}
			// retry_attempt/retry_count：**兼容占位常量**——llm-complete 载荷不携带重试计数，
			// 桥侧无可取真实值（唯一真实值为 retryable），保留旧字段仅为旧订阅方（ChatPanel
			// 进度文案）解析不变；其取值为 1/0 时 `attempt<=maxRetries` 恒 false → 不误报"重试中"。
			emit("llm-error", mustJSON(map[string]any{
				"session_id": sid, "turn_id": tid,
				"code": sval(m["code"]), "message": sval(m["message"]),
				"retryable": retryable, "retry_attempt": 1, "retry_count": 0,
			}))
		}
	case "tasks.started":
		// 任务树事件（server 编排）→ 旧 task-started + tool-pair 开始
		// 注：旧兼容事件名 task-started 与契约主题 task-started 同名（值同）→ 以 msgkeys 常量引用。
		emit(msgkeys.TopicTaskStarted, string(raw))
		emit("tool-pair", toolPairMsg(m, "running"))
	case "tasks.updated":
		// 转后台（pending）→ tool-pair async（旧协议转异步标记）
		if sval(m["state"]) == "pending" {
			emit("tool-pair", toolPairMsg(m, "async"))
		}
	case "tasks.done":
		// 终态 → 旧 task-ended + tool-pair 终态 + tool-result
		emit("task-ended", string(raw))
		st := sval(m["state"])
		emit("tool-pair", toolPairMsg(m, st))
		emit("tool-result", mustJSON(map[string]any{
			"tool_id": sval(m["task_id"]), "task_id": sval(m["task_id"]),
			"tool": sval(m["tool"]), "session_id": sval(m["session_id"]),
			"turn_id": sval(m["turn_id"]), "result": taskResultOf(m), "status": st,
		}))
	}
}

// payloadMap 取 llm-receive 的 payload 对象。
func payloadMap(m map[string]any) map[string]any {
	if p, ok := m["payload"].(map[string]any); ok {
		return p
	}
	return map[string]any{}
}

// toolPairMsg 从任务事件（TaskNode JSON 字段）组装旧 tool-pair。
func toolPairMsg(m map[string]any, status string) string {
	title := sval(m["simplified"])
	if title == "" {
		title = sval(m["name"])
	}
	return mustJSON(map[string]any{
		"tool_id": sval(m["task_id"]), "task_id": sval(m["task_id"]),
		"tool": sval(m["tool"]), "tool_call_id": sval(m["tool_call_id"]),
		"status": status, "session_id": sval(m["session_id"]), "turn_id": sval(m["turn_id"]),
		"title": title,
	})
}

// taskResultOf 终态摘要：done → result_summary；否则 error。
func taskResultOf(m map[string]any) string {
	if sval(m["state"]) == "done" {
		return sval(m["result_summary"])
	}
	return sval(m["error"])
}

// mustJSON 序列化（失败返回 "{}"）。
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
