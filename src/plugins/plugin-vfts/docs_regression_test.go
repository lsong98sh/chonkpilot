// 文档索引「停服降级」清单一致性回归（配合引擎侧 collectFiles 的整批跳过门）：
// 转换服务不可用 → 引擎未索引文档类（应答 indexed 不含文档）→ 清单重建必须**以引擎实际
// Indexed 为准**（keep 只含已索引项），把上次遗留的文档类行从 file_list 移除，而不是保留。
package vfts

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// fileListReq 冒充 persist 的 file_list 请求载荷（已带 ok 的应答消息一律跳过，避免自回环）。
type fileListReq struct {
	OK    *bool          `json:"ok"`
	ReqID string         `json:"req_id"`
	Data  map[string]any `json:"data"`
}

// parseFileListReq 解析请求；非请求（应答/无法解析/无 req_id）→ ok=false。
func parseFileListReq(v *mq.Value) (fileListReq, bool) {
	var req fileListReq
	if err := json.Unmarshal(v.Payload, &req); err != nil || req.OK != nil || req.ReqID == "" {
		return req, false
	}
	return req, true
}

// newStubFileListBus 冒充 persist 的 file_list 数据面（list/put/del），内存持有记录，
// 并返回当前快照读取函数。语义对齐 persist 的请求-应答（同主题 + req_id 关联 + ok/result）。
func newStubFileListBus(t *testing.T, initial []fileRec) (mq.Bus, func() map[string]fileRec) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	var mu sync.Mutex
	store := map[string]fileRec{}
	for _, r := range initial {
		store[r.Key] = r
	}
	reply := func(subject, reqID string, result map[string]any) {
		go func() {
			_ = bus.Emit(context.Background(), subject, map[string]any{
				"req_id": reqID, "ok": true, "result": result,
			})
		}()
	}
	onList := func(_ context.Context, _ string, v *mq.Value) error {
		req, ok := parseFileListReq(v)
		if !ok {
			return nil
		}
		mu.Lock()
		list := make([]any, 0, len(store))
		for _, r := range store {
			b, _ := json.Marshal(r)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			list = append(list, m)
		}
		mu.Unlock()
		reply(subjectFileListList, req.ReqID, map[string]any{"list": list})
		return nil
	}
	onPut := func(_ context.Context, _ string, v *mq.Value) error {
		req, ok := parseFileListReq(v)
		if !ok {
			return nil
		}
		var recs []fileRec
		// 批量形态：data.entries:[…]；单条形态：data 平铺字段。
		if entries, ok := req.Data["entries"].([]any); ok {
			for _, e := range entries {
				b, _ := json.Marshal(e)
				var rec fileRec
				if json.Unmarshal(b, &rec) == nil && rec.Key != "" {
					recs = append(recs, rec)
				}
			}
		} else {
			b, _ := json.Marshal(req.Data)
			var rec fileRec
			if json.Unmarshal(b, &rec) == nil && rec.Key != "" {
				recs = append(recs, rec)
			}
		}
		mu.Lock()
		for _, rec := range recs {
			store[rec.Key] = rec
		}
		mu.Unlock()
		reply(subjectFileListPut, req.ReqID, map[string]any{})
		return nil
	}
	onDel := func(_ context.Context, _ string, v *mq.Value) error {
		req, ok := parseFileListReq(v)
		if !ok {
			return nil
		}
		if keys, ok := req.Data["keys"].([]any); ok {
			mu.Lock()
			for _, k := range keys {
				if s, ok := k.(string); ok {
					delete(store, s)
				}
			}
			mu.Unlock()
		}
		reply(subjectFileListDel, req.ReqID, map[string]any{})
		return nil
	}
	subs := make([]mq.Sub, 0, 3)
	for subject, h := range map[string]mq.VHandler{
		subjectFileListList: onList,
		subjectFileListPut:  onPut,
		subjectFileListDel:  onDel,
	} {
		sub, err := bus.On(subject, 0, h)
		if err != nil {
			t.Fatalf("subscribe %s: %v", subject, err)
		}
		subs = append(subs, sub)
	}
	t.Cleanup(func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
		_ = bus.Close()
	})
	snapshot := func() map[string]fileRec {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]fileRec, len(store))
		for k, v := range store {
			out[k] = v
		}
		return out
	}
	return bus, snapshot
}

// TestRebuildManifestDropsUnindexedDocs：服务不可用（docScanCtx.enabled=false，与引擎
// 「整批跳过」同口径）→ 引擎应答 Indexed 不含文档类 → 清单重建把**上次遗留的文档类行移除**，
// 非文档类保留。即清单保留集严格以引擎实际 Indexed 为准。
func TestRebuildManifestDropsUnindexedDocs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "note.txt", "plain note")
	writeTestFile(t, dir, "报告.docx", "fake docx")

	noteAbs := filepath.ToSlash(filepath.Join(dir, "note.txt"))
	docAbs := filepath.ToSlash(filepath.Join(dir, "报告.docx"))
	noteKey, docKey := keyOf(noteAbs), keyOf(docAbs)

	bus, snapshot := newStubFileListBus(t, []fileRec{
		{Key: noteKey, Path: noteAbs, Size: 10, MTime: "t0", MD5: "aaa", DocIDs: []string{"1"}, Chunks: 1},
		{Key: docKey, Path: docAbs, Size: 9, MTime: "t0", MD5: "bbb@v1", DocIDs: []string{"2"}, Chunks: 1},
	})

	p := New(Options{Exe: "dummy-not-spawned.exe"})
	p.logf = func(string, ...any) {}
	p.deps.Bus = bus
	wd := filepath.Clean(dir)
	p.insts["i1"] = &instRec{workdir: wd}
	r := &workRec{workDir: wd, refs: 1, enabled: true, docs: true}

	// 引擎应答只含 note.txt（服务不可用 → 文档类未索引）
	res := engineIndexResult{Mode: "full", Indexed: []engineIndexedFile{
		{Path: "note.txt", Key: noteKey, DocIDs: []string{"1"}, Chunks: 1},
	}}
	if err := p.rebuildManifest(r, res, []string{".txt"}, nil, false, docScanCtx{enabled: false}); err != nil {
		t.Fatalf("rebuildManifest: %v", err)
	}
	got := snapshot()
	if _, ok := got[docKey]; ok {
		t.Fatalf("服务不可用：未索引的文档类应从 file_list 移除，实际仍在：%+v", got[docKey])
	}
	if _, ok := got[noteKey]; !ok {
		t.Fatalf("非文档类应保留在清单：%+v", got)
	}
}
