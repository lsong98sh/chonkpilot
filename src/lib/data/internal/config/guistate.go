// 个人运行态 key 的分层路由（12-数据层）：
// 窗口几何 / 区块尺寸 / 文件树展开 / 打开页签 / 引擎运行状态等属"项目用户级"，
// 落 prjusr；其余 prj-config 键（团队共享配置）仍落 prj。
//
// 路由在 config 域实现内部完成——**消息契约不变**（仍是 data-prj-config-{list,load,save,delete}
// 与 {key,value} / {data:{key,value}} 形态），故 61-消息一览 无需变更。
//
// 阶段 4「internal 下沉」：本文件由 `chonkpilot-data/persist` 整体下移（逻辑逐字未改）。
package config

import "strings"

// localRuntimeKeys 是落 prjusr 的个人运行态 key（精确名）。
var localRuntimeKeys = map[string]bool{
	"layout":                 true,
	"window":                 true,
	"filetree-expanded-key":  true,
	"filetree-selected-path": true,
	"opened-files":           true,
	"codegraph.status":       true, // 引擎索引运行状态（本机可重建的派生物）
	"vfts.status":            true, // vfts 全文索引运行状态（本机可重建的派生物）
}

// isLocalRuntimeKey 判断是否个人运行态 key（含 window.* / layout.* 细 key 形态）。
func isLocalRuntimeKey(key string) bool {
	if localRuntimeKeys[key] {
		return true
	}
	return strings.HasPrefix(key, "window.") || strings.HasPrefix(key, "layout.")
}
