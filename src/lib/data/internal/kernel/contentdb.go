// contentdb.go —— 会话内容（turns / messages / tasktree / inbox / snapshot）所在库的选择。
//
// 一树一库（SESSION-COMP-1）：`dialog_id` 非空 → 对话库；空 → 旧布局 prjusr。
// 唯一实现点：session / tasktree 两个域门面共用（口径必须一致：缺失即报错，不回退）。
package kernel

import (
	"errors"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// ErrDialogRequired 是「一树一库已生效、调用方却未带 dialog_id」的明确失败
// （61 §0.6 / P3-4d）：不做全局反查，也不静默落回 prjusr。
var ErrDialogRequired = errors.New("dialog_required: 会话类调用缺少 dialog_id（session.db.shard 已生效；如需旧布局请显式置 off）")

// ShardOn 读实例 prj 层的 `session.db.shard`（缺省 on；读失败 → false，不阻断旧布局）。
//
// 开关层级**唯一** = prj 库（与 persist 搬运同源）；勿用 prjusr 库判定（两层各有一张 config 表）。
func (b *Base) ShardOn(instanceID string, scope facade.Scope) bool {
	prj, release, err := b.PrjFor(instanceID, scope)
	if err != nil {
		return false
	}
	defer release()
	return SessionShardOn(prj)
}

// ContentDB 选内容库：`dialogID` 非空 → 打开**已存在**的对话库（缺失即报错）；空 → prjusr（no-op 释放）。
//
// **P3-4d（2026-10-24）**：`shardOn`（调用方按实例经 ShardOn 解析）生效时缺 `dialog_id`
// → ErrDialogRequired。唯一例外是根会话首次 `data-session-ensure-session` 的自举
// （走 SessionEnsure，不经本函数）。
//
// 注：`shardOn` 由调用方显式传入 —— 本函数只拿到 prjusr 库，无法自行判定 prj 层开关。
func ContentDB(prj *data.DB, dialogID string, shardOn bool) (*data.DB, func(), error) {
	if dialogID != "" {
		return data.OpenExistingDialog(prj.Path(), dialogID)
	}
	if shardOn {
		return nil, nil, ErrDialogRequired
	}
	return prj, func() {}, nil
}
