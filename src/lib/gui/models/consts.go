package models

import (
	"path/filepath"
	"sync"

	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// 项目数据根（--data-dir）包级注册：解析后的数据根路径。
// 未设置时 DataDir 返回 <workDir>/.chonkpilot（现状默认，兼容模式）。
var (
	dataDirMu sync.RWMutex
	dataDir   string
)

// SetDataDir 设置项目数据根（--data-dir 解析结果）。
// 显式指定 --data-dir 或 IDE 启动时调用；临时模式（createTempDB）不调用，
// kv 层走默认解析（<workDir>/.chonkpilot）。
func SetDataDir(dir string) {
	dataDirMu.Lock()
	defer dataDirMu.Unlock()
	dataDir = dir
}

// DataDir 返回指定 workDir 对应的**项目数据根**（= prj 层所在目录；`--data-dir` 注册值优先，
// 缺省 `<workDir>/.chonkpilot`）。
//
// ⚠️ 本函数**不是** prjusr 数据根访问器（[24 §3] 实施注意）：prjusr（会话/任务树/快照等个人
// 运行态）+ 其下非库文件（日志 logs/、附件/截图 `<dir>/`）的根一律走 `data.PrjUsrDir`
// —— data_dir 为空时 = `<workDir>/.chonkpilot/users/<OS 用户名>/`（DATAROOT-1；不再读 project-id）。
func DataDir(workDir string) string {
	dataDirMu.RLock()
	defer dataDirMu.RUnlock()
	if dataDir != "" {
		return dataDir
	}
	return filepath.Join(workDir, UserConfigDirName)
}

// UserConfigDirName is the user-level config directory (~/.chonkpilot)。
// 单一字面量来源 = lockfile.ProjectDataDirName（`<work_dir>/.chonkpilot` 项目数据根亦用同名：
// flash / 桌面单体 / CLI 的 prjusr 单例锁按其拼接，避免各自维护字面量失配，见 I-74）。
const UserConfigDirName = lockfile.ProjectDataDirName
