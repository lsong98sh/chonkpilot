// data-memory-* 信封层：记忆库域 + 记忆提取进度。
//
// 报文形状与语义（list {list} / read {data} / save {ok,id} / delete {ok,id}）：
//   - list：预置类别（首次预置） + 自定义类别（目录扫描）；
//   - read：预置恒可读；自定义须已存在（否则 unknown）；
//   - save：写/建类别内容（payload `{data:{category, content}}`）；
//   - delete：删除自定义类别（仅清单项 + 内容文件）；预置类别不可删除（只可清空内容）。
//
// save/delete 后广播 data-memory-refresh（既有主题）——由门面实现侧发出。
// 逻辑见 `internal/memory`。
package persist

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// handleMemory 分发 data-memory-<op>（含 extract-*）。
func (s *Service) handleMemory(op string, payload []byte) {
	method := "data-memory-" + op
	var p MemoryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failID(method, p.ReqID, err)
		return
	}
	d := p.fields()

	switch op {
	case "list":
		resp, err := s.MemoryList(facade.MemoryListRequest{InstanceID: p.InstanceID})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.MemoryListResult(resp.List))
	case "read":
		resp, err := s.MemoryGet(facade.MemoryGetRequest{InstanceID: p.InstanceID, Category: d.Category})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.MemoryGetResult(resp.Doc))
	case "save":
		resp, err := s.MemorySave(facade.MemorySaveRequest{
			InstanceID: p.InstanceID, Category: d.Category, Content: d.Content,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.MemoryIDResult(resp.ID))
	case "delete":
		resp, err := s.MemoryDelete(facade.MemoryDeleteRequest{InstanceID: p.InstanceID, Category: d.Category})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.MemoryIDResult(resp.ID))
	// 记忆提取进度专用表（data-memory-extract-*；OP-05/06，2026-10-06）：
	// 记录 (会话, 类别) → 最后已成功提取的 turn，落 prjusr（不再用 config 键）。
	case "extract-load":
		resp, err := s.MemoryExtractLoad(facade.MemoryExtractLoadRequest{
			InstanceID: p.InstanceID, SessionID: d.SessionID, Category: d.Category,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.MemoryExtractListResult(resp.List))
	case "extract-save":
		resp, err := s.MemoryExtractSave(facade.MemoryExtractSaveRequest{
			InstanceID: p.InstanceID, SessionID: d.SessionID, Category: d.Category, LastTurnID: d.LastTurnID,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, map[string]any{msgkeys.FieldOk: resp.OK, msgkeys.FieldId: resp.ID})
	case "extract-delete":
		if _, err := s.MemoryExtractDelete(facade.MemoryExtractDeleteRequest{
			InstanceID: p.InstanceID, SessionID: d.SessionID, Category: d.Category,
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	}
}
