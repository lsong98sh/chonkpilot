//go:build !windows

// 非 Windows 平台 stub（项目目标平台为 Windows，此文件保证跨平台编译）。
package server

import "errors"

var errSessionBusy = errors.New("session busy")

type sessionLock struct{}

func (s *Server) lockSession(_ string) (*sessionLock, error) {
	return nil, errors.New("session lock not supported on this platform")
}

func (l *sessionLock) Release() {}
