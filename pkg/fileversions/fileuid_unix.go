//go:build !windows

package fileversions

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ComputeFileUID computes the file UID using inode (device, inode).
// Format: "u:dev:ino"
func ComputeFileUID(absPath string) FileUID {
	info, err := os.Stat(absPath)
	if err != nil {
		return FileUID(filepath.ToSlash(absPath))
	}

	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileUID(filepath.ToSlash(absPath))
	}

	return FileUID(fmt.Sprintf("u:%x:%x", st.Dev, st.Ino))
}
