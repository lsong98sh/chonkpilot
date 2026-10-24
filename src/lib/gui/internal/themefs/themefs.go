// Package themefs 提供主题资产（themes.css）的读取。
//
// 口径（THEME-1，2026-10-23）：主题是一份**可编辑的静态 CSS 资产**，运行期优先读
// `<exeDir>/themes/themes.css`（用户可直接改产物目录，重启生效）；目录 / 文件缺失、不可读
// 或为空时**回落 go:embed 的内置副本** —— 永不因读取失败返回 404（最坏也返回内置内容）。
//
// 内置副本的唯一源 = `src/initdata/themes/themes.css`（构建脚本覆盖式同步到
// `embedded/themes.css`，勿手改 embedded 副本）。
package themefs

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed embedded/themes.css
var embeddedCSS []byte

// SubDir 是 exe 同目录下存放主题资产的子目录名（`<exeDir>/themes/`）。
const SubDir = "themes"

// FileName 是主题资产文件名。
const FileName = "themes.css"

// Load 返回主题 CSS 内容。
//
// 优先读 `<exeDir>/themes/themes.css`；exeDir 为空、文件缺失、读取失败或内容为空时
// 返回内置 embed 副本。error 仅在**内置副本也为空**（构建异常）时非 nil。
func Load(exeDir string) ([]byte, error) {
	if exeDir != "" {
		if b, err := os.ReadFile(filepath.Join(exeDir, SubDir, FileName)); err == nil && len(b) > 0 {
			return b, nil
		}
	}
	if len(embeddedCSS) == 0 {
		return nil, errors.New("themefs: 内置主题资产为空（go:embed 副本缺失）")
	}
	return embeddedCSS, nil
}
