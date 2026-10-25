// data-scenario-* 信封层：场景域（list / load / save / delete）。
//
// v6（12-数据层 · 25-MCP与场景分层模型 §6）：数据源 = 文件化 **capability 根下的 `scenarios/` 子目录**
// （`<级别根>/capability/scenarios/`；四级 app/user/project/prjusr，场景 id 全局唯一跨级不重名），
// 不再落 usr DB。应答外形与旧版一致（list/data/id/ok），另加 level 字段标识来源级别。
// 逻辑见 `internal/scenario`。
package persist

import (
	"encoding/json"
	"errors"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// handleScenario 分发 data-scenario-<op>。
func (s *Service) handleScenario(op string, payload []byte) {
	method := "data-scenario-" + op
	var p ScenarioPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failID(method, p.ReqID, err)
		return
	}

	switch op {
	case "list":
		resp, err := s.ScenarioList(facade.ScenarioListRequest{InstanceID: p.InstanceID})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.ScenarioListResult(resp.List))
	case "load":
		m := p.merged()
		resp, err := s.ScenarioGet(facade.ScenarioGetRequest{
			InstanceID: p.InstanceID, ScenarioID: m.target(), Level: m.Level,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.ScenarioGetResult(resp.Scenario))
	case "save":
		resp, err := s.ScenarioSave(facade.ScenarioSaveRequest{
			InstanceID: p.InstanceID, Scenario: scenarioOf(p.merged()),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.ScenarioIDResult(resp.ID))
	case "delete":
		m := p.merged()
		if _, err := s.ScenarioDelete(facade.ScenarioDeleteRequest{
			InstanceID: p.InstanceID, ScenarioID: m.target(), Level: m.Level,
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	default:
		s.failID(method, p.ReqID, errors.New("unsupported scenario action: "+op))
	}
}
