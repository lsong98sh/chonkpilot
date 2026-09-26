// 本包统一日志出口（2026-09-20，可诊断性补齐"后端日志进不了 gui.log"）：
//
// 现状问题：本包（装配告警 / gateway 下游装配 / task 层告警 / llm_run 运行时报错等）原先
// 一律 `fmt.Printf` → **stdout**；而 GUI 单体以 `-H windowsgui` 启动（无控制台，stdout 不可见）
// → 这些诊断行**既不落文件也看不见**，`<dataDir>/logs/gui.log`（chonkpilot-gui/logfile.go，
// 只挂了 slog 默认 logger 的 sink）对本包日志形同虚设。
//
// 修法（**最小改动、缺省行为不变**）：全部输出收敛到 `logf` 单一出口 ——
//   - **始终写 stdout**（既有行为逐字节不变：--test-port / 调试重定向捕获仍可用）；
//   - 装配方经 `Options.LogWriter` 注入额外 sink（GUI 单体 = 同一个滚动文件 sink）后，
//     **同一行同时写 sink** → 落 `<dataDir>/logs/gui.log`。
//
// 分层约束：本包是内嵌库，**不得依赖 gui 包**；注入物只有 `io.Writer`（nil = 仅 stdout）。
// 插件侧不直连此处：插件日志经 `plugin.Deps.Logf`（= Server.pluginLogf）转发到同一出口。
package server

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	// logSinkMu 保护 logSink（装配期注入；测试可替换/还原）。
	logSinkMu sync.Mutex
	// logSink 是额外日志 sink（nil = 仅 stdout，即改前行为）。
	logSink io.Writer
)

// setLogSink 注入（或替换）额外日志 sink（nil = 仅 stdout）。
func setLogSink(w io.Writer) {
	logSinkMu.Lock()
	logSink = w
	logSinkMu.Unlock()
}

// logf 是本包（含 task 层告警 / 插件日志转发）的统一日志出口：
// 先写 stdout（与既有 `fmt.Printf` 逐字节一致：本次不做换行/前缀加工），已注入 sink 时同写 sink。
// 单次 Write 一行，加锁保证多 goroutine 下 sink 内不交错（stdout 侧与既有行为一致）。
func logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	logSinkMu.Lock()
	w := logSink
	logSinkMu.Unlock()
	if w != nil {
		_, _ = io.WriteString(w, line) // sink 写失败不影响 stdout（诊断优先）
	}
	_, _ = io.WriteString(os.Stdout, line)
}
