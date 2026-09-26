// sandbox.go — agentbox 沙箱的 executor 侧强制点（决策 42 §2 (104)/(109)）。
//
// 沙箱策略由宿主（mcp-server spawn executor 时）经环境变量 CHONKPILOT_SANDBOX 注入，
// 本进程在 main 入口 agentbox.InitFromEnv() 装载；本文件提供各文件操作 choke point 使用的
// 判定薄封装。**未启用（无环境变量）= 一律放行**，与引入沙箱前行为等价。
package fileops

import "github.com/chonkpilot/chonkpilot-lib/agentbox"

// sandboxErr 判定单个绝对路径（write=true 需落在可写目录内）；越界返回可诊断错误，放行返回 nil。
// 拒绝时 agentbox 内部记一行 stderr 审计（宿主捕获落日志）。
func sandboxErr(abs string, write bool) error {
	return agentbox.Check(abs, write)
}

// sandboxAllowedRead 报告路径是否允许读（未启用 → true）；供递归遍历剪枝使用。
func sandboxAllowedRead(abs string) bool {
	return agentbox.Current().Allowed(abs, false)
}
