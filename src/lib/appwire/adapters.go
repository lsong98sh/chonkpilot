package appwire

import (
	"github.com/chonkpilot/chonkpilot-data/facade"
	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
)

// DefaultPlugins 返回**内嵌业务插件**清单。D-71（2026-10-25）起为**空**：codegraph / vfts 的
// 引擎与工具已随进程外插件迁出主仓（`chonkpilot-plugins/codegraph`，经 `plugin.ini` 拉起、工具
// 由插件 MCP server 注册），主仓只留框架。
//
// 为什么必须为空（2026-10-27 定位）：内嵌 hook 会在宿主内**另起一份**
// `chonkpilot-codegraph-mcp-server`（独立进程 + 独立 Workspace），与插件引擎**同库双写**
// （`<workdir>/.chonkpilot/codegraph/{meta.json,index.db}`）：
//   ① 插件保存的 skip-dirs/exts 只作用于插件侧引擎 → 内嵌引擎按旧/默认规则重建，把正确索引与
//      meta 覆盖回旧内容（`run_index_gate` G8「用户规则」看似不生效）；
//   ② 两进程并发 `.tmp` → `rename: 文件被占用 / 找不到文件`（G9 502）；
//   ③ vfts 两进程各持 zvec collection → `Can't lock read-write collection`（G9 索引失败）。
// 配套：内嵌 hook 原先承担的运行时状态回写（`codegraph.status` / `vfts.status`）已整体退场
// （PLUGIN-UI-2：插件**不再回写**这两个 prj 键，也不读它们）；**索引进度的 UI 亦归插件**
// （`ui.views` 的 `badge` / `status` + 插件动作 `badge` / `engine-status`），主仓不读任何
// 插件业务键、不渲染任何插件业务视图。
// memory / history 亦非内嵌插件（2026-10-24 改独立 exe 插件）；压缩不是插件（SESSION-1 内建）。
func DefaultPlugins(f facade.API) []hook.Hook {
	_ = f
	return nil
}
