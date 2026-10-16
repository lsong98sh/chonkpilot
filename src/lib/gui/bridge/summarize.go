// zprompt-summarize：正文摘要生成（能力资产「描述·从正文提取」，v2 专用链路 2026-10-14）。
//
// 消息面契约（frontend/src/api/config.js summarizeContent → gui.zprompt-summarize，61 §1）：
//
//	payload {kind, title?, content} → 立即返回 {ok, started}
//	结果经 mq 事件推送：zprompt-summarized {content, error?}（成功 content = 摘要；失败 content 空 + error）
//
// provider 复用**子系统默认 LLM** `llm.promptOptimise`（与 prompt-optimise 同一路径；后续若引入
// 专用「摘要模型」配置，在此切键即可）。每次发起现读配置（热生效，不缓存到包级变量）。
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// callZpromptSummarize 立即返回（摘要在后台 goroutine 生成并经 zprompt-summarized 推送）。
func callZpromptSummarize(b *Bridge, _ context.Context, params []json.RawMessage) ([]byte, error) {
	var req struct {
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	unmarshalParams(params, &req)
	if strings.TrimSpace(req.Content) == "" {
		b.emitSummarized("", &optimizeError{Code: optimizeCodePromptRequired, Message: "content required"})
		return json.Marshal(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldStarted: false})
	}
	llm, oerr := activeLLM(b)
	if oerr != nil {
		b.emitSummarized("", oerr)
		return json.Marshal(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldStarted: false})
	}
	go b.summarizeStream(req.Kind, req.Title, req.Content, llm)
	return json.Marshal(map[string]any{msgkeys.FieldOk: true, msgkeys.FieldStarted: true})
}

// emitSummarized 下发 zprompt-summarized 事件；err 非 nil 时 content 为空并带 error。
func (b *Bridge) emitSummarized(content string, e *optimizeError) {
	payload := map[string]any{msgkeys.ZpromptSummarizedEventContent: content}
	payload[msgkeys.FieldInstanceId] = b.instanceID
	if e != nil {
		payload[msgkeys.ZpromptSummarizedEventError] = e.Message
	}
	b.EmitFrontend(msgkeys.TopicZpromptSummarized, jsonEnvelope(payload))
}

// summarizeStream 非流式调用 LLM 生成摘要（独立 goroutine，不阻塞桥返回）。
func (b *Bridge) summarizeStream(kind, title, content string, llm optimizeLLM) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	base := strings.TrimRight(llm.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if !strings.HasSuffix(base, "/chat/completions") {
		base += "/chat/completions"
	}

	sys := "You are a summarization assistant. Produce a concise, faithful summary of the given content. Keep the original language. Output ONLY the summary text, no explanations."
	user := content
	if title != "" || kind != "" {
		parts := []string{}
		if kind != "" {
			parts = append(parts, "Type: "+kind)
		}
		if title != "" {
			parts = append(parts, "Title: "+title)
		}
		user = strings.Join(parts, "\n") + "\n\nContent:\n" + content
	}

	reqBody := map[string]any{
		"model": llm.Model,
		"messages": []map[string]string{
			{"role": "system", "content": sys},
			{"role": "user", "content": user},
		},
		"stream":      false,
		"temperature": llm.Temp,
	}
	if llm.MaxOutputToken > 0 {
		reqBody["max_tokens"] = llm.MaxOutputToken
	}
	body, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(body))
	if err != nil {
		b.emitSummarized("", &optimizeError{Code: optimizeCodeRequestBuildFailed, Message: err.Error()})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if llm.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+llm.APIKey)
	}
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		b.emitSummarized("", &optimizeError{Code: optimizeCodeLLMRequestFailed, Message: err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		b.emitSummarized("", &optimizeError{
			Code:    optimizeCodeLLMStatusError,
			Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))),
		})
		return
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		b.emitSummarized("", &optimizeError{Code: optimizeCodeLLMEmptyResponse, Message: err.Error()})
		return
	}
	text := ""
	if len(out.Choices) > 0 {
		text = strings.TrimSpace(out.Choices[0].Message.Content)
	}
	if text == "" {
		b.emitSummarized("", &optimizeError{Code: optimizeCodeLLMEmptyResponse, Message: "LLM 未返回内容"})
		return
	}
	b.emitSummarized(text, nil)
}
