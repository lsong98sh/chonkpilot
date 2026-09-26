package models

import (
	"path/filepath"
	"sync"
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
// 运行态）+ 其下非库文件（日志 logs/、附件/截图 tmp/uploads/）的根一律走 `data.PrjUsrDir`
// —— data_dir 为空时 = `~/.chonkpilot/data/<project-id>/`，须先读 prj 库取 project-id。
func DataDir(workDir string) string {
	dataDirMu.RLock()
	defer dataDirMu.RUnlock()
	if dataDir != "" {
		return dataDir
	}
	return filepath.Join(workDir, UserConfigDirName)
}

// UserConfigDirName is the user-level config directory (~/.chonkpilot).
const UserConfigDirName = ".chonkpilot"
