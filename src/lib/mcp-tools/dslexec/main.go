// chonkpilot-dsl-executor：DSL 统一编排的执行体（独立受保护进程，stdio 四协议）。
//
// 生命周期：一作业一进程。启动 → 读首行 run → 建引擎执行（LLM 步骤经 llm_call/llm_result
// 回 gateway 执行）→ 写 result → 退出。传输 = 行分隔 JSON（见 protocol.go）；stdout 只出协议行，
// 进程级诊断错误走 stderr。
//
// 动作方言：文件域 FILE_*、浏览器域 WEB_*、桌面域 PC_*（域前缀消歧三域重名动词），LLM 保持。
// 沙箱：宿主 spawn 时经 CHONKPILOT_SANDBOX 注入策略 → agentbox.InitFromEnv 进程级强制。
// 决策见 42 §2 (247)–(252) / 49 §2 DSL-1/DSL-2。
package main

import (
	"fmt"
	"os"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
)

func main() {
	agentbox.InitFromEnv() // 进程级沙箱策略（未设置 = 不启用隔离）
	if err := runProtocol(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "[dsl-executor] "+err.Error())
		os.Exit(1)
	}
}
