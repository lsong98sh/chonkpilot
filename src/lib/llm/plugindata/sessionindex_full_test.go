// sessionindex_full_test.go —— 全量向量索引 + HNSW 路径（SESSION-SEMANTIC-1 阶段 2）。
package plugindata

import (
	"context"
	"testing"
	"time"
)

// waitIndexReady 轮询 EnsureSemanticIndex 直到非 indexing（ready / error）。
func waitIndexReady(t *testing.T, s *Store, opt *SemanticOptions) *VectorIndexStatus {
	t.Helper()
	return waitIndexReadyUser(t, s, "alice", opt)
}

func waitIndexReadyUser(t *testing.T, s *Store, user string, opt *SemanticOptions) *VectorIndexStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := s.EnsureSemanticIndex(testWorkDir, user, opt)
		if st != nil && st.State != "indexing" {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("全量索引（%s）10s 内未完成", user)
	return nil
}

func TestFullVectorIndexIndexesAllUserMessages(t *testing.T) {
	emb, _ := fakeEmbeddingServer(t)
	s, _ := seedSemanticCorpus(t)
	opt := &SemanticOptions{
		BaseURL: emb.URL, Model: "test-embed", AutoIndex: true, MaxEmbedPerCall: 10,
	}
	st := waitIndexReady(t, s, opt)
	if st.State != "ready" || st.NewVectors != 2 || st.IndexedMessages != 2 || st.TotalMessages != 2 {
		t.Fatalf("全量索引计数不符：%+v", st)
	}
	if st.TotalDialogs != 1 || st.DoneDialogs != 1 {
		t.Fatalf("对话计数不符：%+v", st)
	}

	// 只索引当前用户（alice）；bob 的对话库不应有向量。
	dir, _ := s.targetUserDir(testWorkDir, "alice")
	db, release, err := openDialog(dir, "d-sem-alice")
	if err != nil {
		t.Fatalf("open alice: %v", err)
	}
	vecs, _ := loadVectors(db)
	release()
	if len(vecs) != 2 {
		t.Fatalf("alice 向量数=%d, want 2", len(vecs))
	}
	bdir, _ := s.targetUserDir(testWorkDir, "bob")
	bdb, brelease, err := openDialog(bdir, "d-sem-bob")
	if err != nil {
		t.Fatalf("open bob: %v", err)
	}
	bvecs, _ := loadVectors(bdb)
	brelease()
	if len(bvecs) != 0 {
		t.Fatalf("bob 不应被当前用户全量索引（%d 条）", len(bvecs))
	}

	// 同 model 已 ready → 不再起任务（计数不变）。
	again := s.EnsureSemanticIndex(testWorkDir, "alice", opt)
	if again.State != "ready" || again.StartedAt != st.StartedAt {
		t.Fatalf("ready 任务应复用：%+v / %+v", st, again)
	}
}

func TestSemanticHNSWPathAndFilterFallback(t *testing.T) {
	emb, _ := fakeEmbeddingServer(t)
	s, _ := seedSemanticCorpus(t)
	opt := &SemanticOptions{
		BaseURL: emb.URL, Model: "test-embed", AutoIndex: true,
		MinScore: 0.1, HNSWThreshold: 1, MaxEmbedPerCall: 10,
	}

	// 首次检索可能仍在建索引 → 走精确扫描；关键是索引完成后无过滤检索应走 HNSW。
	first, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "什么时候交付",
		SearchMode: "semantic", Semantic: opt,
	})
	if err != nil {
		t.Fatalf("first semantic: %v", err)
	}
	if len(first.Hits) == 0 {
		t.Fatalf("首次检索应有命中：%+v", first)
	}
	if st := waitIndexReady(t, s, opt); st.State != "ready" {
		t.Fatalf("索引未 ready：%+v", st)
	}

	res, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "什么时候交付",
		SearchMode: "semantic", Semantic: opt,
	})
	if err != nil {
		t.Fatalf("hnsw semantic: %v", err)
	}
	if !res.Approx || res.SearchMode != "semantic" {
		t.Fatalf("应走 HNSW 近似路径：%+v", res)
	}
	if len(res.Hits) != 1 || res.Hits[0].MessageID != "t-sem-1-m1" {
		t.Fatalf("HNSW 命中不符：%+v", res.Hits)
	}
	if res.Hits[0].SemanticScore < 0.9 {
		t.Fatalf("HNSW 语义分异常：%+v", res.Hits[0])
	}
	if res.Index == nil || res.Index.State != "ready" {
		t.Fatalf("应回传全量索引状态：%+v", res.Index)
	}
	if res.ScannedMessages != 2 {
		t.Fatalf("HNSW scanned_messages 应为图节点数 2，got %d", res.ScannedMessages)
	}

	// 带 session_id 过滤 → 近似召回可能漏检，必须回落精确扫描。
	filtered, err := s.SearchSessions(context.Background(), SessionSearchRequest{
		WorkDir: testWorkDir, Mode: "private", Query: "什么时候交付", SessionID: "s-sem",
		SearchMode: "semantic", Semantic: opt,
	})
	if err != nil {
		t.Fatalf("filtered semantic: %v", err)
	}
	if filtered.Approx {
		t.Fatalf("带过滤条件不应走 HNSW：%+v", filtered)
	}
	if len(filtered.Hits) != 1 || filtered.Hits[0].SessionID != "s-sem" {
		t.Fatalf("过滤结果不符：%+v", filtered.Hits)
	}
}

// TestVectorIndexStatusIsolatedPerUser 确保 key 含 user（不同用户互不覆盖）。
func TestVectorIndexStatusIsolatedPerUser(t *testing.T) {
	emb, _ := fakeEmbeddingServer(t)
	s, _ := seedSemanticCorpus(t)
	opt := &SemanticOptions{BaseURL: emb.URL, Model: "test-embed", AutoIndex: true}
	if st := s.EnsureSemanticIndex(testWorkDir, "alice", opt); st == nil {
		t.Fatal("alice 应起任务")
	}
	if st := s.EnsureSemanticIndex(testWorkDir, "bob", opt); st == nil {
		t.Fatal("bob 应起独立任务")
	}
	if st := waitIndexReadyUser(t, s, "alice", opt); st.State != "ready" {
		t.Fatalf("alice 索引未完成：%+v", st)
	}
	if st := waitIndexReadyUser(t, s, "bob", opt); st.State != "ready" {
		t.Fatalf("bob 索引未完成：%+v", st)
	}
}
