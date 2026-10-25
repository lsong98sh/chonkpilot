package bridge

// 待索引队列控制帧（插件传输帧，**不是** 61 消息一览里的 MQ 主题）。
//
// resident indexer 插件（P4b）通过本组控制帧向宿主取队列 / 回执 / 查余额：
//
//	plugin.index.take    入参 {work_dir, consumer, limit}       → {ok:true, items:[{id,work_dir,path,consumer,operation,attempts}]}
//	plugin.index.ack     入参 {work_dir, consumer, ids:[]string, failed:bool, error} → {ok:true, updated:N}
//	plugin.index.pending 入参 {work_dir, consumer}              → {ok:true, pending:N}
//	plugin.index.workdirs 入参 {}                               → {ok:true, work_dirs:["<绝对目录>", ...]}
//
// consumer 是扇出维度（resident 索引引擎标识：vfts / codegraph）；空 = 不按 consumer 过滤
// （兼容旧版 indexer）。前三个均要求已发送 plugin.hello；宿主未装配 IndexQueueHooks 时返回
// `index queue not configured`（CodeInternal）。
//
// `plugin.index.workdirs`（2026-10-19，单实例多 workdir）返回宿主当前**活实例**的
// 去重 work_dir —— 队列库按项目隔离（`Scope{WorkDir}` → `<workdir>/.chonkpilot`），
// 单进程 indexer 无法自行枚举磁盘上的项目，故由宿主实例表提供发现入口；未装配
// `Options.WorkDirs` → 返回空列表（插件空转等待）。

import (
	"context"
	"encoding/json"
)

// IndexQueueItem 是插件侧看到的待索引行。
type IndexQueueItem struct {
	ID        string `json:"id"`
	WorkDir   string `json:"work_dir"`
	Path      string `json:"path"`
	Consumer  string `json:"consumer,omitempty"`
	Operation string `json:"operation"`
	Attempts  int    `json:"attempts"`
}

// IndexQueueHooks 由宿主（server）实现，桥接到 data 门面。
type IndexQueueHooks interface {
	Take(workDir, consumer string, limit int) ([]IndexQueueItem, error)
	Ack(workDir, consumer string, ids []string, failed bool, errMsg string) (int, error)
	Pending(workDir, consumer string) (int, error)
}

func handleIndexTake(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	hooks := s.srv.opts.IndexQueue
	if hooks == nil {
		return nil, errf(CodeInternal, "index queue not configured")
	}
	var in struct {
		WorkDir  string `json:"work_dir"`
		Consumer string `json:"consumer"`
		Limit    int    `json:"limit"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	items, err := hooks.Take(in.WorkDir, in.Consumer, in.Limit)
	if err != nil {
		return nil, errf(CodeInternal, "index queue take failed: %v", err)
	}
	if items == nil {
		items = []IndexQueueItem{}
	}
	return map[string]any{"ok": true, "items": items}, nil
}

func handleIndexAck(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	hooks := s.srv.opts.IndexQueue
	if hooks == nil {
		return nil, errf(CodeInternal, "index queue not configured")
	}
	var in struct {
		WorkDir  string   `json:"work_dir"`
		Consumer string   `json:"consumer"`
		IDs      []string `json:"ids"`
		Failed   bool     `json:"failed"`
		Error    string   `json:"error"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	n, err := hooks.Ack(in.WorkDir, in.Consumer, in.IDs, in.Failed, in.Error)
	if err != nil {
		return nil, errf(CodeInternal, "index queue ack failed: %v", err)
	}
	return map[string]any{"ok": true, "updated": n}, nil
}

func handleIndexPending(_ context.Context, s *Session, args json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	hooks := s.srv.opts.IndexQueue
	if hooks == nil {
		return nil, errf(CodeInternal, "index queue not configured")
	}
	var in struct {
		WorkDir  string `json:"work_dir"`
		Consumer string `json:"consumer"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	n, err := hooks.Pending(in.WorkDir, in.Consumer)
	if err != nil {
		return nil, errf(CodeInternal, "index queue pending failed: %v", err)
	}
	return map[string]any{"ok": true, "pending": n}, nil
}

// handleIndexWorkDirs 返回宿主当前活实例的去重 work_dir（单实例多 workdir 发现入口）。
func handleIndexWorkDirs(_ context.Context, s *Session, _ json.RawMessage) (any, error) {
	if s.PluginID() == "" {
		return nil, errf(CodePluginUnregistered, "请先发送 plugin.hello")
	}
	fn := s.srv.opts.WorkDirs
	if fn == nil {
		return map[string]any{"ok": true, "work_dirs": []string{}}, nil
	}
	dirs := fn()
	if dirs == nil {
		dirs = []string{}
	}
	return map[string]any{"ok": true, "work_dirs": dirs}, nil
}
