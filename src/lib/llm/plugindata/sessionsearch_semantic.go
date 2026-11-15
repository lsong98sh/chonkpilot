// sessionsearch_semantic.go —— semantic / hybrid 检索（SESSION-SEMANTIC-1）。
//
// 与 keyword 检索的差异：
//   - 候选消息先在**只读事务内**收集（含向量表），事务外再做 embedding / 写入；
//   - 向量按 `model` 失效（模型变更 → 重新嵌入）；
//   - 每次最多补 `max-embed-per-call` 条（默认 64），返回 `indexed` + `partial`；
//   - semantic 只按 cosine 排序（低于 `min-score` 丢弃）；hybrid = 0.65*cos + 0.35*关键词。
//
// 预算：候选最多 `max-scan`（默认 2000）/ `maxSearchSessions` 个会话；超出 → partial=true。
package plugindata

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

const maxCandidateChars = 4096

// searchCandidate 是待打分的消息（事务外使用；正文已截断）。
type searchCandidate struct {
	User      string
	SessionID string
	DialogID  string
	Title     string
	TurnID    string
	MessageID string
	Role      string
	Kind      string
	Content   string
	CreatedAt string
	Keyword   float64
	Semantic  float64
	VecState  int // 0 = 无向量, 1 = 有可用向量, 2 = 本次新嵌入
}

func normalizeSearchMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "semantic":
		return "semantic"
	case "hybrid":
		return "hybrid"
	default:
		return "keyword"
	}
}

func vecKey(dialogID, messageID string) string { return dialogID + "\x00" + messageID }

// searchSemantic 实现 semantic / hybrid 检索。
func (s *Store) searchSemantic(ctx context.Context, req SessionSearchRequest,
	users []string, mode string, limit, ctxRunes int, roles map[string]bool, floor string) (SessionSearchResult, error) {

	opt := req.Semantic
	if !opt.ready() {
		return SessionSearchResult{}, &pluginbridge.Error{
			Code: "semantic_disabled",
			Msg:  "语义检索未启用或缺少配置（plugins.session.semantic.{enabled,base-url,model}）",
		}
	}
	opt.normalize()
	query := strings.TrimSpace(req.Query)
	lowerQuery, tokens := queryTerms(query)
	cur := s.currentUser()
	// 全量索引：auto-index 开启时后台补齐语料（不阻塞本次检索）；进度随结果回传。
	idxStatus := s.EnsureSemanticIndex(req.WorkDir, cur, opt)

	// HNSW：仅无过滤条件的 semantic 检索（过滤 + 近似召回会放大漏检 → 走精确扫描）。
	if mode == "semantic" && opt.HNSWThreshold > 0 &&
		req.SessionID == "" && req.Since == "" && len(req.Roles) == 0 {
		res, ok, herr := s.searchSemanticHNSW(ctx, req, users, limit, ctxRunes, opt)
		if herr != nil {
			return SessionSearchResult{}, herr
		}
		if ok {
			res.Index = idxStatus
			return res, nil
		}
	}

	var (
		cands           []searchCandidate
		vectors         = map[string][]float32{} // vecKey → 可用向量
		missing         []int                    // cands 下标（待嵌入）
		scannedSessions int
		partial         bool
	)

scanLoop:
	for _, user := range users {
		if ctx.Err() != nil {
			partial = true
			break
		}
		dir, derr := s.targetUserDir(req.WorkDir, user)
		if derr != nil {
			return SessionSearchResult{}, derr
		}
		ids, derr := dialogIDs(dir)
		if derr != nil {
			return SessionSearchResult{}, derr
		}
		for _, dialogID := range ids {
			db, release, oerr := openDialog(dir, dialogID)
			if oerr != nil {
				s.logf("plugindata: semantic 跳过对话库 %s/%s: %v", user, dialogID, oerr)
				continue
			}
			recs, rerr := sessionRows(db, dialogID)
			vrecs, verr := loadVectors(db)
			if rerr != nil || verr != nil {
				release()
				if rerr != nil {
					return SessionSearchResult{}, rerr
				}
				return SessionSearchResult{}, verr
			}
			titles := make(map[string]string, len(recs))
			for _, rec := range recs {
				titles[sval(rec, "session_id")] = sval(rec, "title")
			}
			if req.SessionID != "" {
				scannedSessions++
			} else {
				scannedSessions += len(recs)
			}
			serr := db.Table(tableMessages).ForEach(func(key string, rec data.Record) error {
				if ctx.Err() != nil {
					return errScanStopped
				}
				if len(cands) >= opt.MaxScan {
					return errScanStopped
				}
				role := sval(rec, "role")
				if !roles[role] {
					return nil
				}
				sid := sval(rec, "session_id")
				if req.SessionID != "" && sid != req.SessionID {
					return nil
				}
				created := sval(rec, "created_at")
				if floor != "" && created != "" && created < floor {
					return nil
				}
				content := sval(rec, "content")
				if content == "" {
					return nil
				}
				cands = append(cands, searchCandidate{
					User: user, SessionID: sid, DialogID: dialogID, Title: titles[sid],
					TurnID: sval(rec, "turn_id"), MessageID: key, Role: role,
					Kind: sval(rec, "kind"), Content: truncateString(content, maxCandidateChars),
					CreatedAt: created,
				})
				if vrec, ok := vrecs[key]; ok {
					if sval(vrec, "model") == opt.Model {
						if dim := recInt(vrec, "dim"); dim > 0 {
							if vec, ok := decodeVector(sval(vrec, "vec"), dim); ok {
								vectors[vecKey(dialogID, key)] = vec
								cands[len(cands)-1].VecState = 1
							}
						}
					}
				}
				// 关键词分先在候选上算好（hybrid 用；semantic 也用于 snippet 定位）。
				if idx, ok := keywordScore(content, lowerQuery, tokens); ok {
					cands[len(cands)-1].Keyword = idx.score
				}
				return nil
			})
			release()
			if serr != nil && !errors.Is(serr, errScanStopped) {
				return SessionSearchResult{}, serr
			}
			if errors.Is(serr, errScanStopped) {
				partial = true
				break scanLoop
			}
		}
		if scannedSessions >= maxSearchSessions {
			partial = true
			break
		}
	}

	// 待补向量（按 MaxEmbedPerCall 截断）。
	for i := range cands {
		if cands[i].VecState == 0 {
			if len(missing) >= opt.MaxEmbedPerCall {
				partial = true
				break
			}
			missing = append(missing, i)
		}
	}

	if len(cands) == 0 {
		return SessionSearchResult{
			Hits: []SessionSearchHit{}, Total: 0, ScannedUsers: len(users),
			ScannedSessions: scannedSessions, SearchMode: mode, Index: idxStatus,
		}, nil
	}

	// 查询向量（必须先成功；失败 → semantic_unavailable）。
	qvecs, err := embedTexts(ctx, opt, []string{query})
	if err != nil {
		return SessionSearchResult{}, &pluginbridge.Error{
			Code: "semantic_unavailable", Msg: "embedding 服务不可用: " + err.Error(),
		}
	}
	qvec := qvecs[0]
	if len(qvec) == 0 {
		return SessionSearchResult{}, &pluginbridge.Error{
			Code: "semantic_unavailable", Msg: "embedding 返回空向量",
		}
	}

	// 补索引：批量嵌入缺失消息 → 写回各自对话库（尽力而为，失败只记日志）。
	indexed := 0
	if len(missing) > 0 {
		texts := make([]string, 0, len(missing))
		for _, i := range missing {
			texts = append(texts, truncateString(cands[i].Content, 2000))
		}
		got, eerr := embedTexts(ctx, opt, texts)
		if eerr != nil {
			s.logf("plugindata: semantic 补索引失败（沿用已有向量）: %v", eerr)
			partial = true
		} else {
			byDialog := map[string]map[string]vectorRow{}
			for j, i := range missing {
				v, ok := got[j]
				if !ok {
					continue
				}
				vectors[vecKey(cands[i].DialogID, cands[i].MessageID)] = v
				cands[i].VecState = 2
				indexed++
				key := cands[i].User + "\x00" + cands[i].DialogID
				if byDialog[key] == nil {
					byDialog[key] = map[string]vectorRow{}
				}
				byDialog[key][cands[i].MessageID] = vectorRow{
					SessionID: cands[i].SessionID, TurnID: cands[i].TurnID,
					Role: cands[i].Role, Model: opt.Model, Dim: len(v), Vec: encodeVector(v),
				}
			}
			for key, rows := range byDialog {
				parts := strings.SplitN(key, "\x00", 2)
				dir, derr := s.targetUserDir(req.WorkDir, parts[0])
				if derr != nil {
					continue
				}
				db, release, oerr := openDialog(dir, parts[1])
				if oerr != nil {
					s.logf("plugindata: semantic 写向量跳过对话库 %s: %v", parts[1], oerr)
					continue
				}
				if werr := saveVectors(db, rows); werr != nil {
					s.logf("plugindata: semantic 写向量失败（%s）: %v", parts[1], werr)
					partial = true
				} else {
					s.invalidateHNSW(semanticKey(req.WorkDir, parts[0]))
				}
				release()
			}
		}
	}

	// 打分。
	maxKw := 0.0
	for i := range cands {
		if cands[i].Keyword > maxKw {
			maxKw = cands[i].Keyword
		}
	}
	hits := make([]SessionSearchHit, 0, len(cands))
	for i := range cands {
		c := &cands[i]
		vec, hasVec := vectors[vecKey(c.DialogID, c.MessageID)]
		if hasVec {
			c.Semantic = cosine(qvec, vec)
			if c.Semantic < 0 {
				c.Semantic = 0
			}
		}
		var score float64
		switch mode {
		case "semantic":
			if !hasVec || c.Semantic < opt.MinScore {
				continue
			}
			score = c.Semantic
		default: // hybrid
			if (!hasVec || c.Semantic < opt.MinScore) && c.Keyword <= 0 {
				continue
			}
			kwNorm := 0.0
			if maxKw > 0 {
				kwNorm = c.Keyword / maxKw
			}
			score = 0.65*c.Semantic + 0.35*kwNorm
		}
		if c.User == cur {
			score *= 1.10 // public / 多用户下"优先自己"
		}
		_, byteIdx := matchScore(c.Content, lowerQuery, tokens)
		snippet := headSnippet(c.Content, ctxRunes*2)
		if byteIdx >= 0 {
			snippet = makeSnippet(c.Content, byteIdx, ctxRunes)
		}
		hits = append(hits, SessionSearchHit{
			User: c.User, SessionID: c.SessionID, DialogID: c.DialogID, Title: c.Title,
			TurnID: c.TurnID, MessageID: c.MessageID, Role: c.Role, Kind: c.Kind,
			CreatedAt: c.CreatedAt, Snippet: snippet, Score: score,
			SemanticScore: c.Semantic, KeywordScore: c.Keyword,
			Self:      req.SelfSession != "" && c.SessionID == req.SelfSession,
			Top:       req.TopSession != "" && c.SessionID == req.TopSession,
			CrossUser: c.User != cur,
		})
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
	return SessionSearchResult{
		Hits: hits, Total: total, ScannedUsers: len(users),
		ScannedSessions: scannedSessions, ScannedMessages: len(cands),
		Truncated: truncated, Partial: partial, SearchMode: mode, Indexed: indexed,
		Index: idxStatus,
	}, nil
}

// keywordScore 返回关键词分与首命中字节偏移。
func keywordScore(content, lowerQuery string, tokens []string) (struct {
	score   float64
	byteIdx int
}, bool) {
	score, idx := matchScore(content, lowerQuery, tokens)
	return struct {
		score   float64
		byteIdx int
	}{score: score, byteIdx: idx}, score > 0
}

// headSnippet 取正文开头 n 个 rune（无关键词命中时的语义 snippet）。
func headSnippet(content string, n int) string {
	if n <= 0 {
		n = 120
	}
	runes := []rune(content)
	if len(runes) <= n {
		return content
	}
	return string(runes[:n]) + "…"
}

// recInt 读 record 里的整数（JSON 解码可能是 float64 / int / int64）。
func recInt(rec data.Record, key string) int {
	switch v := rec[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}
