// Package indexqueue 是 resident indexer 的**持久化待索引队列**门面实现
// （`chonkpilot-data/facade` 的 IndexQueueAPI；prjusr 级 `index_queue` 表，v10 迁移建桶）。
//
// 定位：filesys 写入（create / write / remove / rename）先入队，resident indexer 按
// `work_dir` + `state=pending`、`created_at` 升序取一批（Take 不改状态 = 至少一次），
// 索引完成后回执（Ack：成功 → done；失败 → attempts+1，累计 3 次转 failed）。
//
// **仅 in-process 使用，暂无 MQ 绑定**：P1 只做 inline 门面（同进程函数调用），不新增
// MQ 主题 / msgkeys；加入 MQ 绑定须先更新 `docs/spec/60-reference/61-消息一览.md` 并经
// 用户确认。
//
// ════════════════════════════════════════════════════════════════════════════
// 表行（snake_case）：
//
//	id / work_dir / path / consumer / operation / state / attempts / last_error /
//	created_at / updated_at
//
// consumer 是扇出维度：同一文件变更对每个 resident 索引引擎各有一行，各自独立
// take / ack / pending。旧版本写入的行没有 consumer 字段 → 视为 legacy（空），
// 任意 consumer 均可见 / 可回执。不新增 migration（bbolt bucket 无列约束）。
// ════════════════════════════════════════════════════════════════════════════
package indexqueue

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

const (
	// tableName 是 prjusr 库中的待索引队列表名（migrate.go v10 建桶）。
	tableName = "index_queue"

	// 行状态。
	statePending = "pending"
	stateDone    = "done"
	stateFailed  = "failed"

	// maxAttempts 是失败重试上限：attempts 累计到该值 → state=failed。
	maxAttempts = 3

	// defaultTakeLimit 是 Take 未带 Limit 时的单批上限。
	defaultTakeLimit = 100

	// defaultKeepDoneSeconds 是 Compact 未带 KeepDoneSeconds 时 done 行的保留秒数。
	defaultKeepDoneSeconds = 3600
)

// Store 是 indexqueue 域门面实现（inline 绑定与后续绑定共用）。
type Store struct {
	*kernel.Base
}

// New 构造 indexqueue 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Store { return &Store{Base: base} }

// 编译期断言：实现完整 indexqueue 域门面（缺方法即编译不过）。
var _ facade.IndexQueueAPI = (*Store)(nil)

// prjUsr 解析队列所在 prjusr 库（与会话同层）。
//
// 门面入参不带 instance_id（P1 仅 filesys / resident indexer 同进程调用），故以 Scope 为准：
// Scope 非空时由 work_dir/data_dir 派生一个稳定实例 id 走 PrjUsrFor 的「Scope 自登记」分支
// ——PrjUsrFor 直接传空 id 时，data.Register 的空键守卫会拒绝登记，Scope 分支解析不出来；
// Scope 为空时交回「唯一实例回退」的既有语义。
func (s *Store) prjUsr(scope facade.Scope) (*data.DB, error) {
	id := ""
	if !scope.Empty() {
		id = "indexqueue|" + scope.WorkDir + "|" + scope.DataDir
	}
	db, err := s.PrjUsrFor(id, scope)
	if err != nil {
		return nil, fmt.Errorf("index_queue: 解析 prjusr 库失败：%w", err)
	}
	return db, nil
}

// IndexQueueAppend 入队一条变更（按 Consumers 扇出）。
//
// 去重：同 (work_dir, path, consumer) 已有 pending 行 → 只更新 operation / updated_at
// （保留原 created_at），不产生重复行；未命中 → 插入新行（pending，attempts=0）。
// Consumers 为空 = 旧行为（单行 consumer=""）。
func (s *Store) IndexQueueAppend(req facade.IndexQueueAppendRequest) (facade.IndexQueueAppendResponse, error) {
	if strings.TrimSpace(req.WorkDir) == "" {
		return facade.IndexQueueAppendResponse{}, errors.New("index_queue: work_dir 不能为空")
	}
	if strings.TrimSpace(req.Path) == "" {
		return facade.IndexQueueAppendResponse{}, errors.New("index_queue: path 不能为空")
	}
	switch req.Operation {
	case "create", "write", "remove", "rename":
	default:
		return facade.IndexQueueAppendResponse{}, fmt.Errorf(
			"index_queue: operation 非法 %q（须为 create|write|remove|rename）", req.Operation)
	}
	consumers := req.Consumers
	if len(consumers) == 0 {
		consumers = []string{""}
	}
	path := normalizePath(req.Path)
	db, err := s.prjUsr(req.Scope)
	if err != nil {
		return facade.IndexQueueAppendResponse{}, err
	}
	t := db.Table(tableName)
	now := time.Now().UTC().Format(data.RFC3339FixedNano)

	// 先取同 (work_dir, path) 的全部 pending 行（含各 consumer），再按 consumer 去重/更新。
	recs, _, err := t.Query(data.Query{
		Where: data.Record{"work_dir": req.WorkDir, "path": path, "state": statePending},
	})
	if err != nil {
		return facade.IndexQueueAppendResponse{}, fmt.Errorf("index_queue: 查询待索引行失败：%w", err)
	}
	existing := make(map[string]data.Record, len(recs))
	for _, r := range recs {
		// 旧行无 consumer 字段 → ""（legacy），与 consumer="" 的写入同键。
		existing[kernel.Sval(r["consumer"])] = r
	}

	ids := make([]string, 0, len(consumers))
	seen := make(map[string]bool, len(consumers))
	for _, consumer := range consumers {
		if seen[consumer] {
			continue
		}
		seen[consumer] = true

		if cur, ok := existing[consumer]; ok {
			id := kernel.Sval(cur[data.KeyField])
			delete(cur, data.KeyField)
			cur["consumer"] = consumer
			cur["operation"] = req.Operation
			cur["updated_at"] = now // created_at 保留原值
			if err := t.Upsert(id, cur); err != nil {
				return facade.IndexQueueAppendResponse{}, fmt.Errorf("index_queue: 更新待索引行 %s 失败：%w", id, err)
			}
			ids = append(ids, id)
			continue
		}

		key, err := newKey()
		if err != nil {
			return facade.IndexQueueAppendResponse{}, err
		}
		rec := data.Record{
			"work_dir":   req.WorkDir,
			"path":       path,
			"consumer":   consumer,
			"operation":  req.Operation,
			"state":      statePending,
			"attempts":   0,
			"created_at": now,
			"updated_at": now,
		}
		if err := t.Insert(key, rec); err != nil {
			return facade.IndexQueueAppendResponse{}, fmt.Errorf("index_queue: 写入待索引行失败：%w", err)
		}
		ids = append(ids, key)
	}
	resp := facade.IndexQueueAppendResponse{OK: true, IDs: ids}
	if len(ids) > 0 {
		resp.ID = ids[0]
	}
	return resp, nil
}

// IndexQueueTake 取一批 pending 行（按 created_at 升序；**不修改状态**）。
//
// consumer 过滤在内存进行（bbolt 无列约束；队列量小）：Consumer 非空 → 只返回
// 本 consumer 的行与 legacy 空行；为空 → 不过滤。
func (s *Store) IndexQueueTake(req facade.IndexQueueTakeRequest) (facade.IndexQueueTakeResponse, error) {
	if strings.TrimSpace(req.WorkDir) == "" {
		return facade.IndexQueueTakeResponse{}, errors.New("index_queue: work_dir 不能为空")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultTakeLimit
	}
	db, err := s.prjUsr(req.Scope)
	if err != nil {
		return facade.IndexQueueTakeResponse{}, err
	}
	recs, _, err := db.Table(tableName).Query(data.Query{
		Where:   data.Record{"work_dir": req.WorkDir, "state": statePending},
		OrderBy: "created_at",
	})
	if err != nil {
		return facade.IndexQueueTakeResponse{}, fmt.Errorf("index_queue: 读取待索引行失败：%w", err)
	}
	items := make([]facade.IndexQueueItem, 0, limit)
	for _, r := range recs {
		if !consumerMatch(req.Consumer, r) {
			continue
		}
		items = append(items, toItem(r))
		if len(items) >= limit {
			break
		}
	}
	return facade.IndexQueueTakeResponse{OK: true, Items: items}, nil
}

// IndexQueueAck 回执一批已消费行。
//
//	Failed=false → state=done；
//	Failed=true  → attempts+1；attempts >= 3 → state=failed + last_error，
//	               否则 state=pending + last_error。
//
// 不存在的 id 跳过（不报整体失败）。
func (s *Store) IndexQueueAck(req facade.IndexQueueAckRequest) (facade.IndexQueueAckResponse, error) {
	db, err := s.prjUsr(req.Scope)
	if err != nil {
		return facade.IndexQueueAckResponse{}, err
	}
	t := db.Table(tableName)
	now := time.Now().UTC().Format(data.RFC3339FixedNano)
	updated := 0
	for _, id := range req.IDs {
		if id == "" {
			continue
		}
		var rec data.Record
		found, err := t.Get(id, &rec)
		if err != nil {
			return facade.IndexQueueAckResponse{}, fmt.Errorf("index_queue: 读取队列行 %s 失败：%w", id, err)
		}
		if !found {
			continue // 已被别处删除 → 跳过，不报整体失败
		}
		// 扇出隔离：Consumer 非空时只回执本 consumer 的行（legacy 空行两侧共享）。
		if req.Consumer != "" {
			c := kernel.Sval(rec["consumer"])
			if c != req.Consumer && c != "" {
				continue
			}
		}
		delete(rec, data.KeyField)
		if req.Failed {
			attempts := intVal(rec["attempts"]) + 1
			rec["attempts"] = attempts
			rec["last_error"] = req.Error
			if attempts >= maxAttempts {
				rec["state"] = stateFailed
			} else {
				rec["state"] = statePending
			}
		} else {
			rec["state"] = stateDone
		}
		rec["updated_at"] = now
		if err := t.Upsert(id, rec); err != nil {
			return facade.IndexQueueAckResponse{}, fmt.Errorf("index_queue: 回执队列行 %s 失败：%w", id, err)
		}
		updated++
	}
	return facade.IndexQueueAckResponse{OK: true, Updated: updated}, nil
}

// IndexQueuePending 统计某 work_dir 的 pending 行数（consumer 过滤语义同 Take）。
func (s *Store) IndexQueuePending(req facade.IndexQueuePendingRequest) (facade.IndexQueuePendingResponse, error) {
	if strings.TrimSpace(req.WorkDir) == "" {
		return facade.IndexQueuePendingResponse{}, errors.New("index_queue: work_dir 不能为空")
	}
	db, err := s.prjUsr(req.Scope)
	if err != nil {
		return facade.IndexQueuePendingResponse{}, err
	}
	recs, _, err := db.Table(tableName).Query(data.Query{
		Where: data.Record{"work_dir": req.WorkDir, "state": statePending},
	})
	if err != nil {
		return facade.IndexQueuePendingResponse{}, fmt.Errorf("index_queue: 统计待索引行失败：%w", err)
	}
	pending := 0
	for _, r := range recs {
		if consumerMatch(req.Consumer, r) {
			pending++
		}
	}
	return facade.IndexQueuePendingResponse{OK: true, Pending: pending}, nil
}

// IndexQueueCompact 删除过期（updated_at 早于 now-KeepDoneSeconds）的 done 行。
// failed / pending 不删；时间不可解析 → 保守保留（宁可留、不误删）。
func (s *Store) IndexQueueCompact(req facade.IndexQueueCompactRequest) (facade.IndexQueueCompactResponse, error) {
	keep := req.KeepDoneSeconds
	if keep <= 0 {
		keep = defaultKeepDoneSeconds
	}
	cutoff := time.Now().UTC().Add(-time.Duration(keep) * time.Second)
	db, err := s.prjUsr(req.Scope)
	if err != nil {
		return facade.IndexQueueCompactResponse{}, err
	}
	t := db.Table(tableName)
	recs, _, err := t.Query(data.Query{Where: data.Record{"state": stateDone}})
	if err != nil {
		return facade.IndexQueueCompactResponse{}, fmt.Errorf("index_queue: 扫描已消费行失败：%w", err)
	}
	removed := 0
	for _, r := range recs {
		ts := kernel.Sval(r["updated_at"])
		if ts == "" {
			continue
		}
		tm, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !tm.Before(cutoff) {
			continue
		}
		id := kernel.Sval(r[data.KeyField])
		if err := t.Delete(id); err != nil && !errors.Is(err, data.ErrNotFound) {
			return facade.IndexQueueCompactResponse{}, fmt.Errorf("index_queue: 删除过期队列行 %s 失败：%w", id, err)
		}
		removed++
	}
	return facade.IndexQueueCompactResponse{OK: true, Removed: removed}, nil
}

// normalizePath 归一文件路径为 '/' 分隔（Windows 反斜杠 → '/'）。
func normalizePath(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// consumerMatch 判断行是否对请求方 consumer 可见：
// reqConsumer 为空 → 不过滤；否则匹配同 consumer 或 legacy 空行。
func consumerMatch(reqConsumer string, rec data.Record) bool {
	if reqConsumer == "" {
		return true
	}
	c := kernel.Sval(rec["consumer"])
	return c == reqConsumer || c == ""
}

// toItem 把存储行翻译为门面 DTO。
func toItem(rec data.Record) facade.IndexQueueItem {
	return facade.IndexQueueItem{
		ID:        kernel.Sval(rec[data.KeyField]),
		WorkDir:   kernel.Sval(rec["work_dir"]),
		Path:      kernel.Sval(rec["path"]),
		Consumer:  kernel.Sval(rec["consumer"]),
		Operation: kernel.Sval(rec["operation"]),
		State:     kernel.Sval(rec["state"]),
		Attempts:  intVal(rec["attempts"]),
		LastError: kernel.Sval(rec["last_error"]),
		CreatedAt: kernel.Sval(rec["created_at"]),
		UpdatedAt: kernel.Sval(rec["updated_at"]),
	}
}

// intVal 把 JSON 解码后的数值（float64）或文本归一为 int。
func intVal(v any) int {
	n, _ := strconv.Atoi(kernel.SvalOf(v))
	return n
}

// newKey 生成队列表主键：Q + UTC 时间戳（RFC3339FixedNano）+ 4 位随机数字
// （口径同 kernel.NewInboxKey：主键自带时间序；排序仍以 created_at 为准）。
func newKey() (string, error) {
	now := time.Now().UTC()
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("index_queue: 生成主键失败：%w", err)
	}
	n := (int(b[0])<<8 | int(b[1])) % 10000
	return fmt.Sprintf("Q%s%04d", now.Format(data.RFC3339FixedNano), n), nil
}
