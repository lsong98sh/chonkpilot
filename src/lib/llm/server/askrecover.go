// ask 失效恢复（MOD-1 M6b）：`data-ask-pending` 回查 + 启动扫描 + 失效回执。
//
// 设计约束（41 D-37 / 61 §4.2）：**不建 ask 表** —— ask_id 随发起时的 role=tool running
// 行落在 `messages.ask_id`；未答 ask 由启动扫描（遗留清理前）识别，随后统一置 interrupted。
// 失效 / 重启丢失的应答按 61 语义「转普通 user 消息 = 新 turn」，回查不到则**明确失败**
// （不再静默丢弃）。
package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// askRecord 是 data-ask-pending 回查项（宿主侧视图，字段与 data 面一致）。
type askRecord struct {
	AskID string `json:"ask_id"`
	// DialogID 该 ask 所在对话（一树一库时回带；调用方据此续期到该对话）。
	DialogID   string `json:"dialog_id"`
	SessionID  string `json:"session_id"`
	TurnID     string `json:"turn_id"`
	ToolCallID string `json:"tool_call_id"`
	Status     string `json:"status"`
	Question   string `json:"question"`
	CreatedAt  string `json:"created_at"`
}

// dataAskPending 经 data 面回查 ask 行：
//   - askID 空 + pendingOnly → 未答 ask（启动扫描）；
//   - askID 非空 → 该 ask 精确回查（含已终态；失效回执据此判定）。
func (s *Server) dataAskPending(instanceID, askID string, pendingOnly bool) ([]askRecord, error) {
	data := map[string]any{"pending_only": pendingOnly}
	if askID != "" {
		data["ask_id"] = askID
	}
	res, err := dataRequest(s.bus, msgkeys.TopicDataAskPending, map[string]any{
		"instance_id": instanceID,
		"data":        data,
	})
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(res["items"])
	if err != nil {
		return nil, err
	}
	var items []askRecord
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// scanPendingAsks 启动扫描（首次使用该 instance、遗留清理**之前**）：只命中带 ask_id 且
// 非终态的 ask 行。仅留痕——随后 cleanupStaleToolPairs 统一置 interrupted（前端置灰）。
func (s *Server) scanPendingAsks(instanceID string) {
	items, err := s.dataAskPending(instanceID, "", true)
	if err != nil {
		logf("[chonkpilot-server] 启动扫描 ask 失败（instance=%s）: %v\n", instanceID, err)
		return
	}
	if len(items) > 0 {
		logf("[chonkpilot-server] 启动扫描：instance=%s 发现 %d 条未答 ask（随遗留清理置 interrupted）\n",
			instanceID, len(items))
	}
}

// replyExpiredAsk 处理「等待登记已不存在」的 ask 应答（超时摘除 / 进程重启）。
//
//   - 行已终态（completed / failed / cancelled）→ 重复应答，明确失败、不重复开轮；
//   - 行非终态（running / interrupted）→ 按 61 §4.2「转普通 user 消息 = 新 turn」开新轮喂答案；
//   - 回查不到 / 缺 instance_id → 明确失败（不静默）。
func (s *Server) replyExpiredAsk(instanceID, askID, answer string) error {
	if askID == "" {
		return methodErr("ask 已失效：ask_id 缺失")
	}
	if strings.TrimSpace(answer) == "" {
		return methodErr("ask 已失效：答案为空（ask_id=" + askID + "）")
	}
	if strings.TrimSpace(instanceID) == "" {
		return methodErr("ask 已失效：载荷缺 instance_id，无法回查（ask_id=" + askID + "）")
	}
	items, err := s.dataAskPending(instanceID, askID, false)
	if err != nil {
		return methodErr("ask 失效回查失败：" + err.Error())
	}
	if len(items) == 0 || items[0].SessionID == "" {
		return methodErr("ask 已失效：未找到落库记录（ask_id=" + askID + "）")
	}
	it := items[0]
	switch it.Status {
	case "completed", "failed", "cancelled":
		return methodErr("ask 已回答或已关闭（ask_id=" + askID + "，status=" + it.Status + "）")
	}

	// 新轮：复用该 ask 所属会话（turn 由宿主分配）。
	res := s.acceptStart(StartReq{
		InstanceID: instanceID,
		Session:    it.SessionID,
		Turn:       "turn-" + newID(),
	})
	if ok, _ := res["accepted"].(bool); !ok {
		msg, _ := res["error"].(string)
		return methodErr("ask 已失效：新轮受理失败（" + msg + "）")
	}
	turnID, _ := res["turn"].(string)
	body, _ := json.Marshal(SendReq{
		InstanceID: instanceID,
		Session:    it.SessionID,
		Turn:       turnID,
		Type:       SendTypeTextUser,
		Content:    answer,
	})
	if err := s.onLLMSend(context.Background(), "", &mq.Value{Payload: body}); err != nil {
		return methodErr("ask 已失效：新轮投递失败（" + err.Error() + "）")
	}
	logf("[chonkpilot-server] ask 已失效，答案转新轮：ask_id=%s session=%s turn=%s\n",
		askID, it.SessionID, turnID)
	return nil
}
