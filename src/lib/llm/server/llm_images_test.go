// 图片（多模态）输入测试（P2-8）：chat 的 image_url 内容块 / responses 的 input_image、
// 上限与错误语义、历史「仅最近 N 轮保留原图」规则、纯文本路径不回归。
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-router"
)

// testPNG 构造可被魔数识别的 PNG 字节序列（8 字节签名 + 载荷；仅够类型/大小校验用）。
func testPNG(payload string) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte(payload)...)
}

// writeTestFile 在 dir 下写文件并返回绝对路径。
func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatalf("写测试文件: %v", err)
	}
	return p
}

// imageMsg 构造带图片引用的用户消息文本（对齐前端 InputBox.serialize 的 `![名](路径)`）。
func imageMsg(name, path, text string) string {
	m := fmt.Sprintf("![%s](%s)", name, path)
	if text != "" {
		m += "\n" + text
	}
	return m
}

// TestChatImageContentParts（①）：chat 请求体含 image_url 内容块（data URL：mime/大小正确）+ 文本块；
// 无图片消息的 content 仍是字符串（不回归）。
func TestChatImageContentParts(t *testing.T) {
	dir := t.TempDir()
	data := testPNG("hello-image")
	path := writeTestFile(t, dir, "shot.png", data)

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
	}))
	defer srv.Close()

	c := openAITestSpec(srv.URL, "mock")
	evs, err := chatTest(t, c, []ChatMsg{
		{Role: "system", Content: "sys"},
		{Role: "user", Kind: "text", Content: imageMsg("shot.png", path, "这张图里是什么？")},
	}, nil, ChatOptions{Images: ImageOptions{UploadDir: dir}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	for ev := range evs {
		if ev.Err != nil {
			t.Fatalf("stream err: %v", ev.Err)
		}
	}

	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages=%d want 2: %+v", len(msgs), got["messages"])
	}
	m0, _ := msgs[0].(map[string]any)
	if s, ok := m0["content"].(string); !ok || s != "sys" {
		t.Fatalf("system content=%#v want 字符串 \"sys\"（无图片消息不变）", m0["content"])
	}
	m1, _ := msgs[1].(map[string]any)
	parts, ok := m1["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("user content=%#v want 2 个内容块（text + image_url）", m1["content"])
	}
	p0, _ := parts[0].(map[string]any)
	if p0["type"] != "text" || p0["text"] != "[图片: shot.png]\n这张图里是什么？" {
		t.Fatalf("文本块=%+v want {text, [图片: shot.png]\\n这张图里是什么？}", p0)
	}
	p1, _ := parts[1].(map[string]any)
	if p1["type"] != "image_url" {
		t.Fatalf("图片块 type=%v want image_url", p1["type"])
	}
	iu, _ := p1["image_url"].(map[string]any)
	url, _ := iu["url"].(string)
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	if url != want {
		t.Fatalf("data URL 不符:\n got %q\nwant %q", url, want)
	}
	if dec, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,")); err != nil || len(dec) != len(data) {
		t.Fatalf("data URL 载荷=%d 字节 want %d（err=%v）", len(dec), len(data), err)
	}
}

// TestResponsesImageInputItem（②）：responses 请求体 input message 的 content 含 input_image 块。
func TestResponsesImageInputItem(t *testing.T) {
	dir := t.TempDir()
	data := testPNG("resp-image")
	path := writeTestFile(t, dir, "shot.png", data)

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		responsesSSE(w, []map[string]any{responsesTextDelta("ok"), responsesCompleted()})
	}))
	defer srv.Close()

	spec := router.Spec{Name: "test", Protocol: ProtocolResponses, BaseURL: srv.URL, DefaultModel: "mock"}
	evs, err := chatTest(t, spec, []ChatMsg{
		{Role: "system", Content: "sys"},
		{Role: "user", Kind: "text", Content: imageMsg("shot.png", path, "描述这张图")},
	}, nil, ChatOptions{Images: ImageOptions{UploadDir: dir}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if _, _, _, _, serr := collectStreamEvents(evs); serr != nil {
		t.Fatalf("stream err: %v", serr)
	}

	input, _ := got["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input items=%d want 1（system 吸收为 instructions）: %+v", len(input), got["input"])
	}
	msg, _ := input[0].(map[string]any)
	content, _ := msg["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("input[0].content=%+v want 2 块（input_text + input_image）", msg["content"])
	}
	c0, _ := content[0].(map[string]any)
	if c0["type"] != "input_text" || c0["text"] != "[图片: shot.png]\n描述这张图" {
		t.Fatalf("文本块=%+v", c0)
	}
	c1, _ := content[1].(map[string]any)
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	if c1["type"] != "input_image" || c1["image_url"] != want {
		t.Fatalf("图片块=%+v want {input_image, %s}", c1, want)
	}
}

// TestChatImageErrors（③）：缺失文件 / 类型不支持 / 内容不可识别 / 超限 → 明确报错
// （*LLMError{Kind: ErrProtocol, Retryable:false}），且**不发起 HTTP 请求**（不静默丢）。
func TestChatImageErrors(t *testing.T) {
	dir := t.TempDir()
	ok := writeTestFile(t, dir, "ok.png", testPNG("x"))
	big := writeTestFile(t, dir, "big.png", testPNG(strings.Repeat("a", maxImageBytes+1)))
	txt := writeTestFile(t, dir, "note.txt", []byte("not an image"))
	fake := writeTestFile(t, dir, "fake.png", []byte("not an image at all"))

	cases := []struct {
		name string
		msgs []ChatMsg
		want string
	}{
		{
			name: "文件缺失",
			msgs: []ChatMsg{{Role: "user", Kind: "text", Content: imageMsg("gone.png", filepath.Join(dir, "gone.png"), "")}},
			want: "不可读",
		},
		{
			name: "类型不支持",
			msgs: []ChatMsg{{Role: "user", Kind: "text", Content: imageMsg("note.txt", txt, "")}},
			want: "类型不支持",
		},
		{
			name: "内容不可识别",
			msgs: []ChatMsg{{Role: "user", Kind: "text", Content: imageMsg("fake.png", fake, "")}},
			want: "不可识别",
		},
		{
			name: "单图超限",
			msgs: []ChatMsg{{Role: "user", Kind: "text", Content: imageMsg("big.png", big, "")}},
			want: "过大",
		},
		{
			name: "单条消息超限",
			msgs: []ChatMsg{{Role: "user", Kind: "text", Content: imageMsg("ok.png", ok, "") + "\n" +
				imageMsg("ok.png", ok, "") + "\n" + imageMsg("ok.png", ok, "") + "\n" +
				imageMsg("ok.png", ok, "") + "\n" + imageMsg("ok.png", ok, "")}},
			want: "单条消息图片数量超限",
		},
		{
			name: "单次请求超限",
			msgs: []ChatMsg{
				{Role: "user", Kind: "text", Content: strings.Join([]string{
					imageMsg("ok.png", ok, ""), imageMsg("ok.png", ok, ""), imageMsg("ok.png", ok, "")}, "\n")},
				{Role: "assistant", Content: "a1"},
				{Role: "user", Kind: "text", Content: strings.Join([]string{
					imageMsg("ok.png", ok, ""), imageMsg("ok.png", ok, ""), imageMsg("ok.png", ok, "")}, "\n")},
				{Role: "assistant", Content: "a2"},
				{Role: "user", Kind: "text", Content: strings.Join([]string{
					imageMsg("ok.png", ok, ""), imageMsg("ok.png", ok, ""), imageMsg("ok.png", ok, "")}, "\n")},
			},
			want: "单次请求图片数量超限",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&hits, 1)
				llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
			}))
			defer srv.Close()

			spec := openAITestSpec(srv.URL, "mock")
			_, err := chatTest(t, spec, tc.msgs, nil, ChatOptions{Images: ImageOptions{UploadDir: dir}})
			if err == nil {
				t.Fatalf("应返回错误（%s）", tc.want)
			}
			var le *LLMError
			if !errors.As(err, &le) {
				t.Fatalf("错误类型=%T want *LLMError: %v", err, err)
			}
			if le.Kind != ErrProtocol || le.Retryable {
				t.Fatalf("错误分类=%s retryable=%v want protocol/false", le.Kind, le.Retryable)
			}
			if !strings.Contains(le.Message, tc.want) {
				t.Fatalf("错误文案=%q 应含 %q", le.Message, tc.want)
			}
			if n := atomic.LoadInt32(&hits); n != 0 {
				t.Fatalf("校验失败不应发起 HTTP 请求（hits=%d）", n)
			}
		})
	}
}

// TestImageRecentTurnsRule（④）：仅最近 KeepTurns 轮保留原图，更早轮次的引用降级为文本占位；
// 上传根之外的本地路径原样保留为文本（不读文件、不报错）。
func TestImageRecentTurnsRule(t *testing.T) {
	dir := t.TempDir()
	old := writeTestFile(t, dir, "old.png", testPNG("old"))
	cur := writeTestFile(t, dir, "cur.png", testPNG("cur"))
	outside := writeTestFile(t, t.TempDir(), "out.png", testPNG("out"))

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
	}))
	defer srv.Close()

	evs, err := chatTest(t, openAITestSpec(srv.URL, "mock"), []ChatMsg{
		{Role: "user", Kind: "text", Content: imageMsg("old.png", old, "上一轮")},
		{Role: "assistant", Content: "上一轮答复"},
		{Role: "user", Kind: "text", Content: imageMsg("out.png", outside, "根外路径")},
		{Role: "assistant", Content: "根外答复"},
		{Role: "user", Kind: "text", Content: imageMsg("cur.png", cur, "本轮")},
	}, nil, ChatOptions{Images: ImageOptions{UploadDir: dir, KeepTurns: 1}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	for ev := range evs {
		if ev.Err != nil {
			t.Fatalf("stream err: %v", ev.Err)
		}
	}

	msgs, _ := got["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("messages=%d want 5", len(msgs))
	}
	// 早前轮次（turn 0）：引用降级为文本占位，content 仍是字符串
	m0, _ := msgs[0].(map[string]any)
	if s, ok := m0["content"].(string); !ok || s != "[图片: old.png]\n上一轮" {
		t.Fatalf("早前轮次 content=%#v want 占位字符串", m0["content"])
	}
	// 上传根之外：原样保留 markdown 文本（不当附件读取）
	m2, _ := msgs[2].(map[string]any)
	if s, ok := m2["content"].(string); !ok || s != imageMsg("out.png", outside, "根外路径") {
		t.Fatalf("根外路径 content=%#v want 原文", m2["content"])
	}
	// 本轮（turn 1）：展开为内容块
	m4, _ := msgs[4].(map[string]any)
	parts, ok := m4["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("本轮 content=%#v want 2 块", m4["content"])
	}
	p1, _ := parts[1].(map[string]any)
	iu, _ := p1["image_url"].(map[string]any)
	if want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(testPNG("cur"))); iu["url"] != want {
		t.Fatalf("本轮 data URL 不符: %v", iu["url"])
	}
}

// TestTurnSendsImagesEndToEnd：端到端接线（turn.go → ChatOptions.Images）——真实轮次里，
// 用户消息中的附件引用（上传目录内）被展开为 image_url 内容块（data URL）。
// 上传目录 = prjusr 数据根 tmp/uploads（instance 未带 data_dir → `~/.chonkpilot/data/<prj-id>`，
// 测试内由 persist 的 UsrPath 隔离到临时数据主目录；与桥 gui.upload 落点同源，见 24 §3.2 MW-8）。
func TestTurnSendsImagesEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
	}))
	defer llm.Close()

	s := newTestServer(t, llm)
	prjUsrRoot, err := data.PrjUsrDir(testWorkDir, "")
	if err != nil {
		t.Fatalf("PrjUsrDir: %v", err)
	}
	upDir := filepath.Join(prjUsrRoot, "tmp", "uploads")
	if err := os.MkdirAll(upDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	pngData := testPNG("e2e")
	imgPath := writeTestFile(t, upDir, "shot.png", pngData)

	startTurn(t, s, "s-img", "t-img")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-img", "turn": "t-img",
		"type": "text-user", "content": imageMsg("shot.png", imgPath, "看图"),
	}))
	evs := collectTurn(t, s.bus, "t-img", 10*time.Second)
	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("no llm request recorded")
	}
	msgs, _ := bodies[len(bodies)-1]["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("messages empty: %+v", bodies[len(bodies)-1])
	}
	last, _ := msgs[len(msgs)-1].(map[string]any)
	parts, ok := last["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("末条消息 content=%#v want 2 个内容块（text + image_url）", last["content"])
	}
	p1, _ := parts[1].(map[string]any)
	iu, _ := p1["image_url"].(map[string]any)
	if want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData); iu["url"] != want {
		t.Fatalf("端到端 data URL 不符: got %v", iu["url"])
	}
}

// TestImagesDisabledPlainText（⑤ 不回归）：UploadDir 为空 → 引用原样作为文本发送（无内容块）。
func TestImagesDisabledPlainText(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "shot.png", testPNG("x"))

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
	}))
	defer srv.Close()

	content := imageMsg("shot.png", path, "hi")
	evs, err := chatTest(t, openAITestSpec(srv.URL, "mock"),
		[]ChatMsg{{Role: "user", Kind: "text", Content: content}}, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	for ev := range evs {
		if ev.Err != nil {
			t.Fatalf("stream err: %v", ev.Err)
		}
	}
	msgs, _ := got["messages"].([]any)
	m0, _ := msgs[0].(map[string]any)
	if s, ok := m0["content"].(string); !ok || s != content {
		t.Fatalf("content=%#v want 原样字符串（未配置上传根不回退展开）", m0["content"])
	}
}
