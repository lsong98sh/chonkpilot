// data-filelist-* 信封层：文件清单域（list / put / del；项目级 prj 库 file_list 表）。
//
// 本域**不广播 -refresh**（清单消费者只有 vfts 插件自身）。逻辑见 `internal/filelist`。
package persist

import (
	"encoding/json"
	"errors"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
)

// handleFileList 分发 data-filelist-<op>。
func (s *Service) handleFileList(op string, payload []byte) {
	method := "data-filelist-" + op
	var p FileListPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failID(method, p.ReqID, err)
		return
	}
	f := p.merged()

	switch op {
	case "list":
		resp, err := s.FileListList(facade.FileListListRequest{
			InstanceID: p.InstanceID,
			Prefix:     f.Prefix,
			Offset:     int(f.Offset),
			Limit:      int(f.Limit),
		})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.FileListListResult(resp))
	case "put":
		r := facade.FileListPutRequest{InstanceID: p.InstanceID}
		if len(f.Entries) > 0 { // 批量优先
			for _, e := range f.Entries {
				r.Entries = append(r.Entries, fileListEntryOf(e))
			}
		} else {
			r.Entry = f.entry()
		}
		resp, err := s.FileListPut(r)
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.FileListIDResult(resp.ID))
	case "del":
		keys := f.Keys
		if id := f.target(); id != "" { // 顶端单键（61 §3.5 兼容形态）
			keys = append(keys, id)
		}
		resp, err := s.FileListDelete(facade.FileListDeleteRequest{InstanceID: p.InstanceID, Keys: keys})
		if err != nil {
			s.failID(method, p.ReqID, err)
			return
		}
		s.replyID(method, p.ReqID, wire.FileListDeletedResult(resp.Deleted))
	default:
		s.failID(method, p.ReqID, errors.New("unsupported filelist action: "+op))
	}
}
