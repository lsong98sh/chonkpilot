// CLI 数据根准备（12-数据层）：CLI **恒用临时目录**做数据根。
//
// 复制三级"配置"（usr / prj / prjusr 的 config 表 + llms/mcps 专用表），**不含会话数据**；
// 会话一律写临时目录、退出即弃——保证 CLI 跑批不污染用户配置与项目数据。
//
// 表复制/取 project-id 的实现**在 data 组件内**（`data.CopyConfigTables` /
// `data.ReadProjectIDPath`）：本文件不持库/表句柄（2026-09-21，原跨 module 直开库已删）。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chonkpilot/chonkpilot-data"
)

// prepareTempDataDir 建临时数据根并复制三级配置，返回临时目录与清理函数。
//
//	srcPrjDir 非空 = 源项目数据目录（其下 chonkpilot.db 为 prj 库）；空 = <workDir>/.chonkpilot。
//	清理函数：恢复数据主目录默认值 + 删除临时目录。
func prepareTempDataDir(workDir, srcPrjDir string) (string, func(), error) {
	// 先取真实主目录下的路径（随后 SetDataHome 会改写 data.UserPath/DataRoot）
	srcUsr := data.UserPath()
	srcDataRoot := data.DataRoot()

	tmp, err := os.MkdirTemp("", "chonkpilot-cli-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		data.Reset()
		data.SetDataHome("", "")
		_ = os.RemoveAll(tmp)
	}

	dstUsr := filepath.Join(tmp, "usr.db")
	// 数据主目录重定位到临时目录：usr 主库 + prjusr 数据根都随之走临时区
	data.SetDataHome(dstUsr, filepath.Join(tmp, "data"))

	// ① usr 配置
	if err := copyConfigInto(dstUsr, data.LayerUsr, srcUsr); err != nil {
		fmt.Fprintf(os.Stderr, "[cli] copy usr config: %v\n", err)
	}

	// ② prj 配置
	srcPrj := filepath.Join(workDir, ".chonkpilot", "chonkpilot.db")
	if srcPrjDir != "" {
		srcPrj = filepath.Join(srcPrjDir, "chonkpilot.db")
	}
	dstPrj := filepath.Join(tmp, "chonkpilot.db")
	if err := copyConfigInto(dstPrj, data.LayerPrj, srcPrj); err != nil {
		fmt.Fprintf(os.Stderr, "[cli] copy prj config: %v\n", err)
		return tmp, cleanup, nil
	}

	// ③ prjusr 配置覆盖 prj（同文件：临时形态 prj/prjusr 共库，见 12-数据层）
	if id, _ := data.ReadProjectIDPath(srcPrj); id != "" {
		srcPrjUsr := filepath.Join(srcDataRoot, id, "chonkpilot.db")
		if err := copyConfigInto(dstPrj, data.LayerPrjUsr, srcPrjUsr); err != nil {
			fmt.Fprintf(os.Stderr, "[cli] copy prjusr config: %v\n", err)
		}
	}
	return tmp, cleanup, nil
}

// copyConfigInto 把源库的 config 表 + 专用表复制进目标库（目标不存在则建）
// = data 组件内的 `CopyConfigTables`（调用方不持库句柄）；源不存在/被占用（另一实例持排他锁）
// → 跳过，不视为致命错误。
func copyConfigInto(dstPath string, layer data.Layer, srcPath string) error {
	return data.CopyConfigTables(dstPath, layer, srcPath)
}
