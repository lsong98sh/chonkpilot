//go:build windows

// Package winsvc 提供跨应用复用的 Windows 服务封装：
// 注册/删除/运行（sc + svc.Run），服务启停桥接到应用的 Start/Stop 回调。
package winsvc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"

	"github.com/chonkpilot/chonkpilot-lib/winproc"
)

// Service 描述一个可注册为 Windows 服务的应用。
type Service struct {
	Name        string // 服务名（SCM 注册名）
	DisplayName string
	Description string
	RunArgs     []string                        // 服务启动参数（缺省 ["--service","run"]）；SCM 拉起时拼在 exe 后
	Start       func(ctx context.Context) error // 启动（阻塞直到服务退出）
	Stop        func(ctx context.Context) error // 优雅停止
}

// runArgs 返回服务进程参数（缺省 --service run）。
func (s *Service) runArgs() []string {
	if len(s.RunArgs) > 0 {
		return s.RunArgs
	}
	return []string{"--service", "run"}
}

// IsWindowsService 报告当前进程是否由 SCM 启动。
func IsWindowsService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}

// Run 以服务身份运行（阻塞，与 SCM 通信）。
func (s *Service) Run() error {
	return svc.Run(s.Name, &handler{s: s})
}

// Install 注册服务并注册事件日志源（需管理员权限）。
func (s *Service) Install() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve exe: %w", err)
	}
	exe = filepath.Clean(exe)
	// sc create：binPath = "<exe> <runArgs...>"（应用侧按 --service run 进入服务模式）
	cmd := exec.Command("sc", "create", s.Name,
		"binPath=", fmt.Sprintf(`"%s" %s`, exe, strings.Join(s.runArgs(), " ")),
		"start=", "auto",
		"DisplayName=", s.DisplayName,
		"Description=", s.Description)
	cmd.SysProcAttr = winproc.SysProcAttr()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sc create: %v: %s", err, out)
	}
	// 事件日志源（write 模式），供 winlog 写服务日志
	if err := eventlog.InstallAsEventCreate(s.Name, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		return fmt.Errorf("eventlog install: %v", err)
	}
	return nil
}

// Remove 删除服务并移除事件日志源（需管理员权限）。
func (s *Service) Remove() error {
	cmd := exec.Command("sc", "delete", s.Name)
	cmd.SysProcAttr = winproc.SysProcAttr()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sc delete: %v: %s", err, out)
	}
	eventlog.Remove(s.Name)
	return nil
}

// handler 实现 svc.Handler：服务状态机桥接 Start/Stop 回调。
type handler struct {
	s *Service
}

func (h *handler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	errCh := make(chan error, 1)
	go func() { errCh <- h.s.Start(context.Background()) }()
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := h.s.Stop(ctx)
				cancel()
				changes <- svc.Status{State: svc.Stopped}
				return false, errCode(err)
			}
		case err := <-errCh:
			return false, errCode(err)
		}
	}
}

func errCode(err error) uint32 {
	if err != nil {
		return 1
	}
	return 0
}
