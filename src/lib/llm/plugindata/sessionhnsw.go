// sessionhnsw.go —— 语义检索的 HNSW 加速路径（SESSION-SEMANTIC-1 阶段 2）。
//
// 仅在「无过滤条件的 semantic 检索」下启用（session_id / roles / since 任一存在 → 走精确扫描，
// 避免近似召回被过滤条件放大漏检）。图按 **workdir+user** 建一次并缓存：
//
//	节点 key = <dialog_id>\x00<message_id>，向量 = session_vectors 里同 model 的行。
//
// 全量索引写入新向量 → invalidateHNSW 丢弃图，下次检索重建。返回 `approx=true` 表明结果来自
// 近似检索；预算 / 过滤条件需要精确结果时应改用 keyword/hybrid 或带 session_id 过滤。
package plugindata

import (
	"context"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-llm/plugindata/hnswlite"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// hnswEntry 是某个 workdir+user 的全局近似索引快照。
type hnswEntry struct {
	graph *hnswlite.Graph[string]
	model string
	count int
	dims  int
}

type hnswHit struct {
	user      string
	dialogID  string
	messageID string
	score     float64
}

// ensureHNSW 取（必要时重建）workdir+user 的 HNSW 图；向量数不足 threshold → (nil,false)。
func (s *Store) ensureHNSW(workDir, user, model string, threshold int) (*hnswEntry, bool) {
	key := semanticKey(workDir, user)
	rt := s.semanticRuntime()
	rt.hnswMu.Lock()
	defer rt.hnswMu.Unlock()
	if e := rt.hnsw[key]; e != nil && e.model == model {
		return e, e.graph != nil
	}
	entry := &hnswEntry{model: model}
	graph := hnswlite.NewGraph[string]()
	graph.EfSearch = 64
	dir, err := s.targetUserDir(workDir, user)
	if err == nil {
		ids, derr := dialogIDs(dir)
		if derr == nil {
			for _, dialogID := range ids {
				db, release, oerr := openDialog(dir, dialogID)
				if oerr != nil {
					continue
				}
				vecs, verr := loadVectors(db)
				release()
				if verr != nil {
					continue
				}
				for msgID, vrec := range vecs {
					if sval(vrec, "model") != model {
						continue
					}
					dim := recInt(vrec, "dim")
					if dim <= 0 {
						continue
					}
					vec, ok := decodeVector(sval(vrec, "vec"), dim)
					if !ok || len(vec) == 0 {
						continue
					}
					if entry.dims == 0 {
						entry.dims = len(vec)
					}
					if len(vec) != entry.dims {
						continue
					}
					graph.Add(hnswlite.MakeNode(dialogID+"\x00"+msgID, vec))
					entry.count++
				}
			}
		}
	}
	if entry.count >= threshold {
		entry.graph = graph
	}
	// 缓存上限：超出后整体丢弃（图可重建；避免进程内无界增长）。
	if len(rt.hnsw) >= 128 {
		rt.hnsw = map[string]*hnswEntry{}
	}
	rt.hnsw[key] = entry
	return entry, entry.graph != nil
}

// searchSemanticHNSW 走 HNSW 检索；ok=false 表示当前语料不足 / 无图 → 调用方回落精确扫描。
func (s *Store) searchSemanticHNSW(ctx context.Context, req SessionSearchRequest,
	users []string, limit, ctxRunes int, opt *SemanticOptions) (SessionSearchResult, bool, error) {

	// 先确认每个目标用户都有可用图；不足阈值 → 直接回落精确扫描（**不要**先嵌查询，
	// 否则回落路径会重复一次 embedding 请求）。
	entries := make([]*hnswEntry, 0, len(users))
	for _, user := range users {
		entry, ok := s.ensureHNSW(req.WorkDir, user, opt.Model, opt.HNSWThreshold)
		if !ok {
			return SessionSearchResult{}, false, nil
		}
		entries = append(entries, entry)
	}

	qvecs, err := embedTexts(ctx, opt, []string{strings.TrimSpace(req.Query)})
	if err != nil {
		return SessionSearchResult{}, false, &pluginbridge.Error{
			Code: "semantic_unavailable", Msg: "embedding 服务不可用: " + err.Error(),
		}
	}
	qvec := qvecs[0]
	if len(qvec) == 0 {
		return SessionSearchResult{}, false, &pluginbridge.Error{
			Code: "semantic_unavailable", Msg: "embedding 返回空向量",
		}
	}

	k := limit * 20
	if k < 200 {
		k = 200
	}
	cur := s.currentUser()
	var raw []hnswHit
	for i, user := range users {
		entry := entries[i]
		n := k
		if n > entry.count {
			n = entry.count
		}
		for _, node := range entry.graph.Search(qvec, n) {
			score := 1 - float64(entry.graph.Distance(qvec, node.Value))
			if score < 0 {
				score = 0
			}
			if score < opt.MinScore {
				continue
			}
			parts := strings.SplitN(node.Key, "\x00", 2)
			if len(parts) != 2 {
				continue
			}
			raw = append(raw, hnswHit{user: user, dialogID: parts[0], messageID: parts[1], score: score})
		}
	}
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].score > raw[j].score })

	// 按对话分组取消息（同一对话只开一次库）。
	byDialog := map[string][]hnswHit{}
	order := make([]string, 0, len(raw))
	for _, h := range raw {
		key := h.user + "\x00" + h.dialogID
		if _, ok := byDialog[key]; !ok {
			order = append(order, key)
		}
		byDialog[key] = append(byDialog[key], h)
	}

	hits := make([]SessionSearchHit, 0, len(raw))
	scannedSessions := 0
	for _, key := range order {
		if ctx.Err() != nil {
			break
		}
		parts := strings.SplitN(key, "\x00", 2)
		user, dialogID := parts[0], parts[1]
		dir, derr := s.targetUserDir(req.WorkDir, user)
		if derr != nil {
			continue
		}
		db, release, oerr := openDialog(dir, dialogID)
		if oerr != nil {
			continue
		}
		recs, rerr := sessionRows(db, dialogID)
		if rerr != nil {
			release()
			continue
		}
		titles := make(map[string]string, len(recs))
		for _, rec := range recs {
			titles[sval(rec, "session_id")] = sval(rec, "title")
		}
		for _, h := range byDialog[key] {
			var rec data.Record
			ok, gerr := db.Table(tableMessages).Get(h.messageID, &rec)
			if gerr != nil || !ok {
				continue
			}
			role := sval(rec, "role")
			if role != "user" && role != "assistant" {
				continue
			}
			content := sval(rec, "content")
			if strings.TrimSpace(content) == "" {
				continue
			}
			sid := sval(rec, "session_id")
			score := h.score
			if h.user == cur {
				score *= 1.10
			}
			hits = append(hits, SessionSearchHit{
				User: h.user, SessionID: sid, DialogID: dialogID, Title: titles[sid],
				TurnID: sval(rec, "turn_id"), MessageID: h.messageID, Role: role,
				Kind: sval(rec, "kind"), CreatedAt: sval(rec, "created_at"),
				Snippet: headSnippet(content, ctxRunes*2), Score: score, SemanticScore: h.score,
				Self:      req.SelfSession != "" && sid == req.SelfSession,
				Top:       req.TopSession != "" && sid == req.TopSession,
				CrossUser: h.user != cur,
			})
		}
		release()
		scannedSessions++
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].CreatedAt > hits[j].CreatedAt
	})
	total := len(hits)
	truncated := false
	if total > limit {
		hits = hits[:limit]
		truncated = true
	}
	scannedMessages := 0
	for _, u := range users {
		if e := rtForSearch(s, req.WorkDir, u, opt.Model); e != nil {
			scannedMessages += e.count
		}
	}
	return SessionSearchResult{
		Hits: hits, Total: total, ScannedUsers: len(users),
		ScannedSessions: scannedSessions, ScannedMessages: scannedMessages,
		Truncated: truncated, SearchMode: "semantic", Approx: true,
	}, true, nil
}

// rtForSearch 只读缓存里的图计数（不触发重建）。
func rtForSearch(s *Store, workDir, user, model string) *hnswEntry {
	rt := s.semanticRuntime()
	rt.hnswMu.Lock()
	defer rt.hnswMu.Unlock()
	e := rt.hnsw[semanticKey(workDir, user)]
	if e == nil || e.model != model {
		return nil
	}
	return e
}
