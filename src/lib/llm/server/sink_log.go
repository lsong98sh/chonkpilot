package server

import (
	tasklayer "github.com/chonkpilot/chonkpilot-task"
)

// pluginLogf 插件日志入口（统一前缀，便于区分 server 与插件）：经统一出口 logf
// （stdout + 已注入 sink，见 log.go）——插件失败/跳过一类诊断因此可落 gui.log。
// 前缀与收尾换行**逐字沿用改前实现**（插件侧格式串不带 \n）。
func (s *Server) pluginLogf(format string, args ...any) {
	logf("[chonkpilot-server][plugin] "+format+"\n", args...)
}

// taskLayerLogf 任务层告警出口（21 §9.1：层内失败一律只告警）：经统一出口 logf
// （前缀与收尾换行逐字沿用改前实现）。
func taskLayerLogf(format string, args ...any) {
	logf("[chonkpilot-task] "+format+"\n", args...)
}

// onTaskLayerCancelExec 是**层 → 执行侧**取消回调（21 §8.1-6 / §9.2 交付物 ⑤）：
// 任务层判定某节点可取消且持有 exec 句柄时回调此处 → 经**进程内 sink**（`ExecSink.CancelExec`
// → gateway `CancelExec` → `tm.cancel` → onTaskCancel → provider.Invalidate，kill + respawn
// 归属 provider，真实打断在飞调用）。2026-09-18 前此处经 `mcp-tasks-cancel` 方法面，方法面移除后
// 改为同进程直调（不新增 MQ 主题）。层已保证只告警不阻塞主路径，本回调的失败仅记日志
// （取消结果仍由终态事件/gateway 回报收敛）。
func (s *Server) onTaskLayerCancelExec(taskID string, exec tasklayer.Exec) {
	if exec.GWTaskID == "" {
		return
	}
	if s.execSink == nil {
		logf("[chonkpilot-server] 层取消回调无执行池（未注入 sink，task=%s gw_task=%s）→ 跳过\n",
			taskID, exec.GWTaskID)
		return
	}
	if ok, err := s.execSink.CancelExec(exec.InstanceID, exec.GWTaskID); err != nil || !ok {
		logf("[chonkpilot-server] 层取消回调下发失败（task=%s gw_task=%s）: ok=%v err=%v\n",
			taskID, exec.GWTaskID, ok, err)
	} else {
		logf("[chonkpilot-server] 层取消回调下发（task=%s gw_task=%s）\n", taskID, exec.GWTaskID)
	}
}
