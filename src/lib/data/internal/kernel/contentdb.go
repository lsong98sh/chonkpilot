// contentdb.go —— 会话内容（turns / messages / tasktree / inbox / snapshot）所在库的选择。
//
// 一树一库（SESSION-COMP-1）：会话内容**只**在对话库（`dialogs/dialog-<hex(dialog_id)>.db`）；
// `dialog_id` 缺失 → 明确失败（旧布局已整体退场，DATA-OPT-1 ②）。
// 唯一实现点：session / tasktree 两个域门面共用（口径必须一致：缺失即报错，不回退）。
package kernel

import (
	"errors"

	"github.com/chonkpilot/chonkpilot-data"
)

// ErrDialogRequired 是「会话类调用未带 dialog_id」的明确失败
// （61 §0.6）：不做全局反查，也不回退任何旧布局。
var ErrDialogRequired = errors.New("dialog_required: 会话类调用缺少 dialog_id（一树一库：会话内容只在对话库内）")

// ContentDB 选内容库：`dialogID` 非空 → 打开**已存在**的对话库（缺失即报错）；
// 空 → ErrDialogRequired。
//
// 唯一例外是根会话首次 `data-session-ensure-session` 的自举（走 SessionEnsure，不经本函数）。
func ContentDB(prj *data.DB, dialogID string) (*data.DB, func(), error) {
	if dialogID == "" {
		return nil, nil, ErrDialogRequired
	}
	return data.OpenExistingDialog(prj.Path(), dialogID)
}
