// shard.go —— SESSION-COMP-1 P0/P3-4c：`session.db.shard` 开关与旧布局搬运触发点。
//
// 语义（[2G-会话组件] §9）：
//
//	生效（缺省，P3-4c 起）→ 实例登记后把 prjusr 旧布局（`sessions` 表 + `sessions/<hex>/`）
//	                        搬到 `dialogs/` 一树一库，并做一次启动对账（补目录行 / 删失效行 / 回填 title）；
//	显式 off              → 什么都不做（只读旧布局；测试 / 回退用）。
//
// 幂等：每 prjusr 路径只跑一次（`shardDone`）；失败移除标记，下次 instance-register 重试
// （搬运本身幂等，见 sessionstore.Migrate）。
package persist

import (
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/sessionstore"
)

// maybeShardDialogs 在 `session.db.shard` 生效（**缺省 on**，P3-4c）时执行一次
// 「旧布局 → dialogs/」搬运 + 对账。
//
// 失败一律降级为告警（不阻断启动 / 不影响旧布局读写）。
func (s *Service) maybeShardDialogs(instanceID string) {
	if s == nil {
		return
	}
	prj, release, err := s.PrjFor(instanceID, facade.Scope{})
	if err != nil {
		s.warnShard("读 prj 库失败（跳过搬运）: %v", err)
		return
	}
	on := kernel.SessionShardOn(prj)
	release()
	if !on {
		return
	}
	pudb, err := s.PrjUsrFor(instanceID, facade.Scope{})
	if err != nil {
		s.warnShard("打开 prjusr 失败（跳过搬运）: %v", err)
		return
	}
	key := pudb.Path()
	if _, loaded := s.shardDone.LoadOrStore(key, struct{}{}); loaded {
		return // 本进程已搬运过（含并发重复触发）
	}
	res, err := sessionstore.Migrate(pudb)
	if err != nil {
		s.shardDone.Delete(key) // 允许下次重试（搬运幂等）
		s.warnShard("旧布局搬运失败（可重试）: %v", err)
		return
	}
	rec, err := sessionstore.Reconcile(pudb)
	if err != nil {
		s.warnShard("对话目录对账失败（不影响搬运结果）: %v", err)
		return
	}
	if s.Warnf != nil && (res.Dialogs > 0 || res.Skipped > 0 || rec.Added > 0 || rec.Removed > 0) {
		s.Warnf("persist: session.db.shard=on 搬运完成 %s：新建对话 %d / 跳过 %d / session %d / snapshot %d / messages %d / turns %d / tasktree %d / inbox %d；对账 +%d ~%d -%d",
			key, res.Dialogs, res.Skipped, res.Sessions, res.Snapshots, res.Messages, res.Turns, res.Tasktree, res.Inbox,
			rec.Added, rec.Updated, rec.Removed)
	}
}

// warnShard 统一搬运告警出口（Warnf 可为 nil）。
func (s *Service) warnShard(format string, args ...any) {
	if s != nil && s.Warnf != nil {
		s.Warnf("persist: "+format, args...)
	}
}
