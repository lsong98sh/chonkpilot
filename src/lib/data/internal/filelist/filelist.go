// Package filelist 是 filelist 域门面实现（`chonkpilot-data/facade` 的 FileListAPI）的落地包
// （阶段 4「internal 下沉」：由 `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖：全文索引的**文件清单**（增量清单读写；项目级 prj 库 `file_list` 表）。
// 定位：vfts 全文索引的增量清单——每个参与索引的文件一行，供插件 chonkpilot-plugin-vfts
// 判定 新增 / 跳过 / md5 变化 / 待删除，并驱动引擎按文件增量。
//
// 字段（snake_case，对齐 R-11 绝对路径口径）：
//
//	key        唯一主键（= sha1(绝对路径) 前 16 hex；与内容无关、稳定）
//	path       文件绝对路径（'/' 分隔）
//	size       文件字节数
//	mtime      文件修改时间（RFC3339Nano）
//	md5        文件内容 md5（十六进制；mtime/size 变化时用于判定内容是否真变）
//	doc_ids    该文件在 zvec 集合中的文档 id（主键）列表（JSON 数组；按文件删除用）
//	chunks     该文件的文档块数
//	indexed_at 最近一次索引时间（RFC3339）
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）；
//   - mq  绑定：persist 的 `data-filelist-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// 订阅面：本域**不广播 -refresh**（既有口径：清单消费者只有 vfts 插件自身）。
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package filelist

import (
	"errors"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// Service 是 filelist 域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 filelist 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }

// 编译期断言：实现完整 filelist 域门面（缺方法即编译不过）。
var _ facade.FileListAPI = (*Service)(nil)

// fileListTable 是项目级库中的 vfts 文件清单表名（用户拍板：file_list）。
const fileListTable = "file_list"

// fileListFields 是清单行的规范字段（put 时按此收敛，避免写入无关字段）。
var fileListFields = []string{"key", "path", "size", "mtime", "md5", "doc_ids", "chunks", "indexed_at"}

// FileListList 列出清单（可选 Prefix 按 path 前缀过滤 + Offset/Limit 分页）。
func (s *Service) FileListList(req facade.FileListListRequest) (facade.FileListListResponse, error) {
	db, err := s.PrjFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.FileListListResponse{}, err
	}
	t := db.Table(fileListTable)
	keys, err := t.ListKeys()
	if err != nil {
		return facade.FileListListResponse{}, err
	}
	sort.Strings(keys)
	all := make([]facade.FileListEntry, 0, len(keys))
	for _, k := range keys {
		var rec data.Record
		if ok, err := t.Get(k, &rec); err != nil || !ok {
			continue
		}
		if req.Prefix != "" && !strings.HasPrefix(kernel.Sval(rec["path"]), req.Prefix) {
			continue
		}
		if kernel.Sval(rec["key"]) == "" {
			rec["key"] = k
		}
		all = append(all, wire.FileListEntryFromWire(kernel.RecordView(rec, "")))
	}
	total := len(all)
	if req.Offset > 0 {
		if req.Offset >= len(all) {
			all = all[:0]
		} else {
			all = all[req.Offset:]
		}
	}
	if req.Limit > 0 && req.Limit < len(all) {
		all = all[:req.Limit]
	}
	return facade.FileListListResponse{List: all, Total: total}, nil
}

// FileListPut 单条 upsert（按 Key）：领域条目按清单规范字段落库（避免写入无关字段）。
func (s *Service) FileListPut(req facade.FileListPutRequest) (facade.FileListPutResponse, error) {
	e := req.Entry
	if e.Key == "" {
		return facade.FileListPutResponse{}, errors.New("filelist put: key required")
	}
	rec := data.Record{}
	for _, f := range fileListFields {
		switch f {
		case "key":
			rec[f] = e.Key
		case "path":
			rec[f] = e.Path
		case "size":
			rec[f] = e.Size
		case "mtime":
			rec[f] = e.MTime
		case "md5":
			rec[f] = e.MD5
		case "doc_ids":
			rec[f] = e.DocIDs
		case "chunks":
			rec[f] = e.Chunks
		case "indexed_at":
			rec[f] = e.IndexedAt
		}
	}
	db, err := s.PrjFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.FileListPutResponse{}, err
	}
	if err := db.Table(fileListTable).Upsert(e.Key, rec); err != nil {
		return facade.FileListPutResponse{}, err
	}
	return facade.FileListPutResponse{OK: true, ID: e.Key}, nil
}

// FileListDelete 按 Key 批量删除（重复键去重；不存在的键不计入 Deleted）。
func (s *Service) FileListDelete(req facade.FileListDeleteRequest) (facade.FileListDeleteResponse, error) {
	keys := dedupeKeys(req.Keys)
	if len(keys) == 0 {
		return facade.FileListDeleteResponse{}, errors.New("filelist del: keys required")
	}
	db, err := s.PrjFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.FileListDeleteResponse{}, err
	}
	t := db.Table(fileListTable)
	deleted := 0
	for _, k := range keys {
		if err := t.Delete(k); err == nil {
			deleted++
		}
	}
	return facade.FileListDeleteResponse{OK: true, Deleted: deleted}, nil
}

// dedupeKeys 去重并保持原序（空串剔除）。
func dedupeKeys(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range in {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}
