// Package snapshot 是 data 组件的 **snapshot 域实现**（阶段 4 试点：由 persist 下沉至此）。
//
// 位置含义（23-工程与部署拓扑 §2「隔离」）：`internal` 是**编译器强制**的可见性边界——
// `chonkpilot-data/internal/...` 只允许 `chonkpilot-data/...` 之内的包引用，模块外的
// 调用方（插件 / 测试 / 外壳）一旦直接 import 即编译失败（"use of internal package
// ... not allowed"）。原 `persist.GetSnapshot` / `persist.SetSnapshot` 是导出且带 `*data.DB`
// 参数的越界口子（plugin-compress 曾直接持库调用），现收进本包。
//
// 本包对外可见面 = 门面（`chonkpilot-data/facade`）的实现：见 service.go。
//
// **存储位置（一树一库，SESSION-COMP-1）**：快照落**对话库**的 `snapshot` 表
// （`dialogs/dialog-<hex(dialog_id)>.db`）；prjusr 只留对话目录，不含会话正文。
// 旧布局（`sessions/<hex>/` 每会话一库）已随 DATA-OPT-1 ② 整体退场。
package snapshot

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/storeadapter"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// GetInDialog 读会话快照（一树一库）。
//
// **实现唯一 = 会话组件** `component.GetSnapshot`（本函数只做「kernel 类型 ↔ 组件类型」搬运）。
// 库缺失 → 报错（组件读路径用 ExistingDialog，不静默建库）；无快照 → ok=false。
func GetInDialog(prjUsrDBPath, dialogID, sessionID string) (data.Snapshot, bool, error) {
	comp, err := newComponent(prjUsrDBPath)
	if err != nil {
		return data.Snapshot{}, false, err
	}
	res, err := comp.GetSnapshot(component.GetSnapshotCmd{DialogID: dialogID, SessionID: sessionID})
	if err != nil {
		return data.Snapshot{}, false, err
	}
	// found 口径：行在但 history/turn 皆空 → 视为无快照。
	if !res.Found || (res.SnapshotTurn == "" && len(res.History) == 0) {
		return data.Snapshot{}, false, nil
	}
	return data.Snapshot{SnapshotTurn: res.SnapshotTurn, History: res.History}, true, nil
}

// SetInDialog 写会话快照（一树一库）。
//
// **实现唯一 = 会话组件** `component.SetSnapshot`（会话行必须已存在、同事务刷新 updated_at、
// 提交后发事件）——本函数只做类型搬运，不再另写一份表访问。
func SetInDialog(prjUsrDBPath, dialogID, sessionID string, snap data.Snapshot) error {
	comp, err := newComponent(prjUsrDBPath)
	if err != nil {
		return err
	}
	return comp.SetSnapshot(component.SetSnapshotCmd{
		DialogID: dialogID, SessionID: sessionID,
		SnapshotTurn: snap.SnapshotTurn, History: snap.History,
	})
}

// newComponent 构造会话组件（存储 = 本模块的 bbolt 一树一库适配器）。
func newComponent(prjUsrDBPath string) (*component.Component, error) {
	return component.New(component.Options{Store: storeadapter.New(prjUsrDBPath)})
}
