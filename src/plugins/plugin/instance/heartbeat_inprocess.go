//go:build !split

package instance

import "github.com/chonkpilot/chonkpilot-lib/mq"

// StartHeartbeat 合并单进程形态（**默认构建**）：不发布心跳——实例随进程生命周期结束，
// 失效只走显式 instance-exit（用户 2026-09-16 口径，见 42 §2 (69)(72)）。
// 分离形态实现（周期发布 + 超时清理）见 heartbeat_split.go（`-tags split`）。
func StartHeartbeat(mq.Bus, string, string) func() { return func() {} }
