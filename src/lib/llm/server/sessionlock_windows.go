//go:build windows

// session 排他锁（对齐用户决策：~/.chonkpilot/locks/{session}.lock）。
// 跨进程互斥（LockFileEx）；进程崩溃由 OS 自动释放；单体/分离两种形态均覆盖。
package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// errSessionBusy session 已被占用（同 session 只能有一个活动 turn）。
var errSessionBusy = errors.New("session busy")

// sessionLocksRoot 返回 session 锁根（usr 数据根 ~/.chonkpilot/locks）。
func sessionLocksRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".chonkpilot", "locks")
}

// sanitizeSessionID 把 session id 里的非法文件名字符替换为 "_"（id 即锁名）。
func sanitizeSessionID(id string) string {
	r := strings.NewReplacer("\\", "_", "/", "_", ":", "_", " ", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return r.Replace(id)
}

// sessionLock 是持有中的 session 排他锁。
type sessionLock struct {
	f *os.File
}

// lockSession 非阻塞获取 session 排他锁：已锁 → errSessionBusy。
// 同 session 只能有一个活动 turn；不同 session 互不阻塞。
// 锁目录取 s.locksDir（默认 ~/.chonkpilot/locks；测试可注入临时目录）。
func (s *Server) lockSession(sessionID string) (*sessionLock, error) {
	dir := s.locksDir
	if dir == "" {
		dir = sessionLocksRoot()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, sanitizeSessionID(sessionID)+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := winLock(f); err != nil {
		_ = f.Close()
		return nil, errSessionBusy
	}
	return &sessionLock{f: f}, nil
}

// Release 释放锁（turn 结束/取消/崩溃时调用；崩溃由 OS 自动释放）。
func (l *sessionLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = winUnlock(l.f)
	_ = l.f.Close()
	l.f = nil
}

func winLock(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
}

func winUnlock(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
