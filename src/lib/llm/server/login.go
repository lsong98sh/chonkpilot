// 形态判定（PRODSIM-1：本地认证面已整体移除）。
//
// 历史（阶段 2b-1/2b-2）：本文件曾承载 login-register / login-in / login-out 三个方法面、
// auth 库（`chonkpilot-data/auth`）、令牌解析（`userByToken` / `Authenticated`）、
// 用户数据根（`userDataDir`）与允许根校验（`allowWorkDir`）。PRODSIM-1 拍板「本地 auth 全删，
// 认证将来统一到 registry」→ 这些能力与对应消息面（`login-*`）**全部移除**。
//
// 本文件只保留**形态判定**：多处仍按 `Form()` 选择行为（日志 / 兼容分支），
// 但单产物只有 desktop 一种实际形态。
package server

import "strings"

// 形态取值（61 §4.6：desktop / gui / browser；PRODSIM-1 后仅 desktop 为实际构建形态）。
const (
	FormDesktop = "desktop"
	FormGui     = "gui"
	FormBrowser = "browser"
)

// Form 返回本实例的**运行形态**（Options.Form，由入口/宿主注入；空 / 未知 = desktop）。
func (s *Server) Form() string {
	switch strings.ToLower(strings.TrimSpace(s.opts.Form)) {
	case FormGui:
		return FormGui
	case FormBrowser:
		return FormBrowser
	default:
		return FormDesktop
	}
}

// RequireAuth 是否要求认证：**恒 false**。
//
// PRODSIM-1：单产物 = 本机 UDS 前门（本机信任免鉴权）；本地 auth 已删除，
// 认证将来统一由 registry 承担（不在本进程内）。
func (s *Server) RequireAuth() bool { return false }
