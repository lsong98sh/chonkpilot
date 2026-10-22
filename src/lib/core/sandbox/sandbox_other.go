//go:build !windows

package sandbox

import "os/exec"

// Start 在非 Windows 平台恒返回 [ErrUnsupportedPlatform]。
func Start(cmd *exec.Cmd, spec Spec) (cleanup func(), err error) {
	return nil, ErrUnsupportedPlatform
}

// Prepare 在非 Windows 平台恒返回 [ErrUnsupportedPlatform]。
func Prepare(cmd *exec.Cmd, spec Spec) error { return ErrUnsupportedPlatform }

// EnsureLowIL 在非 Windows 平台恒返回 [ErrUnsupportedPlatform]。
func EnsureLowIL(dir string) error { return ErrUnsupportedPlatform }
