// data-mcp-* 信封层：MCP server 配置域（list / load / save / delete）。
//
// 存储 = 四级 `<级别>/capability/mcps/<名>.json`（app/user/project/prjusr；同名最具体级优先）。
// 应答外形：list `{list}` / load `{data}` / save `{ok,name}` / delete `{ok}`。逻辑见 `internal/mcp`。
package persist

import (
	"encoding/json"
	"errors"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// handleMCP 分发 data-mcp-<op>。
func (s *Service) handleMCP(op string, payload []byte) {
	method := "data-mcp-" + op
	var p McpPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failID(method, p.ReqID, err)
		return
	}

	switch op {
	case "list":
		resp, err := s.McpList(facade.McpListRequest{InstanceID: p.InstanceID})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.McpListResult(resp.List))
	case "load":
		m := p.merged()
		resp, err := s.McpGet(facade.McpGetRequest{
			InstanceID: p.InstanceID, Name: m.target(), Level: m.Level,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.McpGetResult(resp.Server))
	case "save":
		m := p.merged()
		resp, err := s.McpSave(facade.McpSaveRequest{
			InstanceID: p.InstanceID,
			Server:     m.McpServer,
			OldName:    m.OldName,
			OldLevel:   m.OldLevel,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.McpNameResult(resp.Name))
	case "delete":
		m := p.merged()
		if _, err := s.McpDelete(facade.McpDeleteRequest{
			InstanceID: p.InstanceID, Name: m.target(), Level: m.Level,
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	default:
		s.failID(method, p.ReqID, errors.New("unsupported mcp action: "+op))
	}
}
