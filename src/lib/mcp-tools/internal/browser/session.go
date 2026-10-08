// session.go — browser_run（浏览器）DSL 动作域的导出「门面」。
//
// 单实现、双入口：browser_run 工具入口 HandleBrowserRun → Execute 与统一 DSL 编排执行器
// 共用本会话的 Actions/Files；动作构造逻辑只在 browserActions 处暴露一次，不复制实现。
//
// 惰性：NewSession 仅保存入参，不启动 Chrome、不创建 chromedp 上下文；浏览器在首个浏览器动作
// 执行时才启动（见 Runner.ensureStarted）。脚本无浏览器动作时全程不产生浏览器副作用。
package browser

import (
	"context"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// Session 是一次浏览器 DSL 会话的状态容器（供统一执行器复用；单动作工具与统一执行器共用同一实现）。
type Session struct {
	r      *Runner
	parent context.Context
}

// NewSession 构造浏览器会话（无副作用：不启动浏览器）。
func NewSession(opt Options) (*Session, error) {
	return &Session{r: &Runner{opt: opt}, parent: context.Background()}, nil
}

// SetParent 注入父上下文：统一 DSL 编排执行器用它把作业级 context 传播到惰性启动的浏览器
// 生命周期（parent → chromedp allocator，取消即级联关闭）。ctx 为 nil 时保持原值。
// 既有行为不变：browser_run 的 Execute 仍在执行前以自己的 parent 覆盖。
func (s *Session) SetParent(ctx context.Context) {
	if ctx != nil {
		s.parent = ctx
	}
}

// Actions 返回本域 DSL 动作集（OPN/WAT/CLK/.../SHT/UPF/TAB/SLP，Raw 原文透传）。
func (s *Session) Actions() []dsl.Action { return browserActions(s.r) }

// Files 返回本域 DSL 文件系统（校验型 scriptFS，接入 agentbox 沙箱）。
func (s *Session) Files() dsl.FileSystem { return scriptFS{} }

// Summary 返回本域过程输出（DOM/DBG 落盘、TAB list 等；无输出则 nil）。
func (s *Session) Summary() []string {
	if len(s.r.out) == 0 {
		return nil
	}
	return append([]string{}, s.r.out...)
}

// Close 收尾并释放浏览器资源（console/DOM 落盘、失败截图、关闭 Chrome）。
// 未启动浏览器时为空操作（仅释放可能的 allocator）。
func (s *Session) Close() { s.r.teardown(s.r.failed.Load()) }
