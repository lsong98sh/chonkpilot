package models

import (
	"os"
	"path/filepath"
	"strings"
)

// 目录字符串解析统一规范（见 docs/spec/10-architecture/16-路径解析规范.md）：
// 所有接受目录/路径的 CLI 参数（--work-dir、--data-dir 等）必须经 ResolveDir
// 解析，禁止裸用原始字符串。

// ExpandHome 展开 ~ 前缀为用户 home 目录（跨平台：Unix ~ 约定，Windows 同样支持）。
// "~" → home；"~/x" 或 "~\x" → home/x；其余原样返回。
func ExpandHome(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if len(p) >= 2 && (strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\")) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// ResolveDir 解析目录参数为规范绝对路径（项目目录字符串解析统一入口）：
//  1. ~ / ~/xxx 前缀展开为用户 home（ExpandHome）；
//  2. filepath.IsAbs → 绝对路径原样 filepath.Clean；
//  3. 否则 → filepath.Clean(filepath.Join(base, raw))（相对基准 = base）。
//
// raw 为空返回空（由调用方决定缺省值，如 --data-dir 缺省 <workDir>/.chonkpilot）。
func ResolveDir(raw, base string) string {
	raw = ExpandHome(raw)
	if raw == "" {
		return ""
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(base, raw))
}
