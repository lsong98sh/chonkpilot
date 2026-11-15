// sessionindex_full.go —— 语义向量全量索引（SESSION-SEMANTIC-1 阶段 2）。
//
// 与检索时的惰性补索引互补：
//   - 惰性补索引只覆盖**本次检索命中的候选**（受 max-embed-per-call 限制）；
//   - 全量索引在后台逐用户逐对话扫描全部可索引消息（user / assistant / tool，跳过 system），
//     批量 embedding 后写 `session_vectors`，完成后 state=ready。
//
// 触发：语义检索发现 `plugins.session.semantic.auto-index=true`（缺省）时自动启动一次；同一
// workdir+user 已有 ready 任务（同 model）→ 直接复用。进度随 session_search 的 `index` 字段回传。
package plugindata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
)

// VectorIndexStatus 是某个 workdir+user 的全量向量索引进度（可 JSON 直出）。
type VectorIndexStatus struct {
	State           string `json:"state"` // indexing | ready | error
	Model           string `json:"model,omitempty"`
	TotalDialogs    int    `json:"total_dialogs"`
	DoneDialogs     int    `json:"done_dialogs"`
	TotalMessages   int    `json:"total_messages"`
	IndexedMessages int    `json:"indexed_messages"`
	NewVectors      int    `json:"new_vectors"`
	Error           string `json:"error,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

func (st *VectorIndexStatus) clone() *VectorIndexStatus {
	if st == nil {
		return nil
	}
	cp := *st
	return &cp
}

// semanticRuntime 是语义检索的进程内运行时（全量索引任务 + HNSW 缓存）。
type semanticRuntime struct {
	jobsMu  sync.Mutex
	jobs    map[string]*VectorIndexStatus
	cancels map[string]context.CancelFunc

	hnswMu sync.Mutex
	hnsw   map[string]*hnswEntry
}

func (s *Store) semanticRuntime() *semanticRuntime {
	s.semMu.Lock()
	defer s.semMu.Unlock()
	if s.semRT == nil {
		s.semRT = &semanticRuntime{
			jobs:    map[string]*VectorIndexStatus{},
			cancels: map[string]context.CancelFunc{},
			hnsw:    map[string]*hnswEntry{},
		}
	}
	return s.semRT
}

// semanticKey 归一 workdir+user（大小写 / 尾斜杠差异不重复起任务）。
func semanticKey(workDir, user string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(workDir), `\/`)) + "|" + strings.ToLower(strings.TrimSpace(user))
}

// EnsureSemanticIndex 确保后台全量索引已启动（auto-index）；返回当前状态快照。
//
// ready + 同 model → 不重复；indexing → 复用；error → 下次检索重试（保留已写向量）。
func (s *Store) EnsureSemanticIndex(workDir, user string, opt *SemanticOptions) *VectorIndexStatus {
	if s == nil || !opt.ready() || !opt.AutoIndex {
		return nil
	}
	rt := s.semanticRuntime()
	key := semanticKey(workDir, user)

	rt.jobsMu.Lock()
	if st := rt.jobs[key]; st != nil {
		if st.State == "indexing" || (st.State == "ready" && st.Model == opt.Model) {
			cp := st.clone()
			rt.jobsMu.Unlock()
			return cp
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC().Format(time.RFC3339)
	st := &VectorIndexStatus{
		State: "indexing", Model: opt.Model, StartedAt: now, UpdatedAt: now,
	}
	rt.jobs[key] = st
	rt.cancels[key] = cancel
	cp := st.clone()
	rt.jobsMu.Unlock()

	go s.runSemanticIndex(ctx, key, workDir, user, opt, st)
	return cp
}

// runSemanticIndex 后台执行并落终态。
func (s *Store) runSemanticIndex(ctx context.Context, key, workDir, user string, opt *SemanticOptions, st *VectorIndexStatus) {
	err := s.indexUserVectors(ctx, workDir, user, opt, st)
	rt := s.semanticRuntime()
	rt.jobsMu.Lock()
	defer rt.jobsMu.Unlock()
	if cur := rt.jobs[key]; cur == st {
		if err != nil {
			st.State, st.Error = "error", err.Error()
		} else {
			st.State = "ready"
		}
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	delete(rt.cancels, key)
}

// indexUserVectors 逐对话补齐全量向量。已存在同 model 向量的消息跳过（断点续建）。
func (s *Store) indexUserVectors(ctx context.Context, workDir, user string, opt *SemanticOptions, st *VectorIndexStatus) error {
	rt := s.semanticRuntime()
	dir, err := s.targetUserDir(workDir, user)
	if err != nil {
		return err
	}
	ids, err := dialogIDs(dir)
	if err != nil {
		return err
	}
	rt.jobsMu.Lock()
	st.TotalDialogs = len(ids)
	rt.jobsMu.Unlock()

	for _, dialogID := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.indexDialogVectors(ctx, dir, workDir, user, dialogID, opt, st); err != nil {
			return fmt.Errorf("对话 %s: %w", dialogID, err)
		}
		rt.jobsMu.Lock()
		st.DoneDialogs++
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		rt.jobsMu.Unlock()
	}
	return nil
}

type vectorIndexItem struct {
	key       string
	sessionID string
	turnID    string
	role      string
	content   string
}

func (s *Store) indexDialogVectors(ctx context.Context, dir, workDir, user, dialogID string, opt *SemanticOptions, st *VectorIndexStatus) error {
	db, release, err := openDialog(dir, dialogID)
	if err != nil {
		return nil // 单库打开失败：跳过（不因一个库损坏中断全量）
	}
	existing, verr := loadVectors(db)
	if verr != nil {
		release()
		return verr
	}
	var missing []vectorIndexItem
	indexable, already := 0, 0
	serr := db.Table(tableMessages).ForEach(func(key string, rec data.Record) error {
		if ctx.Err() != nil {
			return errScanStopped
		}
		role := sval(rec, "role")
		if role == "system" {
			return nil
		}
		content := strings.TrimSpace(sval(rec, "content"))
		if content == "" {
			return nil
		}
		indexable++
		if vrec, ok := existing[key]; ok && sval(vrec, "model") == opt.Model && recInt(vrec, "dim") > 0 {
			already++
			return nil
		}
		missing = append(missing, vectorIndexItem{
			key: key, sessionID: sval(rec, "session_id"), turnID: sval(rec, "turn_id"),
			role: role, content: truncateString(content, 2000),
		})
		return nil
	})
	release()
	if serr != nil && !errors.Is(serr, errScanStopped) {
		return serr
	}
	if errors.Is(serr, errScanStopped) && ctx.Err() != nil {
		return ctx.Err()
	}
	rt := s.semanticRuntime()
	rt.jobsMu.Lock()
	st.TotalMessages += indexable
	st.IndexedMessages += already
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rt.jobsMu.Unlock()
	if len(missing) == 0 {
		return nil
	}

	for start := 0; start < len(missing); start += embedBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + embedBatchSize
		if end > len(missing) {
			end = len(missing)
		}
		batch := missing[start:end]
		texts := make([]string, 0, len(batch))
		for _, item := range batch {
			texts = append(texts, item.content)
		}
		vecs, eerr := embedTexts(ctx, opt, texts)
		if eerr != nil {
			return fmt.Errorf("embedding 失败（%d 条）: %w", len(batch), eerr)
		}
		rows := make(map[string]vectorRow, len(batch))
		for i, item := range batch {
			v, ok := vecs[i]
			if !ok || len(v) == 0 {
				continue
			}
			rows[item.key] = vectorRow{
				SessionID: item.sessionID, TurnID: item.turnID, Role: item.role,
				Model: opt.Model, Dim: len(v), Vec: encodeVector(v),
			}
		}
		if len(rows) > 0 {
			db, release, oerr := openDialog(dir, dialogID)
			if oerr != nil {
				return oerr
			}
			werr := saveVectors(db, rows)
			release()
			if werr != nil {
				return werr
			}
			s.invalidateHNSW(semanticKey(workDir, user))
			rt.jobsMu.Lock()
			st.IndexedMessages += len(rows)
			st.NewVectors += len(rows)
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			rt.jobsMu.Unlock()
		}
	}
	return nil
}

// invalidateHNSW 丢弃某个 workdir+user 的 HNSW 图（向量有新增 / 变更后必须重建）。
func (s *Store) invalidateHNSW(key string) {
	rt := s.semanticRuntime()
	rt.hnswMu.Lock()
	delete(rt.hnsw, key)
	rt.hnswMu.Unlock()
}
