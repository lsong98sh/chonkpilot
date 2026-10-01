// CLI 数据根准备（12-数据层 · 2A-cli §9）：CLI 数据根**三态语义**（2026-10-01 重定；原「恒用临时目录」作废）。
//
//	① 不传 --data-dir        → 用**真实根**（prj=<workDir>/.chonkpilot、prjusr=~/.chonkpilot/data/<id>），
//	                            **会话数据写入**；workdir 无 .chonkpilot → tmp 建空根（不污染当前目录，退出即弃）。
//	② --data-dir=（显式留空）  → 强制临时隔离：tmp 建空根 + 复制三级"配置"，退出即弃（旧行为）。
//	③ --data-dir=<路径>       → 用该路径作**数据根**（prjusr 数据根 data.DataRoot）；prjusr 仍按 project-id 派生
//	                            （不新增 --prjusr-dir）；prj 保持 <workDir>/.chonkpilot（无则 tmp）；usr 真实。
//
// "传空" 与 "未传" 由调用方经 flag.Visit 判定（见 main.go）。
//
// 表复制/取 project-id 的实现**在 data 组件内**（`data.CopyConfigTables` / `data.ReadProjectIDPath`）：
// 本文件不持库/表句柄（2026-09-21，原跨 module 直开库已删）。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// prepareDataDir 按三态语义准备 CLI 数据根，返回**要下发给实例的 data_dir** 与清理函数。
//
//	dataDirSet = --data-dir 是否显式出现（flag.Visit 判定）；workDir = 已解析的实际工作目录。
func prepareDataDir(workDir, dataDir string, dataDirSet bool) (string, func(), error) {
	// ② 显式留空 → 强制临时隔离（复制三级配置、退出即弃）
	if dataDirSet && dataDir == "" {
		return prepareTempDataDir(workDir)
	}
	// ③ 显式给路径 → 该路径作数据根（prjusr 数据根 data.DataRoot）
	cleanup := func() {
		data.Reset()
		data.SetDataHome("", "")
	}
	if dataDirSet {
		data.SetDataHome("", paths.ResolveDir(dataDir, workDir))
	}
	// ①/③ prj 数据根 = <workDir>/.chonkpilot：存在 → 真实根（data_dir=""）；否则 tmp 空根（不污染 cwd）
	if st, err := os.Stat(filepath.Join(workDir, ".chonkpilot")); err == nil && st.IsDir() {
		return "", cleanup, nil
	}
	tmp, err := os.MkdirTemp("", "chonkpilot-cli-")
	if err != nil {
		return "", cleanup, err
	}
	return tmp, func() { cleanup(); _ = os.RemoveAll(tmp) }, nil
}

// prepareTempDataDir 建临时数据根并复制三级配置（② 显式留空 `--data-dir=` 形态），
// 返回临时目录与清理函数。源配置 = 真实根（usr 主库 / <workDir>/.chonkpilot / prjusr 数据根）。
// 清理函数：恢复数据主目录默认值 + 删除临时目录。
func prepareTempDataDir(workDir string) (string, func(), error) {
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
