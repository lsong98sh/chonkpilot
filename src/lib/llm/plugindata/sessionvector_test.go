// sessionvector_test.go —— SESSION-SEMANTIC-1：embedding 客户端 / 向量落库 / semantic / hybrid。
//
// 后端用 httptest 模拟 OpenAI 兼容 `/embeddings`；向量按文本确定性映射，便于断言排序。
package plugindata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// fakeEmbeddingServer 返回确定性向量：按文本查表，未知 → [0,0,1]。
func fakeEmbeddingServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	table := map[string][]float64{
		"什么时候交付":    {0.95, 0.05, 0},
		"交付时间定在周五":  {1, 0, 0},
		"登录模块报错":    {0, 1, 0},
		"交付时间改到下周一": {0.9, 0.1, 0},
	}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Input any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var inputs []string
		switch v := body.Input.(type) {
		case string:
			inputs = []string{v}
		case []any:
			for _, x := range v {
				inputs = append(inputs, x.(string))
			}
		}
		data := make([]map[string]any, 0, len(inputs))
		for _, text := range inputs {
			vec, ok := table[text]
			if !ok {
				vec = []float64{0, 0, 1}
			}
			data = append(data, map[string]any{"embedding": vec})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// seedSemanticCorpus 造 alice（交付周五 / 登录报错）与 bob（交付下周一）。
func seedSemanticCorpus(t *testing.T) (*Store, string) {
	t.Helper()
	s, usersRoot := newExportStore(t)
	seedDialogDB(t, filepath.Join(usersRoot, "alice"), "d-sem-alice",
		[]seedSession{{id: "s-sem", title: "交付讨论", parentID: "", turns: []seedTurn{
			{id: "t-sem-1", msgs: []seedMsg{
				{"user", "交付时间定在周五"},
				{"assistant", "登录模块报错"},
			}},
		}}})
	seedDialogDB(t, filepath.Join(usersRoot, "bob"), "d-sem-bob",
		[]seedSession{{id: "b-sem", title: "Bob 交付", parentID: "", turns: []seedTurn{
			{id: "t-b-sem-1", msgs: []seedMsg{{"user", "交付时间改到下周一"}}},
		}}})
	return s, usersRoot
}

func TestSemanticSearchOrdersByCosineAndCachesVectors(t *testing.T) {
	emb, requests := fakeEmbeddingServer(t)
	s, _ := seedSemanticCorpus(t)
	opt := &SemanticOptions{BaseURL: emb.URL, Model: "test-embed", MinScore: 0.1, MaxEmbedPerCall: 10}

	res, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "什么时候交付",
		SearchMode: "semantic", Semantic: opt, SelfSession: "s-sem",
	})
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if res.SearchMode != "semantic" {
		t.Fatalf("mode=%q", res.SearchMode)
	}
	if len(res.Hits) != 1 || res.Hits[0].MessageID != "t-sem-1-m1" {
		t.Fatalf("private 语义检索应只命中 alice 的交付消息：%+v", res.Hits)
	}
	if res.Indexed != 2 {
		t.Fatalf("首次应补 2 条向量（周五 + 报错），got %d", res.Indexed)
	}
	if res.Hits[0].SemanticScore < 0.9 || res.Hits[0].Score < 0.9 {
		t.Fatalf("cosine 异常：%+v", res.Hits[0])
	}
	firstRequests := *requests

	// 第二次：向量已缓存 → 只嵌查询，不再补索引。
	res2, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "什么时候交付",
		SearchMode: "semantic", Semantic: opt,
	})
	if err != nil {
		t.Fatalf("semantic search 2: %v", err)
	}
	if res2.Indexed != 0 {
		t.Fatalf("二次检索不应再补向量：indexed=%d", res2.Indexed)
	}
	if *requests != firstRequests+1 {
		t.Fatalf("二次检索应只嵌查询（+1 请求），got %d -> %d", firstRequests, *requests)
	}

	// 向量行落库：alice 对话库里应有 2 行。
	dir, _ := s.targetUserDir(testWorkDir, "alice")
	db, release, err := openDialog(dir, "d-sem-alice")
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	defer release()
	vecs, err := loadVectors(db)
	if err != nil {
		t.Fatalf("load vectors: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("session_vectors 行数=%d, want 2", len(vecs))
	}
}

func TestSemanticSearchPublicCrossUser(t *testing.T) {
	emb, _ := fakeEmbeddingServer(t)
	s, _ := seedSemanticCorpus(t)
	opt := &SemanticOptions{BaseURL: emb.URL, Model: "test-embed", MinScore: 0.1, MaxEmbedPerCall: 10}
	res, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "public", Targets: []string{"*"}, Query: "什么时候交付",
		SearchMode: "semantic", Semantic: opt,
	})
	if err != nil {
		t.Fatalf("semantic public: %v", err)
	}
	if len(res.Hits) < 2 {
		t.Fatalf("public 应命中 alice + bob：%+v", res.Hits)
	}
	if !res.Hits[0].Self && res.Hits[0].User == "bob" {
		t.Fatalf("自己（alice）应排在前：%+v", res.Hits)
	}
	var bobHit bool
	for _, h := range res.Hits {
		if h.User == "bob" && h.CrossUser {
			bobHit = true
		}
	}
	if !bobHit {
		t.Fatalf("缺少 bob 跨用户命中：%+v", res.Hits)
	}
}

func TestHybridSearchMergesKeywordAndSemantic(t *testing.T) {
	emb, _ := fakeEmbeddingServer(t)
	s, _ := seedSemanticCorpus(t)
	opt := &SemanticOptions{BaseURL: emb.URL, Model: "test-embed", MinScore: 0.1, MaxEmbedPerCall: 10}
	res, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "登录", SearchMode: "hybrid", Semantic: opt,
	})
	if err != nil {
		t.Fatalf("hybrid: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("hybrid 应至少命中关键词「登录」")
	}
	if res.Hits[0].KeywordScore <= 0 {
		t.Fatalf("hybrid 命中应带 keyword_score：%+v", res.Hits[0])
	}
}

func TestSemanticDisabledAndUnavailable(t *testing.T) {
	s, _ := seedSemanticCorpus(t)
	_, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "交付", SearchMode: "semantic",
	})
	if got := codeOf(t, err); got != "semantic_disabled" {
		t.Fatalf("未配置应 semantic_disabled：%q %v", got, err)
	}

	bad := &SemanticOptions{BaseURL: "http://127.0.0.1:1", Model: "x", MaxEmbedPerCall: 4}
	_, err = s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "交付", SearchMode: "semantic", Semantic: bad,
	})
	if got := codeOf(t, err); got != "semantic_unavailable" {
		t.Fatalf("服务不可用应 semantic_unavailable：%q %v", got, err)
	}
}

// TestVectorEncodingRoundTrip 覆盖 base64 float32 编解码与 cosine。
func TestVectorEncodingRoundTrip(t *testing.T) {
	v := []float32{0.5, -1, 2.25}
	got, ok := decodeVector(encodeVector(v), 3)
	if !ok || len(got) != 3 || got[0] != 0.5 || got[1] != -1 || got[2] != 2.25 {
		t.Fatalf("编解码失败：%+v ok=%v", got, ok)
	}
	if c := cosine([]float32{1, 0}, []float32{1, 0}); c < 0.999 {
		t.Fatalf("cosine 同向量=%v", c)
	}
	if c := cosine([]float32{1, 0}, []float32{0, 1}); c != 0 {
		t.Fatalf("cosine 正交=%v", c)
	}
}
