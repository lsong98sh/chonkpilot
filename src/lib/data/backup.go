// backup.go —— 库文件一致性备份（bbolt 备份 API；SESSION-COMP-1 P4 fork 用）。
package data

import (
	"os"

	bolt "go.etcd.io/bbolt"
)

// BackupToFile 把当前库的一致性快照写入 dst（`Tx.WriteTo`；**不**用 os.Copy —— 那会拷到
// 撕裂快照）。`dst` 已存在 → 报错（不覆盖）；写失败 → 清理半成品文件。
func (db *DB) BackupToFile(dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := db.b.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(f)
		return err
	}); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}
