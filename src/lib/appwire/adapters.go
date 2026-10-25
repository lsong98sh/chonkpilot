package appwire

import (
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-lib/indexkind"
	"github.com/chonkpilot/chonkpilot-plugin-codegraph"
	"github.com/chonkpilot/chonkpilot-plugin-vfts"
	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
)

// DefaultPlugins 返回内嵌插件的**统一清单**（gui / server 两个入口共用一份，避免漂移）：
// codegraph / vfts（索引工具面，默认关闭）。
//
// memory（每轮异步沉淀记忆）与 history（文件检查点链）**不再是内嵌插件**（2026-10-24）：
// 两者改为独立 exe 插件（`src/plugins/memory`、`src/plugins/history`，manifest 见
// `src/initdata/capability/plugins/<id>/hook.json`），业务代码仍复用
// `chonkpilot-plugin-memory` / `chonkpilot-plugin-history` 包，行为/数据口径/消息面不变。
//
// 压缩不再作为内嵌插件：SESSION-1（2026-10-23）起**压缩内建**在会话侧
// （`chonkpilot-llm/server` 的 `compress.go`：组装后按 Usage 判阈值 → 后台复用刚发出的 payload
// → CAS 回填快照）；`chonkpilot-plugin-compress` 包仅作为纯算法库保留（单测在用）。
//
// 门面绑定的选择在**装配处**（23 §7）：此处 = inline（同进程直调，bus = 宿主实际总线）。
func DefaultPlugins(f facade.API) []hook.Hook {
	return []hook.Hook{
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
