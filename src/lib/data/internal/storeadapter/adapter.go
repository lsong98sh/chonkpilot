// Package storeadapter 是 `chonkpilot-session/store` 端口的**bbolt 实现**（一树一库）。
//
// 职责边界（2026-10-24，41 D-69 ②）：组件只认端口；本适配器决定「怎么切、怎么落盘」：
//
//	Catalog        → prjusr 主库（shared layer，引用计数）
//	OpenDialog     → <prjusr>/dialogs/dialog-<hex>.db（不存在则创建）
//	ExistingDialog → 同上，缺失即报错（不静默新建）
//	CloneDialog    → bbolt 一致性备份（Tx.WriteTo），目标已存在则拒绝
//	DeleteDialog   → 删除库文件（幂等，安全守卫在 data.DeleteDialog）
//	PurgeDialog    → 可选端口 store.DialogPurger：目录行 + 库文件（整对话删除；活动位不清 = 宿主补偿）
//
// 行读写直接复用 `data.Table / data.Tx`（`store.Record` 与 `data.Record` 是同一别名）。
package storeadapter

import (
	"errors"
	"fmt"
	"os"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-agent/session/store"
)

// 编译期断言：本适配器实现组件要求的基础端口 + 可选端口 `store.DialogPurger`
// （可选端口未实现 → 组件 `DeleteSession(主)` 返回 `E_UNSUPPORTED`）。
var (
	_ store.Store        = (*Store)(nil)
	_ store.DialogPurger = (*Store)(nil)
)

// Store 是一树一库（bbolt）的端口实现。
type Store struct {
	prjUsrPath string
}

// New 构造适配器（prjUsrDBPath = prjusr 主库文件路径）。
func New(prjUsrDBPath string) *Store { return &Store{prjUsrPath: prjUsrDBPath} }

// Catalog 打开 prjusr 主库（配对 release）。
func (s *Store) Catalog() (store.DB, func(), error) {
	db, release, err := data.OpenSharedLayer(s.prjUsrPath, data.LayerPrjUsr)
	if err != nil {
		return nil, nil, err
	}
	return dbWrap{db}, release, nil
}

// OpenDialog 打开（不存在则创建）对话库。
func (s *Store) OpenDialog(dialogID string) (store.DB, func(), error) {
	db, release, err := data.OpenDialog(s.prjUsrPath, dialogID)
	if err != nil {
		return nil, nil, err
	}
	return dbWrap{db}, release, nil
}

// ExistingDialog 打开已存在的对话库（缺失 → 错误）。
func (s *Store) ExistingDialog(dialogID string) (store.DB, func(), error) {
	db, release, err := data.OpenExistingDialog(s.prjUsrPath, dialogID)
	if err != nil {
		return nil, nil, err
	}
	return dbWrap{db}, release, nil
}

// CloneDialog 复制源对话库为新对话库（bbolt 一致性快照；目标已存在 → ErrForkTargetExists）。
func (s *Store) CloneDialog(srcDialogID, dstDialogID string) error {
	dstPath := data.DialogDBPath(s.prjUsrPath, dstDialogID)
	if _, err := os.Stat(dstPath); err == nil {
		return fmt.Errorf("%w: %s", store.ErrForkTargetExists, dstPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	src, release, err := data.OpenExistingDialog(s.prjUsrPath, srcDialogID)
	if err != nil {
		return err
	}
	defer release()
	return src.BackupToFile(dstPath)
}

// DeleteDialog 删除对话库（幂等）。
func (s *Store) DeleteDialog(dialogID string) error {
	return data.DeleteDialog(s.prjUsrPath, dialogID)
}

// PurgeDialog 实现组件**可选端口** `store.DialogPurger`（P4-2 批次二，2026-10-28）：
// 整对话删除 = 删 prjusr `dialogs` 目录行 + 删对话库文件（两者都幂等）。
//
// **活动位不在这里清**（选了「只做 1+2」方案）：
//   - 适配器是通用端口实现，**拿不到「主 session id」**（要读对话库 `sessions` 表 = 越过端口边界，
//     「表名 / 主键 / 行字段归组件」）；
//   - 「活动位指向的 session 是否已不存在于任何对话库」需要**全库扫描**（逐个打开对话库，成本高）。
//
// 因此 `active_session_id`（prjusr `config.active_session_id`，宿主库的 UI 运行态）的清理由**宿主**在
// 调用点用 `clearActiveIfMatches`（data 侧同名口径）作跨库补偿完成：两库无跨库事务（2G §8 / 49 §2.3）。
func (s *Store) PurgeDialog(dialogID string) error {
	if dialogID == "" {
		return fmt.Errorf("storeadapter: dialog_id required")
	}
	cat, release, err := s.Catalog()
	if err != nil {
		return err
	}
	err = cat.Table("dialogs").Delete(dialogID)
	release()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("storeadapter: 删 dialogs 行 %s: %w", dialogID, err)
	}
	return s.DeleteDialog(dialogID)
}

// DialogFileName 目录行 `file` 值（一树一库命名规则）。
func (s *Store) DialogFileName(dialogID string) string { return data.DialogFileName(dialogID) }

// NewID 生成新 id（UUID v4）。
func (s *Store) NewID() (string, error) { return data.NewDialogID() }

// dbWrap 把 *data.DB 适配为 store.DB（Table 返回接口；Update 回调参数换成 store.Tx）。
type dbWrap struct{ db *data.DB }

func (d dbWrap) Path() string { return d.db.Path() }

func (d dbWrap) Table(name string) store.Table { return d.db.Table(name) }

func (d dbWrap) Update(fn func(tx store.Tx) error) error {
	return d.db.Update(func(tx *data.Tx) error { return fn(tx) })
}
