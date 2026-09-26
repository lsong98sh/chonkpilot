//go:build windows

package folder

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modshell32  = windows.NewLazySystemDLL("shell32.dll")
	modole32    = windows.NewLazySystemDLL("ole32.dll")
	modcomdlg32 = windows.NewLazySystemDLL("comdlg32.dll")

	procSHBrowseForFolderW   = modshell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = modshell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree        = modole32.NewProc("CoTaskMemFree")
	procGetOpenFileNameW     = modcomdlg32.NewProc("GetOpenFileNameW")
)

// BROWSEINFO struct for SHBrowseForFolderW
type BROWSEINFOW struct {
	HwndOwner      uintptr
	PidlRoot       uintptr
	PszDisplayName *uint16
	LpszTitle      *uint16
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	IImage         int32
}

const (
	BIF_RETURNONLYFSDIRS = 0x0001
	BIF_NEWDIALOGSTYLE   = 0x0040
)

// OPENFILENAME struct for GetOpenFileNameW
type OPENFILENAMEW struct {
	LStructSize       uint32
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       *uint16
	LpstrCustomFilter *uint16
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         *uint16
	NMaxFile          uint32
	LpstrFileTitle    *uint16
	NMaxFileTitle     uint32
	LpstrInitialDir   *uint16
	LpstrTitle        *uint16
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       *uint16
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    *uint16
	PvReserved        uintptr
	DwReserved        uint32
	FlagsEx           uint32
}

const (
	OFN_FILEMUSTEXIST = 0x00001000
	OFN_HIDEREADONLY  = 0x00000004
	OFN_NOCHANGEDIR   = 0x00000008
	OFN_PATHMUSTEXIST = 0x00000800
)

// PickFile shows a native Windows file open dialog and returns the selected path.
func PickFile(title string, filter string) (string, error) {
	if title == "" {
		title = "Select File"
	}
	if filter == "" {
		filter = "All files\x00*.*\x00\x00"
	}

	var filterBuf []uint16
	for _, r := range filter {
		filterBuf = append(filterBuf, uint16(r))
	}
	if filterBuf[len(filterBuf)-1] != 0 {
		filterBuf = append(filterBuf, 0)
	}
	if filterBuf[len(filterBuf)-1] != 0 {
		filterBuf = append(filterBuf, 0)
	}
	filterPtr := &filterBuf[0]

	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return "", fmt.Errorf("convert title to utf16: %w", err)
	}

	fileBuf := make([]uint16, 260)
	ofn := &OPENFILENAMEW{
		LStructSize: uint32(unsafe.Sizeof(OPENFILENAMEW{})),
		LpstrFilter: filterPtr,
		LpstrFile:   &fileBuf[0],
		NMaxFile:    260,
		LpstrTitle:  titlePtr,
		Flags:       OFN_FILEMUSTEXIST | OFN_HIDEREADONLY | OFN_NOCHANGEDIR | OFN_PATHMUSTEXIST,
	}

	ret, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(ofn)))
	if ret == 0 {
		return "", nil
	}
	return windows.UTF16ToString(fileBuf), nil
}

// PickFolder shows a native Windows folder selection dialog and returns the selected path.
func PickFolder(title string) (string, error) {
	if title == "" {
		title = "Select Project Directory"
	}

	displayBuf := make([]uint16, 260)
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return "", fmt.Errorf("convert title to utf16: %w", err)
	}

	bi := &BROWSEINFOW{
		HwndOwner:      0,
		PidlRoot:       0,
		PszDisplayName: &displayBuf[0],
		LpszTitle:      titlePtr,
		UlFlags:        BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE,
	}

	ret, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(bi)))
	if ret == 0 {
		return "", nil
	}

	pathBuf := make([]uint16, 260)
	procSHGetPathFromIDListW.Call(ret, uintptr(unsafe.Pointer(&pathBuf[0])))
	procCoTaskMemFree.Call(ret)

	return windows.UTF16ToString(pathBuf), nil
}
