package mqpolicy

import "github.com/chonkpilot/chonkpilot-workspace"

// 主题级别档案（49 §2.22.7 S2 / 11-MQ §5.1）——**显式清单，不靠前缀猜**。
//
// 三档（详尽收录依据见 `workspace/router.go`，唯一实现已收归 PM / P5 步骤 3）：
//   - **instance 级**：载荷带 `instance_id` → 只投该实例的 sink（精确相等；未标识放行）。
//   - **workdir 级**（`workDirTopics`）：按 work_dir 管理 ⇒ 只投**同 workdir** 的 sink
//     （workdir 未知 → 保守全投）。
//   - **server 级**：其余全部，以及 `serverTopics` 的**显式列白**（必须跨窗）。

// workDirTopics / serverTopics 是 PM 主题清单的**只读别名**（本包不再维护副本）。
var (
	workDirTopics = workspace.WorkDirTopics
	serverTopics  = workspace.ServerTopics
)

// isWorkDirTopic 判定主题是否 workdir 级（server 级白名单优先；转发 PM）。
func isWorkDirTopic(topic string) bool { return workspace.IsWorkDirTopic(topic) }
