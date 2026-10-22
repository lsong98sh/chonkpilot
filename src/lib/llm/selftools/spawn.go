// Package selftools 启动/收尾系统级 app 工具（filesys_run / script_run / web_fetch /
// browser_run / desktop_run）的进程外实现（chonkpilot-self-tools）。
//
// P6（2026-10-19）：宿主进程不再把 `<exeDir>/capability` 的可执行工具注册进内嵌 mcp-server；
// 本包只负责「随宿主生命周期托管该 exe」——工具定义与执行由子进程经插件桥注册/回调完成。
package selftools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/chonkpilot/chonkpilot-lib/exedir"
)

// 宿主注入给 self-tools 的环境变量（与 cmd/self-tools main 的读取键一致）。
const (
	EnvAddr     = "CHONKPILOT_PLUGIN_ADDR"
	EnvPluginID = "CHONKPILOT_PLUGIN_ID"
	EnvRoot     = "CHONKPILOT_CAPABILITY_ROOT"
)

// PluginID 是本进程在插件桥上的身份（与 cmd/self-tools 缺省值一致）。
const PluginID = "self-tools"

// Spawn 启动 chonkpilot-self-tools[.exe]：
//   - 优先宿主 exe 同目录；
//   - 否则新布局 `<exeDir>/mcps/self-tools/`。
//
// output = 子进程 stdout/stderr 的落点（nil = os.Stderr）；windowsgui 宿主应传入自己的日志 sink。
func Spawn(addr, capabilityRoot string, output io.Writer) (*exec.Cmd, error) {
	if addr == "" {
		return nil, errors.New("self-tools: 插件桥地址为空")
	}
	if capabilityRoot == "" {
		return nil, errors.New("self-tools: capability 根为空")
	}
	dir, err := exedir.Dir()
	if err != nil {
		return nil, fmt.Errorf("self-tools: exe 目录: %w", err)
	}
	name := "chonkpilot-self-tools"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := findExe(dir, name)
	if path == "" {
		return nil, fmt.Errorf("self-tools: 未找到 %s（查找 %s 与 %s）",
			name, dir, filepath.Join(dir, "mcps", "self-tools"))
	}
	if output == nil {
		output = os.Stderr
	}
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(),
		EnvAddr+"="+addr,
		EnvPluginID+"="+PluginID,
		EnvRoot+"="+capabilityRoot,
	)
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = sysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("self-tools: start %s: %w", path, err)
	}
	return cmd, nil
}

// findExe 返回可执行文件路径（同目录优先，其次 mcps/self-tools）；均不存在 → 空。
func findExe(dir, name string) string {
	for _, p := range []string{
		filepath.Join(dir, name),
		filepath.Join(dir, "mcps", "self-tools", name),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Stop 终止子进程（幂等；未启动/已退出均安全）。
func Stop(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
