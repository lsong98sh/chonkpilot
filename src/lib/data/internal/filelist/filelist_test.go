// filelist 域单测（A-20 / A-21）：清单列表改单事务扫描后的读序 / 过滤 / 分页行为不变；
// 批量删除错误聚合 + ErrNotFound 幂等（不存在的键不计入 Deleted、不报错）。
package filelist

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

func newFileListService(t *testing.T) *Service {
	t.Helper()
	return New(kernel.NewBase(nil, kernel.Options{
		UsrPath: filepath.Join(t.TempDir(), "usr.db"), AppDir: t.TempDir(),
	}))
}

// TestFileListPutListDelete 走一遍 put → list（排序 / 前缀 / 分页）→ delete（幂等）。
// prj 库经 Scope 落临时目录（dataDir 非空 = 临时形态，prj 与 prjusr 同库），不污染用户数据根。
func TestFileListPutListDelete(t *testing.T) {
	s := newFileListService(t)
	inst := "ins-fl"
	scope := facade.Scope{WorkDir: t.TempDir(), DataDir: t.TempDir()}

	for _, e := range []facade.FileListEntry{
		{Key: "b", Path: "/x/b.go", Size: 2},
		{Key: "a", Path: "/x/a.go", Size: 1},
		{Key: "c", Path: "/y/c.go", Size: 3},
	} {
		if _, err := s.FileListPut(facade.FileListPutRequest{InstanceID: inst, Entry: e, Scope: scope}); err != nil {
			t.Fatalf("put %s: %v", e.Key, err)
		}
	}

	// 全量：按清单键升序（原 ListKeys+sort 口径），Total = 过滤后总数
	resp, err := s.FileListList(facade.FileListListRequest{InstanceID: inst, Scope: scope})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.Total != 3 || len(resp.List) != 3 || resp.List[0].Key != "a" || resp.List[1].Key != "b" || resp.List[2].Key != "c" {
		t.Fatalf("全量列表不符：total=%d list=%+v", resp.Total, resp.List)
	}

	// 前缀过滤
	pfx, err := s.FileListList(facade.FileListListRequest{InstanceID: inst, Prefix: "/y", Scope: scope})
	if err != nil || pfx.Total != 1 || len(pfx.List) != 1 || pfx.List[0].Key != "c" {
		t.Fatalf("前缀过滤不符：err=%v resp=%+v", err, pfx)
	}

	// 分页（Offset/Limit 不影响 Total）
	page, err := s.FileListList(facade.FileListListRequest{InstanceID: inst, Offset: 1, Limit: 1, Scope: scope})
	if err != nil || page.Total != 3 || len(page.List) != 1 || page.List[0].Key != "b" {
		t.Fatalf("分页不符：err=%v resp=%+v", err, page)
	}

	// 删除：存在 + 不存在混合 → 只计实际删除
	del, err := s.FileListDelete(facade.FileListDeleteRequest{InstanceID: inst, Keys: []string{"a", "missing"}, Scope: scope})
	if err != nil || !del.OK || del.Deleted != 1 {
		t.Fatalf("删除结果不符：err=%v resp=%+v", err, del)
	}
	// 幂等：重复删不报错、Deleted=0
	again, err := s.FileListDelete(facade.FileListDeleteRequest{InstanceID: inst, Keys: []string{"a"}, Scope: scope})
	if err != nil || !again.OK || again.Deleted != 0 {
		t.Fatalf("重复删除应幂等：err=%v resp=%+v", err, again)
	}
	// 终态列表
	last, err := s.FileListList(facade.FileListListRequest{InstanceID: inst, Scope: scope})
	if err != nil || last.Total != 2 {
		t.Fatalf("删除后列表不符：err=%v total=%d", err, last.Total)
	}
}
