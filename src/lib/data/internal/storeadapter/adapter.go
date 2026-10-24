// Package storeadapter 是 `chonkpilot-session/store` 端口的**bbolt 实现**（一树一库）。
//
// 职责边界（2026-10-24，41 D-69 ②）：组件只认端口；本适配器决定「怎么切、怎么落盘」：
//
//	Catalog        → prjusr 主库（shared layer，引用计数）
//	OpenDialog     → <prjusr>/dialogs/dialog-<hex>.db（不存在则创建）
//	ExistingDialog → 同上，缺失即报错（不静默新建）
//	CloneDialog    → bbolt 一致性备份（Tx.WriteTo），目标已存在则拒绝
//	DeleteDialog   → 删除库文件（幂等，安全守卫在 data.DeleteDialog）
//
// 行读写直接复用 `data.Table / data.Tx`（`store.Record` 与 `data.Record` 是同一别名）。
package storeadapter

import (
	"fmt"
	"os"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-session/store"
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
