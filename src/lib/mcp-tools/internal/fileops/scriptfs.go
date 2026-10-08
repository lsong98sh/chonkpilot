// scriptfs.go — filesys_run DSL 核心句柄（`#"path"` 数据源读写）使用的校验型文件系统（R-11）。
//
// filesys_run 的动作动词（RPL/APD/PTC/INS/DEL/MOV/CPY）在 actions.go 内自行解析并校验
// 路径；本 FS 服务 DSL 核心语句的 `#"path"` 句柄——LOOP/SET 数据源、访问器
// （.content/.lines/.array/.object/.range）、以及 `=> #"file"` 重定向目标。
// 这些引用同样须绝对路径、`~/` 开头或 `!/` 开头（临时目录）：字面路径由 manager.go 在执行前
// 预校验拦截，{{}} 插值得到的路径由句柄在执行时解析并兜底校验（含 !/ → <temp>/chonkpilot/<instance>/）。
//
// 句柄实现已上移到共享包 dslfs（与 browser_run / desktop_run 共用，均接入 agentbox 沙箱校验）。
package fileops

import (
	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/dslfs"
)

// ScriptFS 是 filesys_run 的 dsl.FileSystem 实现（零状态）。
type ScriptFS struct{}

// Open 返回句柄（路径强校验与沙箱校验在句柄方法内完成）。
func (ScriptFS) Open(path string) dsl.FileHandle { return dslfs.New(path, dslfs.Default) }
