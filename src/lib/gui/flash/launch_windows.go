//go:build windows

package flash

import (
	"os/exec"
	"path/filepath"
	"syscall"
)

// createNoWindow = CREATE_NO_WINDOW（0x08000000）：不为子进程创建控制台窗口。
const createNoWindow = 0x08000000

// launchDetached 在 Windows 上以「无控制台窗口、独立进程」方式拉起服务端：
// flash 为 windowsgui 宿主（无控制台），若按默认方式拉起 console 版 server 会弹出黑窗，
// 故显式 CREATE_NO_WINDOW + HideWindow。server 以 --autoexit 自行收尾，无需父进程托管。
//
// **cwd = server exe 自身目录**：常驻 server 的默认工作目录（`paths.ResolveDir(cwd,"")`，
// 作 `s.opts.WorkDir` 的启动回落值）应为**其安装目录**，而非继承 flash 的 cwd（flash 的
// cwd 可能为任意位置）→ 避免 server 启动 cwd 随调用方漂移。**注**：capability/mcps 等随附
// 资产由 server 以 **`os.Executable()` 同目录**解析（与 cwd 无关，见 `capfs`），故此处只为
// 固定 server 的启动 cwd，**不是**资产定位。见 62 §3A / 本包 server.go。
func launchDetached(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 不托管子进程（server 独立常驻）：释放句柄，免 Wait/finalizer 延迟回收。
	return cmd.Process.Release()
}
