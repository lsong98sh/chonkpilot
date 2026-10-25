package models

import (
	"path/filepath"

	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// UserConfigDirName is the user-level config directory (~/.chonkpilot)。
// 单一字面量来源 = lockfile.ProjectDataDirName（`<work_dir>/.chonkpilot` 项目数据根亦用同名：
// flash / 桌面单体 / CLI 的 prjusr 单例锁按其拼接，避免各自维护字面量失配，见 I-74）。
const UserConfigDirName = lockfile.ProjectDataDirName

// ProjectDataDir 返回指定 workDir 对应的**项目数据根**（= prj 层所在目录）：
// 固定规则 `<workDir>/.chonkpilot`（2026-10-25 起无 `--data-dir` 启动参数，不可覆盖）。
//
// ⚠️ 本函数**不是** prjusr 数据根访问器（[24 §3] 实施注意）：prjusr（会话/任务树/快照等个人
// 运行态）+ 其下非库文件（日志 logs/、附件/截图 `<dir>/`）的根一律走 `data.PrjUsrDir`
// —— = `<workDir>/.chonkpilot/users/<OS 用户名>/`（DATAROOT-1；不再读 project-id）。
func ProjectDataDir(workDir string) string {
	return filepath.Join(workDir, UserConfigDirName)
}
