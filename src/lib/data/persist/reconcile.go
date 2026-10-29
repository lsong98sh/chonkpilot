// reconcile.go —— 实例登记后的 `dialogs/` 目录对账（SESSION-COMP-1 §8）。
//
// 语义：`dialogs` 是可重建索引 —— 每次实例登记后扫一遍 `dialogs/`：
// 文件在、目录行缺失 → 补行；行在、文件缺失 → 删行；title/kind/updated_at 漂移 → 回填。
// 覆盖「建库与写目录行之间崩溃」的半完成态（重启恢复）。
//
// 幂等：每条 prjusr 路径只跑一次（`reconciled`）；失败移除标记，下次 instance-register 重试。
// 失败一律降级为告警，不阻断启动。
package persist

import (
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/sessionstore"
)

// reconcileDialogs 执行一次目录对账（不搬运、不读任何旧布局开关）。
func (s *Service) reconcileDialogs(instanceID string) {
	if s == nil {
		return
	}
	pudb, err := s.PrjUsrFor(instanceID, facade.Scope{})
	if err != nil {
		s.warnReconcile("打开 prjusr 失败（跳过对账）: %v", err)
		return
	}
	key := pudb.Path()
	if _, loaded := s.reconciled.LoadOrStore(key, struct{}{}); loaded {
		return // 本进程已对账过（含并发重复触发）
	}
	rec, err := sessionstore.Reconcile(pudb)
	if err != nil {
		s.reconciled.Delete(key) // 允许下次重试（对账幂等）
		s.warnReconcile("对话目录对账失败（可重试）: %v", err)
		return
	}
	if s.Warnf != nil && (rec.Added > 0 || rec.Updated > 0 || rec.Removed > 0) {
		s.Warnf("persist: dialogs 目录对账完成 %s：+%d ~%d -%d", key, rec.Added, rec.Updated, rec.Removed)
	}
}

// warnReconcile 统一对账告警出口（Warnf 可为 nil）。
func (s *Service) warnReconcile(format string, args ...any) {
	if s != nil && s.Warnf != nil {
		s.Warnf("persist: "+format, args...)
	}
}
