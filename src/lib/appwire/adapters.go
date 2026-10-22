package appwire

import (
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-lib/indexkind"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-codegraph"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
	"github.com/chonkpilot/chonkpilot-plugin-history"
	"github.com/chonkpilot/chonkpilot-plugin-memory"
	"github.com/chonkpilot/chonkpilot-plugin-vfts"
)

// DefaultPlugins 返回内嵌插件的**统一清单**（gui / server 两个入口共用一份，避免漂移）：
// compress（llm-compress 压缩）+ memory（每轮异步沉淀记忆）+ history（git 快照）+
// codegraph / vfts（索引工具面，默认关闭）。
//
// 门面绑定的选择在**装配处**（23 §7）：此处 = inline（同进程直调，bus = 宿主实际总线）。
func DefaultPlugins(f facade.API) []plugin.Hook {
	return []plugin.Hook{
		compress.New(compress.DefaultOptions(), f),
		memory.New(memory.DefaultOptions()),
		history.New(),
		codegraph.New(codegraph.Options{}),
		vfts.New(vfts.Options{}),
	}
}

// indexQueue 把 filesys 的变更事件写入 data 门面待索引队列（P2），
// 与 resident indexer 的消费端（queuehooks）共用同一门面绑定。
func indexQueue(api facade.API) filesys.IndexQueue {
	return filesys.IndexQueueFunc(func(c filesys.IndexChange) error {
		_, err := api.IndexQueueAppend(facade.IndexQueueAppendRequest{
			WorkDir:   c.WorkDir,
			Path:      c.Path,
			Operation: c.Operation,
			Consumers: indexkind.All,
			Scope:     facade.Scope{WorkDir: c.WorkDir},
		})
		return err
	})
}
