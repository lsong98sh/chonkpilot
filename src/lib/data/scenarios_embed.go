// 出厂场景的内嵌面：把 `src/lib/data/scenarios/`（与 capability/ 平级的独立根）经 go:embed
// 打进 exe —— 发布不再随产物拷贝 `scenarios/`，改由数据层在 app 初始化（首次 list）时把内嵌
// 出厂场景**物化**到 app 级场景根（`<exeDir>/scenarios/`，缺失即恢复、已存在不覆盖）。
//
// 三级根均可编辑（app 级不再只读）；出厂内容见 <scenarios/default/>（「开发场景」）。
package data

import (
	"embed"
	"io/fs"
)

//go:embed scenarios
var factoryScenarios embed.FS

// FactoryScenarios 返回内嵌**出厂场景**文件系统（根 = 场景目录集合，其下为各场景目录，如
// `default/scenario.json`）。供数据层在 app 初始化时物化到 app 级场景根。
func FactoryScenarios() fs.FS {
	sub, err := fs.Sub(factoryScenarios, "scenarios")
	if err != nil {
		return factoryScenarios
	}
	return sub
}
