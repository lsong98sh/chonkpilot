//go:build windows

package recent

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockFile(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var overlapped windows.Overlapped
	return windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped)
}

func unlockFile(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
}

func isLocked(path string) bool {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return true
	}
	defer f.Close()
	handle := windows.Handle(f.Fd())
	var overlapped windows.Overlapped
	err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if err != nil {
		return true
	}
	windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
	return false
}
