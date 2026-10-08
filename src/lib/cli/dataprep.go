// Package cli 承载 CLI 形态的**启动前置编排**（I-158 · D-45，自 src/desktop/cli/dataprep.go 上移）：
// `--data-dir` 三态解析（方案 B）、work-dir 占用校验（I-74 原语复用）、prjusr 试开冲突检测、
// 三级配置复制编排（临时隔离形态）。chonkpilot-cli.exe（src/desktop/cli）只留 flag 解析 + 装配，
// 本包独立成库使 L2 黑盒测试可行（src/test/chonkpilot-cli/unittest）。
//
// 三态语义（方案 B，D-45 · 42 §2 (239)；2026-10-01 旧三态已**反转**）：
//
//	① 未指定 --data-dir            → 临时隔离（默认）：tmp 建空根 + 复制三级配置，退出即弃
//	                                  —— CLI 批处理不污染真实配置、不与 GUI 冲突（bbolt 打开即排他锁）。
//	② 显式留空（`--data-dir=`）     → 真实根（与 GUI 一致）：会话写入 <workDir>/.chonkpilot +
//	                                  ~/.chonkpilot/data/<prj-id>；**参与 work-dir 占用校验**
//	                                  （同 work-dir 只允许一个持真实根的实例，I-74）。
//	③ 给路径（`--data-dir=<路径>`） → 该路径作 prjusr 数据根（data.SetDataHome 重定位）；
//	                                  prj 仍按 <workDir>/.chonkpilot（无则 tmp 空根，不污染 cwd）。
//
// 冲突口径（② ③ 态）：prj/usr 层双方短开（D-45 ②）→ 可与 GUI 并发读同一项目配置；
// **prjusr 保持常开**（D-45 ③）→ 真实/自定数据根下的 prjusr 库若已被另一进程持有（GUI），
// 启动准备期**试开即报错退出**（提示改用临时模式或关闭 GUI），不留到 server 启动才失败。
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/lockfile"
	"github.com/chonkpilot/chonkpilot-lib/paths"
)

// DataMode 是 `--data-dir` 三态（方案 B，D-45）。
type DataMode int

const (
	// DataModeTemp ① 未指定 → 临时隔离（tmp 空根 + 复制三级配置，退出即弃）。
	DataModeTemp DataMode = iota
	// DataModeReal ② 显式留空 → 真实根（与 GUI 一致；参与 work-dir 占用校验）。
	DataModeReal
	// DataModeCustom ③ 给路径 → 该路径作 prjusr 数据根。
	DataModeCustom
)

// ResolveDataMode 三态判定（dataDirSet = flag.Visit 判定 `--data-dir` 是否显式出现）。
func ResolveDataMode(dataDir string, dataDirSet bool) DataMode {
	switch {
	case !dataDirSet:
		return DataModeTemp
	case dataDir == "":
		return DataModeReal
	default:
		return DataModeCustom
	}
}

// Prepared 是数据根准备结果（PrepareDataDir 产物）。
type Prepared struct {
	// Mode 命中的三态。
	Mode DataMode
	// DataDir 下发给实例的 data_dir（数据层分支判据，12-数据层 §3）：
	// "" = 实例按常规规则解析真实根；非空 = 临时/自定数据根（prj 与 prjusr 同根）。
	DataDir string
	cleanup func()
}

// Cleanup 退出清理：临时态删 tmp 根 + 恢复数据主目录默认；真实/自定态释放占用锁 +
// 恢复数据主目录默认。幂等（重复调用为空操作）。
func (p *Prepared) Cleanup() {
	if p.cleanup != nil {
		p.cleanup()
		p.cleanup = nil
	}
}

// PrepareDataDir 按三态准备 CLI 数据根。workDir 必须已是绝对路径（调用方 resolveDir 后传入）。
// 返回的 Prepared 必须在进程退出前 Cleanup（defer）。
func PrepareDataDir(workDir, dataDir string, dataDirSet bool) (*Prepared, error) {
	switch ResolveDataMode(dataDir, dataDirSet) {
	case DataModeTemp:
		return prepareTemp(workDir)
	case DataModeReal:
		return prepareReal(workDir)
	default:
		return prepareCustom(workDir, dataDir)
	}
}

// prepareReal ② 显式留空 → 真实根（data_dir="" 下发）。
// 顺序：work-dir 占用校验（I-74：**先于任何库打开**，避免被 prjusr 排他锁卡住）→ prjusr 试开。
func prepareReal(workDir string) (*Prepared, error) {
	lock, err := lockfile.AcquireWorkDir(workDir)
	switch {
	case err == nil:
		// 持锁至 Cleanup（对齐 GUI：锁随实例存活，进程崩溃由 OS 回收）。
	case errors.Is(err, lockfile.ErrBusy):
		return nil, fmt.Errorf("work-dir 已被另一实例打开（%s）：请关闭该实例，或改用临时模式（不传 --data-dir）", workDir)
	default:
		// 锁设施自身故障（非占用）不阻断启动（对齐 GUI 口径：仅降级不持锁）。
		lock = nil
	}
	p := &Prepared{Mode: DataModeReal, DataDir: ""}
	p.cleanup = func() {
		data.Reset() // 关闭 prjusr 常开句柄等连接缓存（Windows 侧先于锁释放）
		if lock != nil {
			lock.Release()
		}
	}
	if err := probePrjUsr(workDir); err != nil {
		p.Cleanup()
		return nil, err
	}
	return p, nil
}

// prepareCustom ③ 给路径 → 该路径作 prjusr 数据根；prj 仍按 <workDir>/.chonkpilot
// （存在 → data_dir=""；无则 tmp 空根——不污染 cwd，与旧口径一致）。
func prepareCustom(workDir, dataDir string) (*Prepared, error) {
	root := paths.ResolveDir(dataDir, workDir)
	data.SetDataHome("", root) // prjusr 数据根重定位（usr 主库不动）
	p := &Prepared{Mode: DataModeCustom, DataDir: ""}
	p.cleanup = func() {
		data.Reset()
		data.SetDataHome("", "")
	}
	if st, err := os.Stat(filepath.Join(workDir, ".chonkpilot")); err == nil && st.IsDir() {
		if err := probePrjUsr(workDir); err != nil { // 自定数据根下的 prjusr 试开
			p.Cleanup()
			return nil, err
		}
		return p, nil
	}
	tmp, err := os.MkdirTemp("", "chonkpilot-cli-")
	if err != nil {
		p.Cleanup()
		return nil, err
	}
	p.DataDir = tmp // tmp 形态：prj/prjusr 同库于临时根（自定 root 不参与，实例按 data_dir 解析）
	prev := p.cleanup
	p.cleanup = func() { prev(); _ = os.RemoveAll(tmp) }
	return p, nil
}

// prepareTemp ① 未指定 → 临时隔离：tmp 建空根 + 复制三级配置（usr / prj / prjusr 覆盖 prj），
// 退出即弃。配置复制失败 → **报错**（D-45 ④：静默降级会让 CLI 拿着缺配置的库跑，用户无从察觉）。
func prepareTemp(workDir string) (*Prepared, error) {
	// 先取真实主目录下的路径（随后 SetDataHome 会改写 data.UserPath/DataRoot）。
	srcUsr := data.UserPath()
	srcDataRoot := data.DataRoot()

	tmp, err := os.MkdirTemp("", "chonkpilot-cli-")
	if err != nil {
		return nil, err
	}
	p := &Prepared{Mode: DataModeTemp, DataDir: tmp}
	p.cleanup = func() {
		data.Reset()
		data.SetDataHome("", "")
		_ = os.RemoveAll(tmp)
	}

	dstUsr := filepath.Join(tmp, "usr.db")
	// 数据主目录重定位到临时目录：usr 主库 + prjusr 数据根都随之走临时区。
	data.SetDataHome(dstUsr, filepath.Join(tmp, "data"))

	// ① usr 配置（源不存在 → CopyConfigTables 跳过；读失败 → 报错，D-45 ④）。
	if err := data.CopyConfigTables(dstUsr, data.LayerUsr, srcUsr); err != nil {
		p.Cleanup()
		return nil, fmt.Errorf("复制 usr 配置: %w", err)
	}

	// ② prj 配置。
	srcPrj := filepath.Join(workDir, ".chonkpilot", "chonkpilot.db")
	dstPrj := filepath.Join(tmp, "chonkpilot.db")
	if err := data.CopyConfigTables(dstPrj, data.LayerPrj, srcPrj); err != nil {
		p.Cleanup()
		return nil, fmt.Errorf("复制 prj 配置: %w", err)
	}

	// ③ prjusr 配置覆盖 prj（同文件：临时形态 prj/prjusr 共库，见 12-数据层）。
	if id, _ := data.ReadProjectIDPath(srcPrj); id != "" {
		srcPrjUsr := filepath.Join(srcDataRoot, id, "chonkpilot.db")
		if err := data.CopyConfigTables(dstPrj, data.LayerPrjUsr, srcPrjUsr); err != nil {
			p.Cleanup()
			return nil, fmt.Errorf("复制 prjusr 配置: %w", err)
		}
	}
	return p, nil
}

// probePrjUsr 试开**当前数据主目录配置下**的 prjusr 库（真实/自定数据根形态的冲突检测，
// D-45 (239) ③）：prj 库已有 project-id 且对应 prjusr 库被另一进程常开（GUI）→ 明确报错。
// prj 库不存在（新项目）或 prjusr 库不存在 → 无冲突可试，直接通过。
// 试开成功即释放（服务启动后按 prjusr 常开语义正式打开；窗口期竞态由主库 Open 超时兜底）。
func probePrjUsr(workDir string) error {
	id, ok := data.ReadProjectIDPath(data.ProjectPath(workDir, ""))
	if !ok || id == "" {
		return nil
	}
	puPath := data.PrjUsrDBPath(id)
	if _, err := os.Stat(puPath); err != nil {
		return nil // prjusr 库不存在 → 无冲突
	}
	_, release, err := data.OpenSharedLayer(puPath, data.LayerPrjUsr)
	if err != nil {
		return fmt.Errorf("prjusr 库被另一实例占用（%s）：%v——可改用临时模式（不传 --data-dir）或关闭 GUI", puPath, err)
	}
	release()
	return nil
}
