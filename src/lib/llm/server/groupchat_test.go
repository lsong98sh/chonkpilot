// 群聊项目级配置（`group_chat.max_rounds`）读取口径单测（SUBCHAT-3 阶段 E）。
//
// 口径（D49 / §10 C8）：缺失 / 非数字 / **区间外**（< 20 或 > 40）→ 回落默认 20；
// 区间内（20–40）→ 原样采用。断言纯函数与读点（经门面 prj 配置）两侧一致。
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

func TestGroupChatMaxRoundsConfig(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	data.Register("ins-test", testWorkDir, "")
	t.Cleanup(data.Reset)
	prj, releasePrj, err := data.Prj("ins-test")
	if err != nil {
		t.Fatalf("data.Prj: %v", err)
	}
	t.Cleanup(releasePrj)

	// 缺失 → 默认 20。
	if got := s.loadGroupChatMaxRounds("ins-test"); got != defaultGroupChatMaxRounds {
		t.Fatalf("缺失 group_chat.max_rounds → 默认 %d：got %d", defaultGroupChatMaxRounds, got)
	}
	if defaultGroupChatMaxRounds != 20 {
		t.Fatalf("默认值应为 20（D49）：got %d", defaultGroupChatMaxRounds)
	}

	// 区间内 → 原样采用（读覆盖）。
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"20", 20}, {"25", 25}, {"40", 40}, {" 30 ", 30}, // 容忍首尾空白
	} {
		if err := data.SetConfig(prj, groupChatMaxRoundsKey, tc.raw); err != nil {
			t.Fatal(err)
		}
		if got := s.loadGroupChatMaxRounds("ins-test"); got != tc.want {
			t.Fatalf("group_chat.max_rounds=%q → 期望 %d：got %d", tc.raw, tc.want, got)
		}
	}

	// 区间外 / 非数字 / 空 → 回落默认 20（不 clamp，避免误配被静默放大）。
	for _, raw := range []string{"19", "41", "0", "-5", "abc", "", "1000"} {
		if err := data.SetConfig(prj, groupChatMaxRoundsKey, raw); err != nil {
			t.Fatal(err)
		}
		if got := s.loadGroupChatMaxRounds("ins-test"); got != defaultGroupChatMaxRounds {
			t.Fatalf("group_chat.max_rounds=%q 区间外/非法 → 期望默认 %d：got %d", raw, defaultGroupChatMaxRounds, got)
		}
	}

	// 纯函数口径同源（不读配置）。
	if got := groupChatMaxRoundsOf(map[string]string{groupChatMaxRoundsKey: "35"}); got != 35 {
		t.Fatalf("groupChatMaxRoundsOf(35) → 35：got %d", got)
	}
	if got := groupChatMaxRoundsOf(map[string]string{groupChatMaxRoundsKey: "99"}); got != defaultGroupChatMaxRounds {
		t.Fatalf("groupChatMaxRoundsOf(99) → 默认：got %d", got)
	}
}
