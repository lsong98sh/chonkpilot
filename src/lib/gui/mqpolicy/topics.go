package mqpolicy

import "github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"

// 主题级别档案（49 §2.22.7 S2 / 11-MQ §5.1）——**显式清单，不靠前缀猜**。
//
// 三档：
//   - **instance 级**：载荷带 `instance_id`（61 §0 硬规则「业务 payload 一律必带」）→ 沿用 S1 规则，
//     只投该实例的窗口 sink（精确相等；未标识 sink 放行）。
//   - **workdir 级**（下表 `workDirTopics`）：按 work_dir 管理 ⇒ 只投**同 workdir** 的窗口 sink
//     （workdir 未知 → 保守全投）。
//   - **server 级**：其余全部，以及下表 `serverTopics` 的**显式列白**（本可被误收紧而实际必须跨窗）。

// workDirTopics 是 workdir 级主题（S2 收紧的唯一判据）。
//
// 收录依据：
//   - `filesys.changed` / `filesys.watch-error`（61 §2.4）：载荷带 `work_dir`，语义 = **某工作目录内**
//     的文件变化（文件树 / 预览 / VCS 刷新）；`filesys` 的 dirWatcher 在**无 watch 声明者**时
//     **不带 `instance_id`** 直接广播（`src/lib/filesys/watcher.go` 的 `emit`）—— 此时必须由
//     workdir 把关，否则会串到其它项目的窗口。
//   - `data-<domain>-refresh` 的项目级域（61 §3.1）：prj-config / prj-security / prompt / mcp /
//     scenario / memory 的落点都是**项目（work_dir）级**或「项目优先」的四级合并 → 同一 work_dir
//     的窗口共享同一份数据。**当前** persist 带 `instance_id` 逐实例下发（`kernel.conf.go`
//     `refreshTargets`：prj-config 按 `InstancesByWorkDir` 扇出），S1 规则已足够；列入本表是
//     **兜底**：将来若某条漏带 `instance_id`，仍按 workdir 收口，不串项目。
var workDirTopics = map[string]struct{}{
	msgkeys.TopicFilesysChanged:    {},
	msgkeys.TopicFilesysWatchError: {},

	msgkeys.TopicDataPrjConfigRefresh:   {},
	msgkeys.TopicDataPrjSecurityRefresh: {},
	msgkeys.TopicDataPromptRefresh:      {},
	msgkeys.TopicDataMcpRefresh:         {},
	msgkeys.TopicDataScenarioRefresh:    {},
	msgkeys.TopicDataMemoryRefresh:      {},
}

// serverTopics 是**显式列白**的「服务端级广播」：语义上必须跨窗口（含不同 workdir），
// **不得收紧**。`isWorkDirTopic` 先查本表 → 即使将来有人误把它们加进 `workDirTopics`，也不会收紧。
//
//   - `data-user-config-changed`（61 §3.1）：**不带 `instance_id`** 的 usr 配置下行广播 →
//     全局投递；用途 = 多窗口 theme / locale **即时同步**（24 §6.5 · WIN-021；真机用例 MW-T27）。
//   - `data-session-title-changed`（61 §3.2）：**不带 `instance_id`** 的会话标题下行广播 →
//     全局投递；用途 = 主窗改标题 → 已开对话窗口标题跟随（真机用例 MW-T29）。
//   - `data-user-config-refresh` / `config-refresh`（61 §3.1）：usr 级（**跨项目**）配置刷新，
//     按实例下发；不做 workdir 收口，避免「同一用户的其它项目窗口」漏刷新。
var serverTopics = map[string]struct{}{
	msgkeys.TopicDataUserConfigChanged:   {},
	msgkeys.TopicDataSessionTitleChanged: {},
	msgkeys.TopicDataUserConfigRefresh:   {},
	msgkeys.TopicConfigRefresh:           {},
}

// isWorkDirTopic 判定主题是否 workdir 级（server 级白名单优先）。
func isWorkDirTopic(topic string) bool {
	if _, ok := serverTopics[topic]; ok {
		return false
	}
	_, ok := workDirTopics[topic]
	return ok
}
