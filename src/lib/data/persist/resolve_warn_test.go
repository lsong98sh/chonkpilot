// MW-11 ④：`View.Resolve` 的唯一实例回退**保持可用**（单实例行为逐条不变），但输出告警；
// 多实例（非唯一）拒绝回退并告警 —— 让未带 instance_id 的遗漏显式暴露。
package persist

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestResolveFallbackWarns：单实例回退照旧 + 告警；显式 instance 不告警；非唯一拒绝回退 + 告警；
// 未注入 Warnf（默认）行为不变、不 panic。
func TestResolveFallbackWarns(t *testing.T) {
	var mu sync.Mutex
	var logs []string
	s := New(nil, Options{Warnf: func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}})
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(logs) == 0 {
			return ""
		}
		return logs[len(logs)-1]
	}
	logCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(logs)
	}

	// 单实例：回退照旧可用（行为不变）+ 告警
	s.View.Register("ins-1", "wd", "dd")
	id, info, err := s.View.Resolve("")
	if err != nil || id != "ins-1" || info.WorkDir != "wd" {
		t.Fatalf("单实例唯一实例回退行为应不变: id=%q info=%+v err=%v", id, info, err)
	}
	if !strings.Contains(last(), "回退唯一实例 ins-1") {
		t.Fatalf("单实例回退应告警: %v", logs)
	}

	// 显式 instance → 成功且无告警
	before := logCount()
	if _, _, err := s.View.Resolve("ins-1"); err != nil {
		t.Fatalf("显式命中应成功: %v", err)
	}
	if logCount() != before {
		t.Fatalf("显式 instance 不应告警: %v", logs)
	}

	// 多实例（非唯一）：拒绝回退（ErrInstanceIDRequired）+ 告警
	s.View.Register("ins-2", "wd2", "dd2")
	if _, _, err := s.View.Resolve(""); err == nil {
		t.Fatal("非唯一实例视图应拒绝回退（ErrInstanceIDRequired）")
	}
	if !strings.Contains(last(), "非唯一") {
		t.Fatalf("非唯一应告警: %v", logs)
	}

	// 未注入 Warnf（默认装配）→ 行为不变、不 panic
	s2 := New(nil, Options{})
	s2.View.Register("ins-x", "wd", "dd")
	if id, _, err := s2.View.Resolve(""); err != nil || id != "ins-x" {
		t.Fatalf("空 Warnf 行为应不变: id=%q err=%v", id, err)
	}
}
