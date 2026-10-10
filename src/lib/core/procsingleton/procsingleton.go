// Package procsingleton 提供「进程级单例」判定（🆕 2026-10-10；19 §8.11 / 62 §3A）。
//
// 用途：`chonkpilot-server.exe` **全局一份** —— 第二份**立即退出**（exit code 非 0），
// **不得**触碰 / 覆盖既有 UDS socket，**不得**启动第二份服务。
//
// 判据 = Windows **命名互斥体**（`CreateMutexW`，名字按当前用户隔离）。**不再**用
// 「UDS bind 失败」判定 —— `uds.Server.Start()` 先 `os.Remove` 旧 socket 再
// `net.Listen`，Windows 下同一路径**重复 bind 不失败**（见 `uds` 包注释），旧口径不成立。
//
// 本文件的逻辑与平台无关（可单测）；系统调用在 `mutex_windows.go` / `mutex_other.go`。
package procsingleton

import (
	"hash/fnv"
	"strconv"
	"strings"
)

// namePrefix 是单例互斥体名前缀：`Global\` 命名空间 = **跨登录会话全局**（R7-B-3：
// `Local\` 下同用户的两个登录会话各得一份互斥体，第二份 server 会成功启动并顶掉第一份的
// UDS socket）；后缀 = 用户稳定哈希（按当前用户隔离，故不同用户仍互不冲突）。
const namePrefix = `Global\ChonkPilotServer_`

// Name 返回 server 进程单例的命名互斥体名（同 user → 同名；跨进程稳定）。
// user 为空 → 归一为空串（仍得确定名，作「未取到用户」的兜底单例，不 panic）。
func Name(user string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(user))))
	return namePrefix + strconv.FormatUint(h.Sum64(), 16)
}

// ShouldExitAsDuplicate 判定：命名互斥体**已存在**（`CreateMutexW` 返回
// `ERROR_ALREADY_EXISTS`）→ 本进程为第二实例 → 应退出。
func ShouldExitAsDuplicate(alreadyExists bool) bool {
	return alreadyExists
}
