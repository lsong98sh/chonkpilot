package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// 本文件只保留 **SSE 测试素材构造件**（被本包多个用例复用）。
//
// 原 `TestChat*` / `TestEffectiveTimeouts` 系列是针对 llm 侧直连客户端 `LLMClient.Chat` 的白盒，
// LR-11 后出网归 `chonkpilot-router` → 断言已迁到 router 白盒
// （`src/lib/router/internal/adaptor/openai/openai_test.go`、`responses_test.go`、`echo_test.go`
// 与 `src/lib/router/error_test.go`）；llm 侧只保留「装配 + 折算」白盒。

// llmSSE 构造 SSE 响应。
func llmSSE(w http.ResponseWriter, chunks []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func sseChunk(delta map[string]any, finish string) string {
	ch := map[string]any{"choices": []any{
		map[string]any{"delta": delta, "finish_reason": finish},
	}}
	b, _ := json.Marshal(ch)
	return string(b)
}
