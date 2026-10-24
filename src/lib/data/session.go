// session.go —— **会话库**（SESSION-COMP-1：一树一库，P0 起为 `dialogs/`）。
//
// 两套布局并存（P0 只加新布局，P3 切换读路径）：
//
//	旧（LayerSession）：<prjusr>/sessions/<hex(session_id)>/chonkpilot.db —— 每主会话一库
//	新（LayerDialog）： <prjusr>/dialogs/dialog-<hex(dialog_id)>.db   —— 一库 = 一棵会话树
//
// prjusr 保留会话 → 对话的目录（旧 `sessions` 表 / 新 `dialogs` 表）。
// 连接策略：按路径缓存 + 引用计数（与 prj/usr 一致）；会话库**短开**（配对 release），
// 避免按会话堆积 fd。文件名 = hex(id)，天然规避路径穿越。
package data

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpenSession 打开某主会话的会话库（旧布局；短开：调用方必须配对 release）。
func OpenSession(prjUsrDBPath, sessionID string) (*DB, func(), error) {
	if sessionID == "" {
		return nil, nil, fmt.Errorf("data: session_id required")
	}
	return OpenSharedLayer(SessionDBPath(prjUsrDBPath, sessionID), LayerSession)
}

// OpenDialog 打开某对话的库文件（新布局，一树一库；短开：调用方必须配对 release）。
func OpenDialog(prjUsrDBPath, dialogID string) (*DB, func(), error) {
	if dialogID == "" {
		return nil, nil, fmt.Errorf("data: dialog_id required")
	}
	return OpenSharedLayer(DialogDBPath(prjUsrDBPath, dialogID), LayerDialog)
}

// DeleteDialog 删除对话库文件（新布局；幂等）。
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

// DeleteSession 删除会话库（旧布局，连同 <sessions>/<hex>/ 目录）。
//
// 仅供门面的「会话删除」使用；内部先强制关闭该路径的缓存连接，再删文件。
func DeleteSession(prjUsrDBPath, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	path := SessionDBPath(prjUsrDBPath, sessionID)
	dir := filepath.Dir(path)
	// 安全守卫：只允许删 <...>/sessions/<hex> 这样的目录（防止路径拼接失误扩大删除范围）。
	if filepath.Base(filepath.Dir(dir)) != "sessions" {
		return fmt.Errorf("data: 拒绝删除非会话库目录 %s", dir)
	}
	closeSharedPath(path)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
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
