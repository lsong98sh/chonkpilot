// data-mcp-* 信封层：MCP server 配置域（list / load / save / delete）。
//
// 存储 = 四级 `<级别>/capability/mcps/<名>.json`（app/user/project/prjusr；同名最具体级优先）。
// 应答外形：list `{list}` / load `{data}` / save `{ok,name}` / delete `{ok}`。逻辑见 `internal/mcp`。
package persist

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// mcpEnvelopeFields 是 MCP save 载荷 `data` 里**信封 / 路由键**（不属定义正文，不得当未知字段落盘）：
// `req_id`/`instance_id` = 信封头（Header）；`old_name`/`old_level` = 改名路由（前端放在 data 内）。
var mcpEnvelopeFields = map[string]bool{
	"req_id": true, "instance_id": true, "old_name": true, "old_level": true,
}

// mcpUnknownFields 取 MCP save 载荷 `data` 中 **DTO 未声明**的字段（= 「未知字段保留」的来源）。
// 只看 `data` 对象顶层（信封 / 路由键见 mcpEnvelopeFields，不参与）；
// 无 `data` / 解析失败 / 全为已知字段 → nil（落盘内容与改前逐字节一致）。
func mcpUnknownFields(raw json.RawMessage) map[string]any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	known := facade.McpServerFields()
	var extra map[string]any
	for k, v := range obj {
		if known[k] || mcpEnvelopeFields[k] {
			continue
		}
		if extra == nil {
			extra = make(map[string]any, len(obj))
		}
		extra[k] = v
	}
	return extra
}

// handleMCP 分发 data-mcp-<op>。
func (s *Service) handleMCP(op string, payload []byte) {
	method := "data-mcp-" + op
	var p McpPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failDecode(method, payload, err)
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
		// 未知字段保留（61 §3.1）：`data` 中 DTO 未声明的字段原样并入落盘文件顶层
		// （已知字段优先；纯已知字段 = nil → 落盘与改前逐字节一致）。
		m.McpServer.Extra = mcpUnknownFields(p.Data)
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
