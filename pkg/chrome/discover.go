// Package chrome provides Chrome browser discovery utilities.
package chrome

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Result holds the result of Chrome discovery.
type Result struct {
	Path string `json:"path"` // empty if not found
	Ok   bool   `json:"ok"`
}

// Discover searches for a Chrome/Chromium/Edge installation.
// Returns the first executable found, or empty string if none found.
func Discover() Result {
	candidates := []string{
		"chrome.exe",
		"chromium.exe",
		"msedge.exe",
		"brave.exe",
		"opera.exe",
		"vivaldi.exe",
	}

	// 1. Check PATH
	for _, name := range candidates {
		if p, err := exec.LookPath(name); err == nil {
			return Result{Path: p, Ok: true}
		}
	}

	// 2. Windows registry: App Paths for Chrome
	chromeKeyPaths := []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`,
		`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`,
	}
	for _, keyPath := range chromeKeyPaths {
		if p := readRegDefaultValue(keyPath); p != "" {
			if _, err := os.Stat(p); err == nil {
				return Result{Path: p, Ok: true}
			}
		}
	}

	// 3. Chrome uninstall key (InstallLocation)
	uninstallKeys := []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Google Chrome`,
		`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\Google Chrome`,
	}
	for _, keyPath := range uninstallKeys {
		if p := readRegStringValue(keyPath, "InstallLocation"); p != "" {
			chromePath := filepath.Join(p, "chrome.exe")
			if _, err := os.Stat(chromePath); err == nil {
				return Result{Path: chromePath, Ok: true}
			}
		}
	}

	// 4. Microsoft Edge registry
	edgeKeyPaths := []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`,
		`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`,
	}
	for _, keyPath := range edgeKeyPaths {
		if p := readRegDefaultValue(keyPath); p != "" {
			if _, err := os.Stat(p); err == nil {
				return Result{Path: p, Ok: true}
			}
		}
	}

	// 5. Common install paths
	localAppData := os.Getenv("LOCALAPPDATA")
	progFiles := os.Getenv("PROGRAMFILES")
	progFiles86 := os.Getenv("ProgramFiles(x86)")
	commonPaths := []string{
		// Chrome
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		localAppData + `\Google\Chrome\Application\chrome.exe`,
		// Chromium
		`C:\Program Files\Chromium\Application\chrome.exe`,
		localAppData + `\Chromium\Application\chrome.exe`,
		// Edge
		progFiles86 + `\Microsoft\Edge\Application\msedge.exe`,
		progFiles + `\Microsoft\Edge\Application\msedge.exe`,
		// Brave
		localAppData + `\BraveSoftware\Brave-Browser\Application\brave.exe`,
	}
	for _, p := range commonPaths {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return Result{Path: p, Ok: true}
			}
		}
	}

	return Result{Ok: false}
}

// Verify checks if the Chrome path is still valid.
func Verify(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// chromeNotFoundError creates an error message about missing Chrome.
func chromeNotFoundError() string {
	return fmt.Sprintf("Chrome/Chromium browser not found. %s",
		"Please install Google Chrome or Microsoft Edge.")
}

// readRegDefaultValue reads the default (unnamed) value of a registry key.
func readRegDefaultValue(keyPath string) string {
	var h windows.Handle
	keyUTF16, err := windows.UTF16PtrFromString(keyPath)
	if err != nil {
		return ""
	}
	err = windows.RegOpenKeyEx(windows.HKEY_LOCAL_MACHINE, keyUTF16, 0, windows.KEY_READ, &h)
	if err != nil {
		return ""
	}
	defer windows.RegCloseKey(h)

	var bufSize uint32 = 4096
	buf := make([]uint16, bufSize/2)
	var valType uint32
	err = windows.RegQueryValueEx(h, nil, nil, &valType,
		(*byte)(unsafe.Pointer(&buf[0])), &bufSize)
	if err != nil {
		return ""
	}
	if valType != windows.REG_SZ && valType != windows.REG_EXPAND_SZ {
		return ""
	}
	return windows.UTF16ToString(buf[:bufSize/2])
}

// readRegStringValue reads a named string value from a registry key.
func readRegStringValue(keyPath, valueName string) string {
	var h windows.Handle
	keyUTF16, err := windows.UTF16PtrFromString(keyPath)
	if err != nil {
		return ""
	}
	err = windows.RegOpenKeyEx(windows.HKEY_LOCAL_MACHINE, keyUTF16, 0, windows.KEY_READ, &h)
	if err != nil {
		return ""
	}
	defer windows.RegCloseKey(h)

	var bufSize uint32 = 4096
	buf := make([]uint16, bufSize/2)
	vNameUTF16, err := windows.UTF16PtrFromString(valueName)
	if err != nil {
		return ""
	}
	var valType uint32
	err = windows.RegQueryValueEx(h, vNameUTF16, nil, &valType,
		(*byte)(unsafe.Pointer(&buf[0])), &bufSize)
	if err != nil {
		return ""
	}
	if valType != windows.REG_SZ && valType != windows.REG_EXPAND_SZ {
		return ""
	}
	return windows.UTF16ToString(buf[:bufSize/2])
}
