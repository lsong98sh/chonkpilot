//go:build !windows

package folder

import "errors"

// PickFolder returns the fallback directory (current working dir) on non-Windows platforms.
// A proper implementation would use Zenity or kdialog for native dialogs.
func PickFolder(title string) (string, error) {
	return "", errors.New("folder picker not implemented on this platform")
}

// PickFile returns not implemented on non-Windows platforms.
func PickFile(title string, filter string) (string, error) {
	return "", errors.New("file picker not implemented on this platform")
}
