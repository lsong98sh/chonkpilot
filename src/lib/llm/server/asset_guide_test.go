// RB-7 白盒：知识库资产检索指引的**门控**（知识库已接入 → 出现；未接入 → 不出现）。
//
// 指引正文须写清与记忆的区别（记忆 = 文件按绝对路径读；资产 = mcp_find + mcp_load），
// 且随每轮请求注入（turn.msgs() 与记忆指引并列）。
package server

import (
	"strings"
	"testing"
)

// TestAssetGuideGatedByKnowledgeBase：门控两条 —— 未接入知识库（无 capability dir 节点）→ 不出现；
// 已接入 → 出现且含使用指引与「与记忆的区别」。
func TestAssetGuideGatedByKnowledgeBase(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm) // 内嵌 gateway（mcp_find/mcp_load 存在）

	// ① 未接入知识库（无 dir 节点）→ 空串（不误导演示 LLM）
	if g := s.assetGuide("ins-kb"); g != "" {
		t.Fatalf("未接入知识库时指引应为空: %q", g)
	}

	// ② 接入知识库（instance 已注册 capability dir 节点）→ 出现
	s.mu.Lock()
	s.dirNodes["ins-kb"] = []string{"ins-kb-user"}
	s.mu.Unlock()
	g := s.assetGuide("ins-kb")
	if !strings.Contains(g, "知识库资产") || !strings.Contains(g, "mcp_find") || !strings.Contains(g, "mcp_load") {
		t.Fatalf("指引应写明资产检索路径: %q", g)
	}
	if !strings.Contains(g, "记忆") {
		t.Fatalf("指引应写清与记忆的区别: %q", g)
	}

	// ③ 门控按实例隔离：未接入的其它实例不受影响
	if g2 := s.assetGuide("ins-other"); g2 != "" {
		t.Fatalf("其它实例不应出现指引: %q", g2)
	}

	// ④ 随每轮请求注入（turn.msgs() 首位 system 段）
	tc := &turnCtx{server: s}
	tc.req.InstanceID = "ins-kb"
	tc.req.Turn = "t-rb7"
	msgs := tc.msgs()
	found := false
	for _, m := range msgs {
		if m.Role == "system" && strings.Contains(m.Content, "知识库资产") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("msgs() 应注入知识库资产指引: %+v", msgs)
	}
}
