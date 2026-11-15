// sessionlocate.go —— 跨用户会话定位（read / status 共用）。
//
// public / llm 模式下 `targets` 解析结果可能多于一个用户（public 恒含自己）。单一目标用户
// 不成立，因此按用户顺序**逐个查找会话所在对话库**，命中即返回；全部未命中 → not_found。
package plugindata

import (
	"errors"
	"path/filepath"

	"github.com/chonkpilot/chonkpilot-data"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// openSessionForUsers 在候选用户里定位会话（可选 dialog_id 快路径）并打开对话库。
//
// 返回 (user, dialogID, db, release, err)；调用方负责 release。
func (s *Store) openSessionForUsers(workDir string, users []string, sessionID, dialogID string) (string, string, *data.DB, func(), error) {
	for _, user := range users {
		dir, err := s.targetUserDir(workDir, user)
		if err != nil {
			return "", "", nil, nil, err
		}
		if dialogID != "" {
			db, release, oerr := openDialog(dir, dialogID)
			if oerr != nil {
				continue
			}
			var rec data.Record
			ok, gerr := db.Table(tableSessions).Get(sessionID, &rec)
			if gerr != nil || !ok || staleDialogRow(rec, dialogID) {
				release()
				if gerr != nil {
					return "", "", nil, nil, gerr
				}
				continue
			}
			return user, dialogID, db, release, nil
		}
		did, db, release, lerr := s.locateSessionDialog(filepath.Join(dir, sessionDBFile), sessionID)
		if lerr == nil {
			return user, did, db, release, nil
		}
		if !isSessionNotFound(lerr) {
			return "", "", nil, nil, lerr
		}
	}
	return "", "", nil, nil, errSessionNotFound(sessionID)
}

// isSessionNotFound 判定桥错误码是否为 not_found。
func isSessionNotFound(err error) bool {
	var e *pluginbridge.Error
	return errors.As(err, &e) && e.Code == pluginbridge.CodeNotFound
}
