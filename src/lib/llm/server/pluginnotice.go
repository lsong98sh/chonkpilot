// 插件失败的用户可见提示（2026-09-20，B 批"插件失败用户与日志双不可见"修复）。
//
// 现状问题：内嵌插件（memory 每轮沉淀 / compress 上下文压缩）失败时只 `logf("…跳过")`
// 即返回 —— **既不报用户、也不落日志文件**；用户只看到"记忆没有沉淀/上下文没有压缩"，
// 无从诊断，也没有任何可核提示。
//
// 修法（**不新增 MQ 主题**）：插件经 `plugin.Deps.Notify` 上报一次失败 → 宿主在此
//   - **去重/限频**：同一 (实例, 会话, 轮次, 插件, 失败类别) **只提示一次**（同一轮多类别/
//     多失败点不刷屏；轮次或类别变化才再提示）；
//   - **呈现**：经**既有通知面** `tool-notify`（61-消息一览 §4.3，2026-09-20 增补
//     `notice=plugin-failure`；前端 ChatPanel 既有订阅对非 completion 通知即以轻提示展示，
//     MessageList 对非 completion/recover 通知不落气泡）→ 可见但不打扰；
//   - **不阻塞**：仅 publish（不等应答）、不改流程终态、不降级、不抛出；
//   - **可查**：失败原因 = 插件同时写入的日志行（`logf` → gui.log）+ 提示里的可理解措辞。
package server

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-plugin"
)

// pluginNoticeWhat 把插件名映射为用户可读措辞（宿主提示文案用；未登记 → 原样展示插件名）。
var pluginNoticeWhat = map[string]string{
	"memory":   "记忆沉淀",
	"compress": "上下文压缩",
}

// pluginNoticeDedupCap 是判重表上限（超出即按插入序淘汰最旧键；长跑进程不无界增长）。
const pluginNoticeDedupCap = 512

// noticeDedup 是"同轮同类只提示一次"判重表（键 FIFO 淘汰；并发安全）。
type noticeDedup struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	order []string
}

// newNoticeDedup 构建判重表。
func newNoticeDedup() *noticeDedup {
	return &noticeDedup{seen: map[string]struct{}{}}
}

// mark 登记键：首次出现返回 true（应提示），重复出现返回 false（本轮已提示过）。
func (d *noticeDedup) mark(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[key]; ok {
		return false
	}
	d.seen[key] = struct{}{}
	d.order = append(d.order, key)
	for len(d.order) > pluginNoticeDedupCap {
		old := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, old)
	}
	return true
}

// pluginNotify 是插件失败上报入口（`plugin.Deps.Notify` 的实现）：判重后经既有
// tool-notify 通知面投递一条用户可见提示（不新增主题、不阻塞、不抛出）。
func (s *Server) pluginNotify(n plugin.Notice) {
	if s == nil || s.bus == nil || n.Plugin == "" || n.InstanceID == "" {
		return // 实例字段必带（61 §0）：缺失 → 不广播（与既有通知同口径）
	}
	kind := n.Kind
	if kind == "" {
		kind = "failed"
	}
	key := strings.Join([]string{n.InstanceID, n.Session, n.Turn, n.Plugin, kind}, "\x00")
	if s.noticeSeen != nil && !s.noticeSeen.mark(key) {
		return // 同轮同类已提示 → 去重（反打扰）
	}
	what := pluginNoticeWhat[n.Plugin]
	if what == "" {
		what = n.Plugin
	}
	reason := strings.TrimSpace(n.Reason)
	if reason == "" {
		reason = "原因未知（详见日志文件）"
	}
	s.publish("tool-notify", map[string]any{
		"instance_id": n.InstanceID,
		"session_id":  n.Session,
		"turn_id":     n.Turn,
		"notice":      "plugin-failure", // 新增 notice 取值（既有通知面 payload 只增字段）
		"plugin":      n.Plugin,
		"kind":        kind,
		"reason":      reason,
		"message":     "⚠️ " + what + "失败：" + reason + "（本轮对话未受影响；详情见日志文件）",
		"message_id":  "msg-pnotice-" + noticeKeyID(key),
	})
}

// noticeKeyID 由判重键派生稳定短 id（同一键 → 同一 id，供前端按 message_id 幂等去重）。
func noticeKeyID(key string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return fmt.Sprintf("%08x", h.Sum32())
}
