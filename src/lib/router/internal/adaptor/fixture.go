// SSE fixture 规范与加载器（LR-5 前置物）—— LR-3 / LR-4 / LR-6 共用的**金样本素材**。
//
// 素材格式（每条 = 一个 `*.sse.json` 文件，落 `testdata/`）：
//
//	{
//	  "name": "openai_text_reason_usage_done",   // 唯一名
//	  "desc": "…",                                // 可选说明
//	  "kind": "sse",                              // "sse"（缺省）| "error"
//	  "chunks": ["data: {…}\n\n", "data: {…"],    // kind=sse：原始分片；可任意切分（含半包）
//	  "payloads": ["{…}", "[DONE]"],              // kind=sse：SSEDecoder 期望载荷序列
//	  "truncated": false,                         // kind=sse：true = 流在事件边界前中断
//	  "body": "<html>…</html>",                   // kind=error：原始响应体（可非 JSON）
//	  "expect_error": "…"                         // kind=error：ErrorMessage 期望值
//	}
//
// 覆盖场景（已落 testdata）：文本分片 · reasoning 分片 · tool_call 按 index 的多段分片 ·
// usage 分片 · `[DONE]` · 非 JSON 错误体 · 跨 chunk 半包 · 流中断。
//
// 说明：`payloads` 是 **SSE 层**的期望（与解析件一一对应）；到 canonical 事件的映射由各协议适配器
// 在 LR-3 / LR-4 断言（本包不替适配器做协议语义）。
package adaptor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Fixture kind 取值。
const (
	KindSSE   = "sse"   // 流式素材：chunks → payloads
	KindError = "error" // 错误体素材：body → expect_error
)

// FixtureSuffix 是素材文件后缀（`LoadDir` 只收以此结尾的文件）。
const FixtureSuffix = ".sse.json"

// Fixture 是一份可复用的 SSE 分片 / 错误体测试素材。
type Fixture struct {
	Name string `json:"name"`
	Desc string `json:"desc,omitempty"`
	Kind string `json:"kind,omitempty"` // 缺省 = KindSSE

	// kind=sse：
	Chunks    []string `json:"chunks,omitempty"`    // 原始分片（可任意切分，含跨行/跨空行半包）
	Payloads  []string `json:"payloads,omitempty"`  // SSEDecoder 期望的 data 载荷序列
	Truncated bool     `json:"truncated,omitempty"` // true = 末尾应得 ErrTruncated

	// kind=error：
	Body        string `json:"body,omitempty"`         // 原始响应体
	ExpectError string `json:"expect_error,omitempty"` // ErrorMessage 期望值
}

// EffectiveKind 返回归一后的素材类型（缺省 KindSSE）。
func (f *Fixture) EffectiveKind() string {
	if f.Kind == "" {
		return KindSSE
	}
	return f.Kind
}

// Load 从一个 JSON 读取素材并做基本校验（name 非空、kind 合法）。
func Load(r io.Reader) (*Fixture, error) {
	var f Fixture
	dec := json.NewDecoder(r)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("解析 fixture: %w", err)
	}
	if err := f.validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// LoadFile 读取单个素材文件。
func LoadFile(path string) (*Fixture, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	f, err := Load(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// LoadDir 读取目录下全部 `*.sse.json` 素材（按文件名升序；目录不存在 → 空切片）。
func LoadDir(dir string) ([]*Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), FixtureSuffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]*Fixture, 0, len(names))
	for _, n := range names {
		f, err := LoadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// validate 做基本校验（不含协议语义）。
func (f *Fixture) validate() error {
	if f.Name == "" {
		return fmt.Errorf("fixture 缺 name")
	}
	switch f.EffectiveKind() {
	case KindSSE:
		if len(f.Chunks) == 0 {
			return fmt.Errorf("fixture %q: kind=sse 需至少一个 chunk", f.Name)
		}
		if len(f.Payloads) == 0 {
			return fmt.Errorf("fixture %q: kind=sse 需 payloads", f.Name)
		}
	case KindError:
		if f.Body == "" {
			return fmt.Errorf("fixture %q: kind=error 需 body", f.Name)
		}
	default:
		return fmt.Errorf("fixture %q: 未知 kind %q", f.Name, f.Kind)
	}
	return nil
}
