// session.go — desktop_run（桌面编排）DSL 动作域的导出「门面」。
//
// 单实现、双入口：desktop_run 工具入口 HandleDesktopRun 与统一 DSL 编排执行器共用本会话的
// Actions/Files；动作构造逻辑只在 desktopActions 处暴露一次，不复制实现。
package desktop

import (
	"fmt"
	"runtime"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// Session 是一次桌面编排 DSL 会话的状态容器（供统一执行器复用；单动作工具与统一执行器共用同一实现）。
type Session struct {
	ctx *runCtx
}

// NewSession 构造桌面会话（默认 speed=5、jitter=3、delay=0，与 HandleDesktopRun 原构造一致），
// 并把当前 goroutine 锁定到 OS 线程（SendInput 输入注入依赖线程消息队列的稳定性）。
func NewSession() (*Session, error) {
	runtime.LockOSThread()
	return &Session{ctx: &runCtx{
		vars:   map[string]float64{},
		speed:  5,
		jitter: 3,
		delay:  0,
	}}, nil
}

// Actions 返回本域 DSL 动作集（WIN/MOV/CLK/.../SHT/SLP，Raw 原文透传）。
func (s *Session) Actions() []dsl.Action { return desktopActions(s.ctx) }

// Files 返回本域 DSL 文件系统（校验型 scriptFS，接入 agentbox 沙箱）。
func (s *Session) Files() dsl.FileSystem { return scriptFS{} }

// Summary 返回本域结果摘要（执行指令数 + 最近一次文本输出，如 WIN list）。
func (s *Session) Summary() []string {
	out := []string{fmt.Sprintf("executed: %d", s.ctx.done)}
	if s.ctx.out != "" {
		out = append(out, s.ctx.out)
	}
	return out
}

// Close 解除 OS 线程锁定（释放本域资源）。
func (s *Session) Close() { runtime.UnlockOSThread() }
