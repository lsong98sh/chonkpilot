// Scope 是实例的数据根（61 §4.1 的实例绑定事实：work_dir / data_dir）。
//
// 说明：门面**不自己探测路径**、也不把路径规则交给调用方（23 §7：门面不是管道，
// 不交解析器、不交路径规则）。Scope 属例外的一处**事实传递**——调用方（如压缩插件）
// 在 `session-compress` 事件载荷里已经拿到该实例的 work_dir/data_dir，用于本进程
// 尚未登记该实例时自登记（等价于内核既有的"独立服务可凭事件载荷自登记"口径）。
// 常规形态下调用方留空（留空 = 门面按 InstanceID 在数据组件的实例绑定表里解析）。
package facade

// Scope 是实例数据根。
type Scope struct {
	// WorkDir 实例工作目录。
	WorkDir string `json:"work_dir,omitempty"`
	// DataDir 实例数据目录（空 = 缺省 `<work_dir>/.chonkpilot`）。
	DataDir string `json:"data_dir,omitempty"`
}

// Empty 判定是否未提供数据根（未提供 → 门面只按 InstanceID 解析）。
func (s Scope) Empty() bool {
	return s.WorkDir == "" && s.DataDir == ""
}
