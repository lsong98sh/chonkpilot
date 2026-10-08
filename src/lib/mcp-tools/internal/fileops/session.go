// session.go — filesys_run（文件操作）DSL 动作域的导出「门面」。
//
// 单实现、双入口：filesys_run 工具入口 HandleFileManager 与统一 DSL 编排执行器共用本会话的
// Actions/Files；动作构造逻辑只在 fileEditActions 处暴露一次，不复制实现。
package fileops

import (
	"fmt"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// Session 是一次文件操作 DSL 会话的状态容器（供统一执行器复用；单动作工具与统一执行器共用同一实现）。
type Session struct {
	ec *editCtx
}

// NewSession 构造文件操作会话。workDir 为相对路径基准；expectMD5 为顶层 md5 期望
// （路径 → 期望 md5），可为 nil。构造无副作用。
func NewSession(workDir string, expectMD5 map[string]string) (*Session, error) {
	return &Session{ec: newEditCtx(workDir, expectMD5)}, nil
}

// Actions 返回本域 DSL 动作集（RPL/APD/PTC/INS/DEL/MOV/CPY，Raw 原文透传）。
func (s *Session) Actions() []dsl.Action { return fileEditActions(s.ec) }

// Files 返回本域 DSL 文件系统（校验型 ScriptFS，接入 agentbox 沙箱）。
func (s *Session) Files() dsl.FileSystem { return ScriptFS{} }

// Summary 返回本域结果摘要（modified/created/deleted 计数 + 逐条单操作失败）。
func (s *Session) Summary() []string {
	modified := s.ec.finish()
	createdRefs, deleted, fails := s.ec.snapshot()
	created := createdEntries(createdRefs)
	out := []string{fmt.Sprintf("modified: %d, created: %d, deleted: %d", len(modified), len(created), len(deleted))}
	for _, f := range fails {
		out = append(out, fmt.Sprintf("%s %s：%s", f.Op, f.File, f.Error))
	}
	return out
}

// Close 释放资源（文件操作无外部资源，空实现）。
func (s *Session) Close() {}
