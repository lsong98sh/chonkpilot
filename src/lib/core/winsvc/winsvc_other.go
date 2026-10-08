//go:build !windows

// 非 Windows 平台 stub（项目目标平台为 Windows，此文件保证跨平台编译）：
// Windows 服务 API 不可用 → IsWindowsService 恒 false，Run/Install/Remove 返回 ErrUnsupported。
package winsvc

import (
	"context"
	"errors"
)

// ErrUnsupported 本平台不支持 Windows 服务。
var ErrUnsupported = errors.New("winsvc: not supported on this platform")

// Service 描述一个可注册为 Windows 服务的应用（非 Windows：占位类型，仅保证编译）。
type Service struct {
	Name        string
	DisplayName string
	Description string
	RunArgs     []string
	Start       func(ctx context.Context) error
	Stop        func(ctx context.Context) error
}

// IsWindowsService 非 Windows 恒 false。
func IsWindowsService() bool { return false }

// Run 非 Windows 不支持。
func (s *Service) Run() error { return ErrUnsupported }

// Install 非 Windows 不支持。
func (s *Service) Install() error { return ErrUnsupported }

// Remove 非 Windows 不支持。
func (s *Service) Remove() error { return ErrUnsupported }
