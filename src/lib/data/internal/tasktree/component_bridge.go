// component_bridge.go —— P4-2 批次 C（2026-10-31）：tasktree 域的组件调用桥。
//
// 存储语义（列表过滤 / 单事务合并写 / 级联软删）归组件（`component.Tasktree*`）；
// DTO 整形（`wire.TaskNodeFromWire` / `facade.Task`）、跨库聚合与 `task-deleted` 广播留本包。
package tasktree

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/storeadapter"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// componentBridge 打开会话组件（存储 = 本模块的 bbolt 适配器；同一 prjusr 路径 → 同源数据）。
func componentBridge(prj *data.DB) (*component.Component, error) {
	return component.New(component.Options{Store: storeadapter.New(prj.Path())})
}
