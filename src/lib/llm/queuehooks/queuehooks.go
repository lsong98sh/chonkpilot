// Package queuehooks 把 data 门面的待索引队列适配为 pluginbridge.IndexQueueHooks。
//
// resident indexer 插件经插件桥控制帧（plugin.index.take / ack / pending）访问本适配器，
// 适配器再以 `Scope{WorkDir: workDir}` 直调同进程 data 门面（inline / persist 均满足
// QueueAPI）。本包**不新增 MQ 主题 / msgkeys**，只是门面 → 插件桥的形态适配。
package queuehooks

import (
	"errors"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-llm/pluginbridge"
)

// errNotConfigured 是宿主未注入门面时的明确错误（插件侧最终表现为 `index queue ... failed`）。
var errNotConfigured = errors.New("index queue facade not configured")

// QueueAPI 是适配器所需的最小门面面；`*persist.Service` 与 inline 绑定（facade.API）
// 均满足（它们实现了完整 facade.API，含 IndexQueueAPI）。
type QueueAPI interface {
	IndexQueueTake(facade.IndexQueueTakeRequest) (facade.IndexQueueTakeResponse, error)
	IndexQueueAck(facade.IndexQueueAckRequest) (facade.IndexQueueAckResponse, error)
	IndexQueuePending(facade.IndexQueuePendingRequest) (facade.IndexQueuePendingResponse, error)
}

// Hooks 是 pluginbridge.IndexQueueHooks 的门面实现。
type Hooks struct {
	api QueueAPI
}

// New 构造适配器；api 为 nil 时各方法返回 `index queue facade not configured`。
func New(api QueueAPI) *Hooks {
	return &Hooks{api: api}
}

// Take 取一批待索引行（门面语义：不修改状态，至少一次）。
//
// consumer 是扇出维度（vfts / codegraph）；空 = 不按 consumer 过滤。
func (h *Hooks) Take(workDir string, consumer string, limit int) ([]pluginbridge.IndexQueueItem, error) {
	if h == nil || h.api == nil {
		return nil, errNotConfigured
	}
	res, err := h.api.IndexQueueTake(facade.IndexQueueTakeRequest{
		WorkDir:  workDir,
		Consumer: consumer,
		Limit:    limit,
		Scope:    facade.Scope{WorkDir: workDir},
	})
	if err != nil {
		return nil, err
	}
	items := make([]pluginbridge.IndexQueueItem, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, pluginbridge.IndexQueueItem{
			ID:        it.ID,
			WorkDir:   it.WorkDir,
			Path:      it.Path,
			Consumer:  it.Consumer,
			Operation: it.Operation,
			Attempts:  it.Attempts,
		})
	}
	return items, nil
}

// Ack 回执一批已消费行（成功 → done；失败 → attempts+1，累计 3 次转 failed）。
//
// work_dir 用于定位 prjusr 库（队列按项目隔离）；不得留空。
// consumer 非空 → 只回执本 consumer 的行与 legacy 空行。
func (h *Hooks) Ack(workDir string, consumer string, ids []string, failed bool, errMsg string) (int, error) {
	if h == nil || h.api == nil {
		return 0, errNotConfigured
	}
	res, err := h.api.IndexQueueAck(facade.IndexQueueAckRequest{
		Consumer: consumer,
		IDs:      ids,
		Failed:   failed,
		Error:    errMsg,
		Scope:    facade.Scope{WorkDir: workDir},
	})
	if err != nil {
		return 0, err
	}
	return res.Updated, nil
}

// Pending 统计某项目的 pending 行数（consumer 过滤语义同 Take）。
func (h *Hooks) Pending(workDir string, consumer string) (int, error) {
	if h == nil || h.api == nil {
		return 0, errNotConfigured
	}
	res, err := h.api.IndexQueuePending(facade.IndexQueuePendingRequest{
		WorkDir:  workDir,
		Consumer: consumer,
		Scope:    facade.Scope{WorkDir: workDir},
	})
	if err != nil {
		return 0, err
	}
	return res.Pending, nil
}
