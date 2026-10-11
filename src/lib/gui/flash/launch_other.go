//go:build !windows

package flash

import "os/exec"

// launchDetached 是非 Windows 平台的默认拉起实现（无控制台窗口问题）。
// env = 子进程环境（nil = 继承父环境）；非空时含 `CHONK_UDS` 自定义 UDS 下传（见 serverEnv）。
func launchDetached(exe string, args []string, env []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return err
	}
	// 不托管子进程（server 独立常驻）：释放句柄，免 Wait/finalizer 延迟回收。
	return cmd.Process.Release()
}
