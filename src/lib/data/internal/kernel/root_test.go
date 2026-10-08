// 实例数据根解析告警口径（B 修复）：容错探测路径（InstBindingFor → View.ResolveQuiet）**不告警**；
// 严格路径（WorkDirFor 的最终 Resolve / PrjUsrFor）仍用告警版 Resolve —— 解析语义逐条一致。
package kernel

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TestInstBindingForQuietNoWarn：容错探测不再刷"回退/拒绝回退"告警；严格路径仍告警。
func TestInstBindingForQuietNoWarn(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	rec := func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(logs) }

	// ① 单实例 + 空 instance_id：容错探测命中"唯一实例回退"，但**不告警**
	b := NewBase(nil, Options{Warnf: rec})
	b.View.Register("ins-1", "wd", "dd")
	wd, dd, ok := b.InstBindingFor("", facade.Scope{})
	if !ok || wd != "wd" || dd != "dd" {
		t.Fatalf("容错探测应命中唯一实例回退：wd=%q dd=%q ok=%v", wd, dd, ok)
	}
	if n := count(); n != 0 {
		t.Fatalf("容错探测（InstBindingFor）不应告警，got %d: %v", n, logs)
	}

	// ② 多实例 + 空 instance_id：容错探测失败（ok=false），仍**不告警**
	b.View.Register("ins-2", "wd2", "dd2")
	if _, _, ok := b.InstBindingFor("", facade.Scope{}); ok {
		t.Fatal("多实例 + 空 instance_id 容错探测应失败")
	}
	if n := count(); n != 0 {
		t.Fatalf("多实例容错探测不应告警，got %d: %v", n, logs)
	}

	// ③ 严格路径 WorkDirFor：同上输入（多实例 + 空）→ 报错且**告警**（走告警版 Resolve）
	if _, err := b.WorkDirFor("", facade.Scope{}); err == nil {
		t.Fatal("严格路径应报错（ErrInstanceIDRequired）")
	}
	mu.Lock()
	last := logs[len(logs)-1]
	mu.Unlock()
	if !strings.Contains(last, "拒绝回退") {
		t.Fatalf("严格路径应告警（拒绝回退）：%v", logs)
	}
}
