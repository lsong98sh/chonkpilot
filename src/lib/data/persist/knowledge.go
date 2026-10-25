// data-knowledge-* 信封层：知识库域（root / list / read / save / create / delete /
// rename / mkdir / rmdir / rename-dir / import）。
//
// 逻辑在 `internal/knowledge`（G-26 作用域校验在实现侧，不绕过）。
package persist

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// handleKnowledge 分发 data-knowledge-<action>；未知 action（含 -refresh 误入）忽略不回复。
func (s *Service) handleKnowledge(action string, payload []byte) {
	method := "data-knowledge-" + action
	var p KnowledgePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failID(method, p.ReqID, err)
		return
	}
	d := p.fields()

	switch action {
	case "root": // root 只解析 kind 根，不依赖实例（app 根可独立存在）
		resp, err := s.KnowledgeRoot(facade.KnowledgeRootRequest{InstanceID: p.InstanceID, Kind: d.Kind})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.KnowledgeRootResult(resp))
	case "list":
		resp, err := s.KnowledgeList(facade.KnowledgeListRequest{InstanceID: p.InstanceID, Dir: d.Dir})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.KnowledgeListResult(resp))
	case "read":
		resp, err := s.KnowledgeRead(facade.KnowledgeReadRequest{InstanceID: p.InstanceID, Path: d.Path})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.KnowledgeReadResult(resp))
	case "save":
		if _, err := s.KnowledgeSave(facade.KnowledgeSaveRequest{
			InstanceID: p.InstanceID, Path: d.Path, Doc: knowledgeDocOf(d.Doc),
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	case "create":
		resp, err := s.KnowledgeCreate(facade.KnowledgeCreateRequest{
			InstanceID: p.InstanceID, Dir: d.Dir, Type: d.Type, Name: d.Name,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, map[string]any{msgkeys.FieldOk: true, msgkeys.FieldPath: resp.Path})
	case "delete":
		if _, err := s.KnowledgeDelete(facade.KnowledgeDeleteRequest{
			InstanceID: p.InstanceID, Path: d.Path,
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	case "rename":
		if _, err := s.KnowledgeRename(facade.KnowledgeRenameRequest{
			InstanceID: p.InstanceID, Path: d.Path, NewName: d.NewName,
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	case "mkdir":
		resp, err := s.KnowledgeMkdir(facade.KnowledgeMkdirRequest{
			InstanceID: p.InstanceID, Parent: d.Parent, Name: d.Name,
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, map[string]any{msgkeys.FieldOk: true, msgkeys.FieldPath: resp.Path})
	case "rmdir":
		if _, err := s.KnowledgeRmdir(facade.KnowledgeRmdirRequest{
			InstanceID: p.InstanceID, Path: d.Path,
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	case "rename-dir":
		if _, err := s.KnowledgeRenameDir(facade.KnowledgeRenameDirRequest{
			InstanceID: p.InstanceID, Path: d.Path, NewName: d.NewName,
		}); err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.OKResult())
	case "import": // 导入 / 新建「其他类型」（61 §3.3；逐文件尽力而为 → ok + paths + errors）
		resp, err := s.KnowledgeImport(facade.KnowledgeImportRequest{
			InstanceID: p.InstanceID, Dir: d.Dir, Files: knowledgeImportFiles(d.Files),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		paths := make([]any, 0, len(resp.Paths))
		for _, path := range resp.Paths {
			paths = append(paths, path)
		}
		result := map[string]any{
			msgkeys.DataKnowledgeImportResultOk:    resp.OK,
			msgkeys.DataKnowledgeImportResultPaths: paths,
		}
		if len(resp.Errors) > 0 {
			errs := make([]any, 0, len(resp.Errors))
			for _, e := range resp.Errors {
				errs = append(errs, map[string]any{msgkeys.FieldName: e.Name, msgkeys.FieldError: e.Error})
			}
			result[msgkeys.DataKnowledgeImportResultErrors] = errs
		}
		s.replyID(method, p.ReqID, result)
	}
}

// knowledgeDocOf 把载荷里的 `doc` 对象转门面文档领域形态（非对象 / 缺省 → 零值）。
func knowledgeDocOf(raw json.RawMessage) facade.KnowledgeDoc {
	var doc facade.KnowledgeDoc
	if raw = nonNullRaw(raw); len(raw) == 0 || raw[0] != '{' {
		return doc
	}
	_ = json.Unmarshal(raw, &doc)
	return doc
}

// knowledgeImportFiles 把导入清单转门面入参。
func knowledgeImportFiles(files []KnowledgeImportItem) []facade.KnowledgeImportFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]facade.KnowledgeImportFile, 0, len(files))
	for _, f := range files {
		out = append(out, facade.KnowledgeImportFile{Name: f.Name, Content: f.Content})
	}
	return out
}
