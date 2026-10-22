// Package contracts 是 LLM 域工具契约的**唯一副本**（ask_user / next_run / subchat_open /
// subchat_finish / tool_stop / tool_result）。
//
// 历史上同一组文件在 `server/contracts/tools/`（进程内注册用）与
// `cmd/server-tools/contracts/`（进程外实现用）各存一份，靠一致性测试防漂移。
// 现已收敛为本包单一 embed；2026-10-19（G-68 第二步）起 server-tools 进程删除，
// 消费方为：
//
//   - server（进程内域工具注册，单测 / CLI）→ contracts.FS
//   - build-mcp-server.ps1 把本目录契约同步到 `capability/tools/server/` →
//     core-mcp-server 按 `core-only` 契约注册并经 executor 执行
package contracts

import "embed"

// FS 内含 `tools/*.tool.md`；文件名（去扩展名）= 工具名。
//
//go:embed tools/*.tool.md
var FS embed.FS
