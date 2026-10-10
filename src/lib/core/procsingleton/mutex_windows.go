//go:build windows

package procsingleton

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// Acquire 尝试取得 server 进程单例（命名互斥体）。返回：
//   - duplicate=true → **已有 server 在运行**（本进程应退出；**未持有**句柄）；
//   - 否则 → release 非 nil（进程退出时释放；调用方应 defer 调用）；
//   - 判定失败（如查名转 UTF-16 失败）→ err 非 nil（调用方按**非单例**继续，仅告警）。
//
// 注：`x/sys/windows` 的 `CreateMutex` 在 `ERROR_ALREADY_EXISTS` 时**返回有效句柄 + 该错误**，
// 故据此判重；`Local\` 前缀 + 用户名哈希 = 每登录会话内按当前用户隔离。
func Acquire(user string) (release func(), duplicate bool, err error) {
	name := Name(user)
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, fmt.Errorf("procsingleton: utf16(%s): %w", name, err)
	}
	h, err := windows.CreateMutex(nil, false, p)
	if ShouldExitAsDuplicate(errors.Is(err, windows.ERROR_ALREADY_EXISTS)) {
		_ = windows.CloseHandle(h)
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("procsingleton: CreateMutexW(%s): %w", name, err)
	}
	return func() { _ = windows.CloseHandle(h) }, false, nil
}
