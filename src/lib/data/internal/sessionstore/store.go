// Package sessionstore 是**会话组件**的存储边界（2026-10-23）。
//
// 分工：
//
//	prjusr（catalog）  sessions 表 = 会话目录（列表/标题/父子/kind/时间戳）；项目级表不动
//	session（每会话一库） messages / turns / snapshot / tasktree / inbox
//
// 本包把「目录 + 会话库」的组合操作收在一处，供 session / snapshot / tasktree / inbox 域复用，
// 也是 **fork（派生会话）** 的唯一实现点：fork 只读写存储，不产生任何 MQ 消息。
//
// 组件视角（单聊 / 多聊 / 子聊天）：三者的区别只在目录行的 `kind`（main / group_chat / sub_chat）
// 与 `parent_id` 关系；数据面完全同构 —— 每个会话（含子聊天）各自一库，fork 亦同。
package sessionstore

import (
	"errors"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
)

// ForkMode 是派生策略。
type ForkMode string

const (
	// ForkFull 复制全部会话数据（messages / turns / snapshot / tasktree）。
	ForkFull ForkMode = "full"
	// ForkContext 只复制上下文快照（snapshot），消息与任务树留在源会话。
	ForkContext ForkMode = "context"
)

// Store 是会话组件的存储入口（Catalog = prjusr 主库）。
type Store struct{ Catalog *data.DB }

// New 构造会话组件存储。
func New(catalog *data.DB) *Store { return &Store{Catalog: catalog} }

// Open 打开某会话的会话库（短开：调用方配对 release）。
func (s *Store) Open(sessionID string) (*data.DB, func(), error) {
	if s == nil || s.Catalog == nil {
		return nil, nil, errors.New("sessionstore: catalog is nil")
	}
	return data.OpenSession(s.Catalog.Path(), sessionID)
}

// Delete 删除会话库（目录行由调用方先删 / 后删均可；本方法只负责文件）。
func (s *Store) Delete(sessionID string) error {
	if s == nil || s.Catalog == nil {
		return errors.New("sessionstore: catalog is nil")
	}
	return data.DeleteSession(s.Catalog.Path(), sessionID)
}

// CatalogRow 读目录行（ok=false = 不存在）。
func (s *Store) CatalogRow(sessionID string) (data.Record, bool, error) {
	var rec data.Record
	ok, err := s.Catalog.Table("sessions").Get(sessionID, &rec)
	return rec, ok, err
}

// ForkOptions 控制派生细节。
type ForkOptions struct {
	Mode ForkMode
	// Title 为空 → 源标题 + " (fork)"。
	Title string
	// Kind 为空 → 继承源 kind（main / sub_chat / group_chat）。
	Kind string
}

// ForkResult 是派生结果摘要。
type ForkResult struct {
	SessionID string `json:"session_id"`
	From      string `json:"forked_from"`
	Mode      ForkMode
	Copied    map[string]int `json:"copied"` // 表 → 复制行数
}

// Fork 从 from 派生一个新会话 to（目录行 + 会话库）。
//
// 语义：
//   - 目录行：`kind` 继承（可覆盖）、`parent_id` 继承**源的 parent_id**（派生是兄弟，不是子会话）、
//     额外写 `forked_from = from`；`created_at/updated_at` = 现在。
//   - `full`：复制 snapshot / turns / messages / tasktree（行内容原样，key 不变——新库内唯一）。
//   - `context`：只复制 snapshot（"带着上下文另开一支"）。
//   - **不复制** inbox（待办不属于派生会话）。
//   - 幂等：目标会话已存在 → 返回错误（不覆盖，避免误删数据）。
func (s *Store) Fork(from, to string, opts ForkOptions) (ForkResult, error) {
	if s == nil || s.Catalog == nil {
		return ForkResult{}, errors.New("sessionstore: catalog is nil")
	}
	if from == "" || to == "" {
		return ForkResult{}, errors.New("sessionstore: from/to required")
	}
	if from == to {
		return ForkResult{}, errors.New("sessionstore: from == to")
	}
	if opts.Mode == "" {
		opts.Mode = ForkFull
	}
	if opts.Mode != ForkFull && opts.Mode != ForkContext {
		return ForkResult{}, fmt.Errorf("sessionstore: 未知 fork mode %q（full|context）", opts.Mode)
	}
	if _, exists, err := s.CatalogRow(to); err != nil {
		return ForkResult{}, err
	} else if exists {
		return ForkResult{}, fmt.Errorf("sessionstore: 目标会话已存在: %s", to)
	}

	src, exists, err := s.CatalogRow(from)
	if err != nil {
		return ForkResult{}, err
	}
	if !exists {
		return ForkResult{}, fmt.Errorf("sessionstore: 源会话不存在: %s", from)
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	row := data.Record{
		"session_id":  to,
		"title":       opts.Title,
		"kind":        opts.Kind,
		"parent_id":   sval(src["parent_id"]),
		"forked_from": from,
		"created_at":  now,
		"updated_at":  now,
	}
	if row["title"] == "" {
		title := sval(src["title"])
		if title == "" {
			title = from
		}
		row["title"] = title + " (fork)"
	}
	if row["kind"] == "" {
		row["kind"] = sval(src["kind"])
	}
	if err := s.Catalog.Table("sessions").Upsert(to, row); err != nil {
		return ForkResult{}, fmt.Errorf("sessionstore: 写目录行: %w", err)
	}

	srcDB, relSrc, err := s.Open(from)
	if err != nil {
		return ForkResult{}, err
	}
	defer relSrc()
	dstDB, relDst, err := s.Open(to)
	if err != nil {
		return ForkResult{}, err
	}
	defer relDst()

	res := ForkResult{SessionID: to, From: from, Mode: opts.Mode, Copied: map[string]int{}}
	copied, err := copyTable(srcDB, dstDB, "snapshot", "", "")
	if err != nil {
		return res, err
	}
	res.Copied["snapshot"] = copied
	if opts.Mode == ForkContext {
		return res, nil
	}
	for _, table := range []string{"turns", "messages", "tasktree"} {
		n, err := copyTable(srcDB, dstDB, table, "session_id", from)
		if err != nil {
			return res, fmt.Errorf("sessionstore: 复制 %s: %w", table, err)
		}
		res.Copied[table] = n
	}
	return res, nil
}

// copyTable 复制表行：filterField 非空 → 只复制该字段等于 filterVal 的行。
func copyTable(src, dst *data.DB, table, filterField, filterVal string) (int, error) {
	n := 0
	err := src.Table(table).ForEach(func(k string, rec data.Record) error {
		if filterField != "" && sval(rec[filterField]) != filterVal {
			return nil
		}
		out := data.Record{}
		for kk, vv := range rec {
			out[kk] = vv
		}
		if err := dst.Table(table).Upsert(k, out); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

// sval 把 Record 值转字符串（nil → ""）。
func sval(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
