package models

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultWorkspaceDirName 是首次启动时自动创建的工作目录名。
const DefaultWorkspaceDirName = "workspace"

// DefaultWorkspaceDir 返回首次启动的默认工作目录：
// Windows = %APPDATA%\chonkpilot\workspace（os.UserConfigDir 语义）；
// 取不到用户配置目录时回落到 ~/.chonkpilot/workspace。
func DefaultWorkspaceDir() string {
	base, err := os.UserConfigDir()
	if err == nil && base != "" {
		return filepath.Join(base, "chonkpilot", DefaultWorkspaceDirName)
	}
	home, herr := os.UserHomeDir()
	if herr != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".chonkpilot", DefaultWorkspaceDirName)
}

// EnsureDefaultWorkspace 创建并返回默认工作目录（幂等）。
func EnsureDefaultWorkspace() (string, error) {
	dir := DefaultWorkspaceDir()
	if dir == "" {
		return "", fmt.Errorf("无法解析默认工作目录")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建默认工作目录 %s: %w", dir, err)
	}
	return dir, nil
}
