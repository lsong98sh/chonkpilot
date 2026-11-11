// 场景 agent 定义与加载类型（25 §8.1 #8 / T6，2026-09-25）。
//
// 2026-11-08：core 的域工具契约已全部迁出/删除（`ask_user` / `next_run` /
// `subchat_open` / `subchat_finish` → chonkpilot-plugins/task；`tool_result` 删除，
// 结果读取统一走 task 插件 `task_status`），本文件不再承载工具契约 embed。
// **agent 不 embed** —— 场景 agent 与出厂场景统一为 app 级场景
// （`<exeDir>/scenarios/<场景>/`，随发布只读资源），经数据层门面读取
// （见 domainmcp.go registerDomainAgents）。
package server

// AgentDef 是 app 级场景内的 agent 定义（来自 `<exeDir>/scenarios/*/` 内的 agent 文件：
// v2 契约「# 基本信息 + # 摘要 + # 正文」，正文 = 该 agent 的提示词）。
type AgentDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}