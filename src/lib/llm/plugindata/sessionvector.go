// sessionvector.go —— 会话消息的向量语义检索（SESSION-SEMANTIC-1，2026-11-15）。
//
// 设计（与 [2J §9] 一致）：
//   - **默认关闭**：`plugins.session.semantic.enabled=false`；显式开启才走 embedding。
//   - 后端 = 任意 OpenAI 兼容 `/embeddings`（base-url + api-key + model 由配置提供，
//     不绑定 LLM provider 协议，便于对接 qianfan / OpenAI / 本地服务）。
//   - 向量落点 = **每个对话库的 `session_vectors` 表**（一树一库，天然按用户/项目隔离）：
//     key = message_id；行 = {session_id, turn_id, role, model, dim, vec(base64 LE float32)}。
//   - **惰性补索引**：每次 semantic/hybrid 检索最多补 `max-embed-per-call` 条（默认 64），
//     返回 `indexed` 与 `partial=true`；历史消息随多次检索逐步覆盖，不在检索事务内发网络请求。
//   - 跨用户读取沿用 `resolveTargets`：向量在各自用户的对话库里，public/llm 模式同样可查。
//
// 本文件只被宿主（llm server）调用；插件侧无感（只多一个 `mode` 参数）。
package plugindata

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
)

// sessionVectorTable 是对话库内的向量表名（与 messages 同库；只由本包写）。
const sessionVectorTable = "session_vectors"

const (
	defaultMaxEmbedPerCall  = 64
	maxEmbedPerCall         = 256
	defaultSemanticMinScore = 0.20
	defaultSemanticMaxScan  = 2000
	embedBatchSize          = 16
	embedTimeout            = 30 * time.Second
)

// SemanticOptions 是一次语义检索的后端配置（由服务端从 usr/prj 配置解析后传入）。
type SemanticOptions struct {
	BaseURL         string
	APIKey          string
	Model           string
	MaxEmbedPerCall int
	MinScore        float64
	MaxScan         int
	// AutoIndex 开启后，首次语义检索会在后台全量补索引（缺省 true；server 配置控制）。
	AutoIndex bool
	// HNSWThreshold = 单用户向量数达到该值时启用 HNSW（0 = 512）。
	HNSWThreshold int
}

const defaultHNSWThreshold = 512

// normalize 补齐缺省并限制上限。
func (o *SemanticOptions) normalize() {
	o.BaseURL = strings.TrimSpace(o.BaseURL)
	o.Model = strings.TrimSpace(o.Model)
	if o.MaxEmbedPerCall <= 0 {
		o.MaxEmbedPerCall = defaultMaxEmbedPerCall
	}
	if o.MaxEmbedPerCall > maxEmbedPerCall {
		o.MaxEmbedPerCall = maxEmbedPerCall
	}
	if o.MinScore <= 0 {
		o.MinScore = defaultSemanticMinScore
	}
	if o.MaxScan <= 0 {
		o.MaxScan = defaultSemanticMaxScan
	}
	if o.HNSWThreshold <= 0 {
		o.HNSWThreshold = defaultHNSWThreshold
	}
}

// ready 判定后端是否可用（缺 base-url / model → 不可用）。
func (o *SemanticOptions) ready() bool {
	return o != nil && strings.TrimSpace(o.BaseURL) != "" && strings.TrimSpace(o.Model) != ""
}

// ── 向量编解码 ────────────────────────────────────────────

func encodeVector(v []float32) string {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func decodeVector(s string, dim int) ([]float32, bool) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || dim <= 0 || len(raw) != dim*4 {
		return nil, false
	}
	out := make([]float32, dim)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out, true
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// vectorRow 是 session_vectors 表的一行（vec 为 base64）。
type vectorRow struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	Role      string `json:"role,omitempty"`
	Model     string `json:"model"`
	Dim       int    `json:"dim"`
	Vec       string `json:"vec"`
}

// loadVectors 单事务读出该对话库全部向量（message_id → row）。
func loadVectors(db *data.DB) (map[string]data.Record, error) {
	out := map[string]data.Record{}
	err := db.Table(sessionVectorTable).ForEach(func(key string, rec data.Record) error {
		out[key] = rec
		return nil
	})
	return out, err
}

// saveVectors 把新向量写回对话库（幂等 upsert；调用方保证 db 可用）。
func saveVectors(db *data.DB, rows map[string]vectorRow) error {
	if len(rows) == 0 {
		return nil
	}
	tbl := db.Table(sessionVectorTable)
	for mid, row := range rows {
		if err := tbl.Upsert(mid, data.Record{
			"session_id": row.SessionID, "turn_id": row.TurnID, "role": row.Role,
			"model": row.Model, "dim": row.Dim, "vec": row.Vec,
			"created_at": time.Now().UTC().Format(data.RFC3339FixedNano),
		}); err != nil {
			return err
		}
	}
	return nil
}

// ── embedding 客户端 ─────────────────────────────────────

type embedResponse struct {
	Data []struct {
		Embedding json.RawMessage `json:"embedding"`
	} `json:"data"`
	Error any `json:"error"`
}

// embedTexts 调 OpenAI 兼容 `/embeddings`（批量；失败降级逐条）。
// 返回向量与**成功条目的下标**（失败条目省略）。
func embedTexts(ctx context.Context, opt *SemanticOptions, texts []string) (map[int][]float32, error) {
	if len(texts) == 0 {
		return map[int][]float32{}, nil
	}
	vecs := map[int][]float32{}
	var firstErr error
	for start := 0; start < len(texts); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]
		got, err := embedBatch(ctx, opt, batch)
		if err == nil {
			for i, v := range got {
				vecs[start+i] = v
			}
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
		// 批量失败 → 逐条重试（部分服务只接受单条 input）。
		for i, text := range batch {
			one, oerr := embedBatch(ctx, opt, []string{text})
			if oerr == nil && len(one) == 1 {
				vecs[start+i] = one[0]
			}
		}
	}
	if len(vecs) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return vecs, nil
}

func embedBatch(ctx context.Context, opt *SemanticOptions, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": opt.Model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embeddingsURL(opt.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(opt.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(opt.APIKey))
	}
	client := &http.Client{Timeout: embedTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings HTTP %d: %s", resp.StatusCode, truncateString(string(raw), 200))
	}
	var out embedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("embeddings 应答解析失败: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings 返回条数不匹配（want %d got %d）", len(texts), len(out.Data))
	}
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		v, err := decodeEmbedding(d.Embedding)
		if err != nil {
			return nil, err
		}
		vecs[i] = v
	}
	return vecs, nil
}

// decodeEmbedding 兼容两种应答形态：float 数组（OpenAI）与 base64 float32（部分国产服务）。
func decodeEmbedding(raw json.RawMessage) ([]float32, error) {
	var nums []float64
	if err := json.Unmarshal(raw, &nums); err == nil {
		out := make([]float32, len(nums))
		for i, f := range nums {
			out[i] = float32(f)
		}
		return out, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		b, derr := base64.StdEncoding.DecodeString(s)
		if derr != nil || len(b)%4 != 0 {
			return nil, errors.New("embeddings: base64 向量非法")
		}
		out := make([]float32, len(b)/4)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		}
		return out, nil
	}
	return nil, errors.New("embeddings: embedding 类型不支持（float[] / base64）")
}

func embeddingsURL(base string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.Contains(b, "/embeddings") {
		return b
	}
	if strings.HasSuffix(b, "/v1") || strings.HasSuffix(b, "/v2") {
		return b + "/embeddings"
	}
	return b + "/v1/embeddings"
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
