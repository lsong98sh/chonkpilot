// session.go —— **对话库**（SESSION-COMP-1：一树一库）。
//
// 布局（唯一）：
//
//	<prjusr>/dialogs/dialog-<hex(dialog_id)>.db   —— 一库 = 一棵会话树
//
// prjusr 只保留对话目录（`dialogs` 表）。旧布局（`sessions/<hex(session_id)>/chonkpilot.db`
// 每主会话一库）已随 DATA-OPT-1 ② 整体退场，不再有打开/删除原语。
//
// 连接策略：按路径缓存 + 引用计数（与 prj/usr 一致）；对话库**短开**（配对 release），
// 避免按对话堆积 fd。文件名 = hex(id)，天然规避路径穿越。
package data

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpenDialog 打开某对话的库文件（一树一库；短开：调用方必须配对 release）。
func OpenDialog(prjUsrDBPath, dialogID string) (*DB, func(), error) {
	if dialogID == "" {
		return nil, nil, fmt.Errorf("data: dialog_id required")
	}
	return OpenSharedLayer(DialogDBPath(prjUsrDBPath, dialogID), LayerDialog)
}

// DeleteDialog 删除对话库文件（幂等）。
//
// 安全守卫：只允许删 `.../dialogs/dialog-*.db`（防止路径拼接失误扩大删除范围）。
// 目录行（prjusr `dialogs` 表）由调用方先删 / 后删均可。
func DeleteDialog(prjUsrDBPath, dialogID string) error {
	if dialogID == "" {
		return nil
	}
	path := DialogDBPath(prjUsrDBPath, dialogID)
	if filepath.Base(filepath.Dir(path)) != "dialogs" {
		return fmt.Errorf("data: 拒绝删除非对话库文件 %s", path)
	}
	closeSharedPath(path)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// closeSharedPath 强制关闭某路径的缓存连接（仅用于「要删文件」的场景；正常读写走 release）。
func closeSharedPath(path string) {
	storeMu.Lock()
	s := shared[path]
	delete(shared, path)
	storeMu.Unlock()
	if s != nil {
		_ = s.db.Close()
	}
}
