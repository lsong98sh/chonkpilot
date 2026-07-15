//go:build windows

package fileversions

import (
	"fmt"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// ComputeFileUID computes the file UID using NTFS FileIndex.
// Format: "w:volSerial:fileIndexHigh:fileIndexLow"
// Fallback: path if GetFileInformationByHandle fails.
func ComputeFileUID(absPath string) FileUID {
	pathPtr, err := syscall.UTF16PtrFromString(absPath)
	if err != nil {
		return FileUID(filepath.ToSlash(absPath))
	}

	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return FileUID(filepath.ToSlash(absPath))
	}
	defer windows.CloseHandle(handle)

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return FileUID(filepath.ToSlash(absPath))
	}

	return FileUID(fmt.Sprintf("w:%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow))
}
