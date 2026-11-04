// export.go —— `session.export` 的数据读取通道（G-103，2026-11-03）。
//
// 通道 = 数据层**既有公开 API**（`github.com/chonkpilot/chonkpilot-data`，与数据面
// `data-session-get` / `data-session-history` 内部同一读取模式）：
//   - 目标用户 prjusr 目录：desktop = `<work_dir>/.chonkpilot/users/<user>`；共享数据根形态
//     （`Options.DataDir` 非空）无 users/ 分支 → prjusr = `DataDir`（与 user 无关）；
//   - 枚举 `<prjusr>/dialogs/dialog-*.db`（只列文件名，`data.DialogIDFromFile` 反解 dialog_id）；
//   - 逐库 `data.OpenExistingDialog` **短开**（不持有句柄；与在写回合的短并发由 bbolt 3s
//     超时自然互斥，与数据面自身读路径同语义）；
//   - 对话库 `sessions` 表按 session_id 命中 → 读 `turns` / `messages` 组装导出 JSON。
//
// C8 铁律不破：本包运行在**宿主进程**（插件侧不得直接开库指插件进程）；本文件不 import bbolt。
// 导出体**内联返回**（不落文件、不建导出目录），调用方自行消费；不做脱敏（与 history 同口径，
// 单机单信任域），顶层含 `session_id` / `dialog_id` / `exported_at` / `session` / `turns`（各自含 messages）。
package plugindata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// 会话 / 轮次 / 消息在对话库内的表名（与数据层 internal/session 同一 schema，只读）。
const (
	tableSessions = "sessions"
	tableTurns    = "turns"
	tableMessages = "messages"
)

// sessionPrjUsrDir 解析导出目标用户的 prjusr 目录（与 usersRoot 同一分支判据）。
func (s *Store) sessionPrjUsrDir(workDir, user string) (string, error) {
	if s.opts.UsersRoot != nil {
		root, err := s.opts.UsersRoot(workDir)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, user), nil
	}
	if s.opts.DataDir != "" {
		// 共享数据根形态：prjusr = data_dir（无 users/ 分支，user 参数不改变落点）。
		return s.opts.DataDir, nil
	}
	root, err := s.usersRoot(workDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, user), nil
}

// readSessionExport 导出某用户某会话的完整 JSON。会话在任何对话库中都找不到 → `not_found`。
func (s *Store) readSessionExport(workDir, user, sessionID string) (json.RawMessage, error) {
	prjUsrDir, err := s.sessionPrjUsrDir(workDir, user)
	if err != nil {
		return nil, err
	}
	dialogID, db, release, err := s.locateSessionDialog(filepath.Join(prjUsrDir, "chonkpilot.db"), sessionID)
	if err != nil {
		return nil, err
	}
	defer release()

	var sessionRec data.Record
	if ok, gerr := db.Table(tableSessions).Get(sessionID, &sessionRec); gerr != nil {
		return nil, gerr
	} else if !ok {
		return nil, errSessionNotFound(sessionID)
	}
	turns, err := readTurnsWithMessages(db, sessionID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"session_id":  sessionID,
		"dialog_id":   dialogID,
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"session":     sessionRec,
		"turns":       turns,
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

// locateSessionDialog 在 prjusr 的各对话库中定位包含该 session 的库（短开，调用方配对 release）。
func (s *Store) locateSessionDialog(prjUsrDBPath, sessionID string) (string, *data.DB, func(), error) {
	dialogsDir := filepath.Join(filepath.Dir(prjUsrDBPath), "dialogs")
	entries, rerr := os.ReadDir(dialogsDir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return "", nil, nil, errSessionNotFound(sessionID)
		}
		return "", nil, nil, rerr
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		dialogID, ok := data.DialogIDFromFile(name)
		if !ok {
			continue
		}
		db, release, oerr := data.OpenExistingDialog(prjUsrDBPath, dialogID)
		if oerr != nil {
			// 单库打开失败（被短占用 / 损坏）→ 跳过继续找；全部未命中按 not_found 收口。
			s.logf("plugindata: 导出跳过对话库 %s: %v", name, oerr)
			continue
		}
		var rec data.Record
		found, gerr := db.Table(tableSessions).Get(sessionID, &rec)
		if gerr != nil {
			release()
			return "", nil, nil, gerr
		}
		if !found || staleDialogRow(rec, dialogID) {
			release()
			continue
		}
		return dialogID, db, release, nil
	}
	return "", nil, nil, errSessionNotFound(sessionID)
}

// staleDialogRow 行内 dialog_id 与所在库不符 = 陈旧行（半删除残留），按未命中跳过。
func staleDialogRow(rec data.Record, dialogID string) bool {
	if v, ok := rec["dialog_id"].(string); ok && v != "" && v != dialogID {
		return true
	}
	return false
}

// readTurnsWithMessages 读该会话全部轮次并嵌套各自消息：turns 按 (created_at, key) 升序，
// 轮内 messages 同口径；导出元素 = 轮次行 + `messages` 数组（不落库、不回写）。
func readTurnsWithMessages(db *data.DB, sessionID string) ([]map[string]any, error) {
	type row struct {
		key string
		rec data.Record
	}
	var turnRows, msgRows []row
	collect := func(table string, dst *[]row) error {
		return db.Table(table).ForEach(func(key string, rec data.Record) error {
			if v, _ := rec["session_id"].(string); v != sessionID {
				return nil
			}
			*dst = append(*dst, row{key: key, rec: rec})
			return nil
		})
	}
	if err := collect(tableTurns, &turnRows); err != nil {
		return nil, err
	}
	if err := collect(tableMessages, &msgRows); err != nil {
		return nil, err
	}
	less := func(a, b row) bool {
		if a.rec["created_at"] != b.rec["created_at"] {
			return rowTime(a.rec) < rowTime(b.rec)
		}
		return a.key < b.key
	}
	sort.Slice(turnRows, func(i, j int) bool { return less(turnRows[i], turnRows[j]) })
	sort.Slice(msgRows, func(i, j int) bool { return less(msgRows[i], msgRows[j]) })

	byTurn := make(map[string][]any, len(turnRows))
	for _, m := range msgRows {
		tid, _ := m.rec["turn_id"].(string)
		byTurn[tid] = append(byTurn[tid], m.rec)
	}
	out := make([]map[string]any, 0, len(turnRows))
	for _, tr := range turnRows {
		item := make(map[string]any, len(tr.rec)+1)
		for k, v := range tr.rec {
			item[k] = v
		}
		msgs := byTurn[tr.key]
		if msgs == nil {
			msgs = []any{}
		}
		item["messages"] = msgs
		out = append(out, item)
	}
	return out, nil
}

// rowTime 取 created_at 的字符串值（数据面统一 RFC3339FixedNano，9 位纳秒 → 字典序 = 时间序）。
func rowTime(rec data.Record) string {
	v, _ := rec["created_at"].(string)
	return v
}

func errSessionNotFound(sessionID string) error {
	return &pluginbridge.Error{
		Code: pluginbridge.CodeNotFound,
		Msg:  "会话不存在: " + sessionID,
	}
}
