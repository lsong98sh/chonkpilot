package winlog

import "time"

// nowPrefix 生成当前时刻的日志前缀（每条日志实时求值，避免固定为进程启动时刻）。
func nowPrefix() string {
	return time.Now().Format("2006/01/02 15:04:05") + " "
}
