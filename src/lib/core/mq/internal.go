// 进程内内存 MQ（对齐 61-消息一览 §9：去 NATS 后唯一实现）。
//
//   - 订阅表 map[string][]handlerEntry + 主题通配匹配（> 多段 / * 单段，段级拆分）
//   - Emit：按主题匹配收集订阅者 → 按 (order, 注册序) 稳定排序 → 同步逐 handler 执行
//     （顺序 = 发布顺序；同一次派发内共享同一 *Value：可写回 Result / 收集 Errors）
//   - handler 返回 error 或 panic → 收集进 v.Errors（不中断后续 handler、不 reject）
//   - sync.RWMutex 保护订阅表；订阅/发布线程安全
package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type handlerEntry struct {
	id    uint64 // 订阅唯一 id（Unsubscribe 定位；同 order 按 id 升序 = 注册序）
	order int    // v2 执行顺序（v1 固定 0）
	v1    bool   // true = v1 Handler（仅收 payload，不写回）
	h     Handler
	vh    VHandler
}

type wildSub struct {
	tokens []string // 通配模式段（含 > / *）
	entry  handlerEntry
}

type internalBus struct {
	mu     sync.RWMutex
	prefix string                    // 命名空间前缀（如 "chonk."；空 = 不隔离）
	subs   map[string][]handlerEntry // 精确主题 → handler 列表（全主题）
	wild   []wildSub                 // 含通配的主题 → handler（* / >，全主题）
	nextID uint64
	closed bool
}

func newBus(prefix string) (Bus, error) {
	return &internalBus{prefix: prefix, subs: make(map[string][]handlerEntry)}, nil
}

// Prefix 返回总线命名空间前缀（未注入 = 空串）。
func (b *internalBus) Prefix() string { return b.prefix }

// fullSubject 返回注册/匹配用的全主题：业务相对主题自动补前缀；
// 已带本总线前缀的主题（遗留全主题直传，如 chonk.srv.notify.data-*）原样透传、不重复加。
func (b *internalBus) fullSubject(subject string) string {
	if b.prefix == "" || strings.HasPrefix(subject, b.prefix) {
		return subject
	}
	return b.prefix + subject
}

// handlerSubject 返回下发给 handler 的 subject：统一为去前缀后的相对形式
// （"session-start"；data 面遗留直传 → "srv.notify.data-*"）。
func (b *internalBus) handlerSubject(full string) string {
	if b.prefix == "" {
		return full
	}
	return strings.TrimPrefix(full, b.prefix)
}

// splitSubject 按 "." 拆段（NATS 约定；空字符串 → 空段表）。
func splitSubject(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ".")
}

// addEntry 追加订阅（含通配入 wild 表，精确入 subs 表）。
func (b *internalBus) addEntry(subject string, e handlerEntry) {
	if strings.ContainsAny(subject, "*>") {
		b.wild = append(b.wild, wildSub{tokens: splitSubject(subject), entry: e})
		return
	}
	b.subs[subject] = append(b.subs[subject], e)
}

func (b *internalBus) allocSub(subject string) *internalSub {
	b.nextID++
	return &internalSub{bus: b, subject: subject, id: b.nextID}
}

// On v2 订阅：order 升序执行（同 order 按注册序）。注册按全主题（补前缀），
// handler 收到的 subject 为去前缀后的相对形式（见 fullSubject/handlerSubject）。
func (b *internalBus) On(subject string, order int, h VHandler) (Sub, error) {
	if h == nil {
		return nil, errNilHandler
	}
	full := b.fullSubject(subject)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errClosed
	}
	sub := b.allocSub(full)
	b.addEntry(full, handlerEntry{id: sub.id, order: order, vh: h})
	return sub, nil
}

// Subscribe v1 兼容订阅（等价 On(subject, 0, 包装)，不参与结果写回）。
func (b *internalBus) Subscribe(subject string, h Handler) (Sub, error) {
	if h == nil {
		return nil, errNilHandler
	}
	full := b.fullSubject(subject)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errClosed
	}
	sub := b.allocSub(full)
	b.addEntry(full, handlerEntry{id: sub.id, v1: true, h: h})
	return sub, nil
}

// match 在锁内收集命中订阅者快照（结构与值复制，执行在锁外）。
func (b *internalBus) match(subject string) []handlerEntry {
	var all []handlerEntry
	if es, ok := b.subs[subject]; ok {
		all = append(all, es...)
	}
	toks := splitSubject(subject)
	for _, w := range b.wild {
		if matchTokens(w.tokens, toks) {
			all = append(all, w.entry)
		}
	}
	// (order, id) 稳定升序：order 决定语义顺序，id = 注册序。
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].order != all[j].order {
			return all[i].order < all[j].order
		}
		return all[i].id < all[j].id
	})
	return all
}

// Emit 派发：按 (order, 注册序) 同步执行全部订阅者，共享 *Value；
// 返回 *Future（进程内同步派发完成即 resolve；errors 恒收集，不 reject）。
// 匹配用全主题（自动补前缀）；Value.Subject 与 handler subject 为去前缀后的相对形式。
func (b *internalBus) Emit(ctx context.Context, subject string, payload any) *Future {
	full := b.fullSubject(subject)
	v := &Value{Subject: b.handlerSubject(full)}
	switch p := payload.(type) {
	case nil:
	case []byte:
		v.Payload = p
	case string:
		v.Payload = []byte(p)
	default:
		if raw, err := json.Marshal(p); err == nil {
			v.Payload = raw
		}
	}
	f := newFuture(v)
	if err := ctx.Err(); err != nil {
		v.Errors = append(v.Errors, err)
		f.resolve()
		return f
	}
	b.mu.RLock()
	entries := b.match(full)
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		v.Errors = append(v.Errors, errClosed)
		f.resolve()
		return f
	}
	for _, e := range entries {
		b.dispatch(ctx, v.Subject, v, e)
	}
	f.resolve()
	return f
}

// dispatch 执行单个订阅者；panic / error → 收集进 v.Errors（不中断后续）。
func (b *internalBus) dispatch(ctx context.Context, subject string, v *Value, e handlerEntry) {
	if e.v1 {
		defer func() {
			if r := recover(); r != nil {
				v.Errors = append(v.Errors, fmt.Errorf("mq: handler panic: %v", r))
			}
		}()
		e.h(subject, v.Payload)
		return
	}
	defer func() {
		if r := recover(); r != nil {
			v.Errors = append(v.Errors, fmt.Errorf("mq: handler panic: %v", r))
		}
	}()
	if err := e.vh(ctx, subject, v); err != nil {
		v.Errors = append(v.Errors, err)
	}
}

// Publish v1 兼容：fire-and-forget（内部 = Emit 不 await；已关闭返回 errClosed）。
func (b *internalBus) Publish(subject string, payload []byte) error {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return errClosed
	}
	b.Emit(context.Background(), subject, payload)
	return nil
}

func (b *internalBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.subs = make(map[string][]handlerEntry)
	b.wild = nil
	return nil
}

// unsubscribe 按订阅 id 从表中移除。
func (b *internalBus) unsubscribe(subject string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.ContainsAny(subject, "*>") {
		kept := b.wild[:0]
		for _, w := range b.wild {
			if !(strings.Join(w.tokens, ".") == subject && w.entry.id == id) {
				kept = append(kept, w)
			}
		}
		b.wild = kept
		return
	}
	es := b.subs[subject]
	kept := es[:0]
	for _, e := range es {
		if e.id != id {
			kept = append(kept, e)
		}
	}
	if len(kept) == 0 {
		delete(b.subs, subject)
	} else {
		b.subs[subject] = kept
	}
}

type internalSub struct {
	bus     *internalBus
	subject string
	id      uint64
}

func (s *internalSub) Unsubscribe() error {
	s.bus.unsubscribe(s.subject, s.id)
	return nil
}
