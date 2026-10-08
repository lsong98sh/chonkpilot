// 配置表整表复制（12-数据层）：CLI 数据根准备（临时目录形态）用。
//
// 逻辑原在 `src/desktop/cli/dataprep.go`（跨 module 直开库 + 取表句柄）；2026-09-21 移入
// data 组件——调用方只报「目标路径 + 层 + 源路径」，**不持库句柄**（库/表句柄不出 data 组件）。
package data

import (
	"fmt"
	"os"
	"strings"
)

// copyTables 是随配置一起复制的专用表（LLM 定义属用户级配置）。
var copyTables = []string{"llms"}

// CopyConfigTables 把源库的 config 表 + 专用表（llms）整表复制进目标库
// （目标不存在则建库）。源**不存在** → 视为跳过（返回 nil，不视为致命错误）；
// 源存在但读取失败（打开/ListKeys 失败，如被另一进程锁住）→ **明确报错**（D-45：
// 静默跳过会让 CLI 拿着缺配置的临时库跑，用户无从察觉）。
func CopyConfigTables(dstPath string, layer Layer, srcPath string) error {
	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", srcPath, err)
	}
	src, err := OpenLayer(srcPath, "")
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer src.Close()

	dst, err := OpenLayer(dstPath, layer)
	if err != nil {
		return err
	}
	defer dst.Close()

	keys, err := src.Table("config").ListKeys()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if k == ProjectIDKey {
			continue // 克隆库不复用源 project-id（A-08）：跳过复制，让克隆库首次打开生成新 id，避免与源共享 prjusr 数据根
		}
		var rec Record
		if ok, _ := src.Table("config").Get(k, &rec); ok {
			if err := dst.Table("config").Upsert(k, rec); err != nil {
				return err
			}
		}
	}
	for _, t := range copyTables {
		tkeys, err := src.Table(t).ListKeys()
		if err != nil {
			return fmt.Errorf("list %s.%s: %w", srcPath, t, err) // D-45：读失败改报错（原静默 continue）
		}
		for _, k := range tkeys {
			var rec Record
			if ok, _ := src.Table(t).Get(k, &rec); ok {
				if err := dst.Table(t).Upsert(k, rec); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ReadProjectIDPath 读某 prj 库文件的 project-id（文件不存在 / 打不开 → ok=false）。
func ReadProjectIDPath(prjDBPath string) (string, bool) {
	if _, err := os.Stat(prjDBPath); err != nil {
		return "", false
	}
	db, err := OpenLayer(prjDBPath, LayerPrj)
	if err != nil {
		return "", false
	}
	defer db.Close()
	return ReadProjectID(db)
}
