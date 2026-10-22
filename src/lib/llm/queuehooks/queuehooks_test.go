package queuehooks

import (
	"errors"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

type fakeQueueAPI struct {
	takeReq facade.IndexQueueTakeRequest
	takeRes facade.IndexQueueTakeResponse
	takeErr error

	ackReq facade.IndexQueueAckRequest
	ackRes facade.IndexQueueAckResponse
	ackErr error

	pendReq facade.IndexQueuePendingRequest
	pendRes facade.IndexQueuePendingResponse
	pendErr error
}

func (f *fakeQueueAPI) IndexQueueTake(req facade.IndexQueueTakeRequest) (facade.IndexQueueTakeResponse, error) {
	f.takeReq = req
	return f.takeRes, f.takeErr
}

func (f *fakeQueueAPI) IndexQueueAck(req facade.IndexQueueAckRequest) (facade.IndexQueueAckResponse, error) {
	f.ackReq = req
	return f.ackRes, f.ackErr
}

func (f *fakeQueueAPI) IndexQueuePending(req facade.IndexQueuePendingRequest) (facade.IndexQueuePendingResponse, error) {
	f.pendReq = req
	return f.pendRes, f.pendErr
}

// TestTakeMapsItemsAndScope：字段映射 + `Scope{WorkDir}` 透传。
func TestTakeMapsItemsAndScope(t *testing.T) {
	api := &fakeQueueAPI{takeRes: facade.IndexQueueTakeResponse{
		OK: true,
		Items: []facade.IndexQueueItem{
			{ID: "row-1", WorkDir: "E:/proj", Path: "a.txt", Consumer: "vfts", Operation: "write", State: "pending", Attempts: 2, LastError: "x"},
			{ID: "row-2", WorkDir: "E:/proj", Path: "b.txt", Consumer: "vfts", Operation: "remove", State: "pending"},
		},
	}}
	h := New(api)

	items, err := h.Take("E:/proj", "vfts", 5)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if api.takeReq.WorkDir != "E:/proj" || api.takeReq.Consumer != "vfts" ||
		api.takeReq.Limit != 5 || api.takeReq.Scope.WorkDir != "E:/proj" {
		t.Fatalf("take 请求未透传: %+v", api.takeReq)
	}
	if len(items) != 2 {
		t.Fatalf("items=%v", items)
	}
	got := items[0]
	want := struct {
		id, workDir, path, consumer, operation string
		attempts                               int
	}{"row-1", "E:/proj", "a.txt", "vfts", "write", 2}
	if got.ID != want.id || got.WorkDir != want.workDir || got.Path != want.path ||
		got.Consumer != want.consumer || got.Operation != want.operation || got.Attempts != want.attempts {
		t.Fatalf("字段映射不符: %+v", got)
	}
}

// TestTakeEmptyIsNonNil：无行时返回空切片（避免插件侧 null）。
func TestTakeEmptyIsNonNil(t *testing.T) {
	h := New(&fakeQueueAPI{})
	items, err := h.Take("E:/proj", "", 0)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("items=%v，期望非 nil 空切片", items)
	}
}

// TestAckPassesThrough：回执参数透传 + 更新行数返回。
func TestAckPassesThrough(t *testing.T) {
	api := &fakeQueueAPI{ackRes: facade.IndexQueueAckResponse{OK: true, Updated: 3}}
	h := New(api)

	n, err := h.Ack("C:/work/proj", "codegraph", []string{"row-1", "row-2"}, true, "boom")
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if n != 3 {
		t.Fatalf("updated=%d，期望 3", n)
	}
	if api.ackReq.Consumer != "codegraph" || len(api.ackReq.IDs) != 2 || api.ackReq.IDs[0] != "row-1" ||
		!api.ackReq.Failed || api.ackReq.Error != "boom" ||
		api.ackReq.Scope.WorkDir != "C:/work/proj" {
		t.Fatalf("ack 请求未透传: %+v", api.ackReq)
	}
}

// TestPendingPassesThrough：pending 统计 + scope 透传。
func TestPendingPassesThrough(t *testing.T) {
	api := &fakeQueueAPI{pendRes: facade.IndexQueuePendingResponse{OK: true, Pending: 9}}
	h := New(api)

	n, err := h.Pending("E:/proj", "vfts")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if n != 9 {
		t.Fatalf("pending=%d，期望 9", n)
	}
	if api.pendReq.WorkDir != "E:/proj" || api.pendReq.Consumer != "vfts" || api.pendReq.Scope.WorkDir != "E:/proj" {
		t.Fatalf("pending 请求未透传: %+v", api.pendReq)
	}
}

// TestErrorsPassThrough：门面错误原样透传（插件桥侧再包一层 CodeInternal）。
func TestErrorsPassThrough(t *testing.T) {
	takeErr := errors.New("take boom")
	ackErr := errors.New("ack boom")
	pendErr := errors.New("pending boom")
	h := New(&fakeQueueAPI{takeErr: takeErr, ackErr: ackErr, pendErr: pendErr})

	if _, err := h.Take("E:/proj", "vfts", 1); !errors.Is(err, takeErr) {
		t.Fatalf("take err=%v", err)
	}
	if _, err := h.Ack("C:/work/proj", "vfts", nil, false, ""); !errors.Is(err, ackErr) {
		t.Fatalf("ack err=%v", err)
	}
	if _, err := h.Pending("E:/proj", "vfts"); !errors.Is(err, pendErr) {
		t.Fatalf("pending err=%v", err)
	}
}

// TestNilFacade：未注入门面时三个方法都返回明确错误。
func TestNilFacade(t *testing.T) {
	h := New(nil)

	cases := []struct {
		name string
		fn   func() error
	}{
		{"take", func() error { _, err := h.Take("E:/proj", "vfts", 1); return err }},
		{"ack", func() error { _, err := h.Ack("C:/work/proj", "vfts", nil, false, ""); return err }},
		{"pending", func() error { _, err := h.Pending("E:/proj", "vfts"); return err }},
	}
	for _, c := range cases {
		err := c.fn()
		if err == nil || !strings.Contains(err.Error(), "index queue facade not configured") {
			t.Fatalf("%s 期望未配置错误，实际 %v", c.name, err)
		}
	}
}
