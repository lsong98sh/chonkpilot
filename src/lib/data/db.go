// Package data 是统一数据层（对齐 12-数据层）：
// 三级 chonkpilot.db（usr/prj/prjusr）+ 通用 Table DAO（类 Hibernate）+ 迁移/seed。
// 系统级不落库（随发布资源 + embed 常量，见 12-数据层）。
// 引擎 bbolt（纯 Go、单文件、无 CGO）。
package data

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/paths"
	bolt "go.etcd.io/bbolt"
)

// BoltOpenTimeout 是打开 chonkpilot.db（含 auth 库）的锁等待上限（bbolt 单文件排他锁）。
// Timeout=0（默认 nil）= 无限等待 → 库被另一进程长持时 CLI/GUI 会静默挂起；显式超时
// 使冲突**快速失败、报错可见**（D-45：GUI × CLI 并发访问同一项目库的第一道兜底，
// 形态级占用校验见 prjusr 单例锁（`<workDir>/.chonkpilot/chonkpilot.lock`）。
const BoltOpenTimeout = 3 * time.Second

// Layer 是数据层标识（v6：三级同构；系统级无库）。
type Layer string

const (
	LayerUsr     Layer = "usr"     // 用户（~/.chonkpilot）
	LayerPrj     Layer = "prj"     // 项目（<workdir>/.chonkpilot，团队共享）
	LayerPrjUsr  Layer = "prjusr"  // 项目用户（<workDir>/.chonkpilot/users/<用户名>，本机本项目；保留会话列表等）
	LayerSession Layer = "session" // 旧布局：主会话库（messages/turns/snapshot/tasktree/inbox；<prjusr>/sessions/<hex>/）
	LayerDialog  Layer = "dialog"  // 新布局（SESSION-COMP-1 P0）：一树一库（<prjusr>/dialogs/dialog-<hex(dialog_id)>.db）
)

// DB 是一层 chonkpilot.db。
type DB struct {
	path  string
	layer Layer
	b     *bolt.DB
}

// Open 打开/创建一层 chonkpilot.db（建表 + 迁移 + seed）。
func Open(path string) (*DB, error) {
	return OpenLayer(path, "")
}

// OpenLayer 指定层打开（层标识仅作元信息；v6 起三层同构，迁移不再按层分支）。
func OpenLayer(path string, layer Layer) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	b, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: BoltOpenTimeout})
	if err != nil {
		return nil, err
	}
	db := &DB{path: path, layer: layer, b: b}
	err = b.Update(func(tx *bolt.Tx) error {
		version := readSchemaVersion(tx)
		for _, m := range migrations {
			if m.Version > version {
				if err := m.Up(tx); err != nil {
					return err
				}
				recordMigration(tx, m)
			}
		}
		return seedDefaults(tx, layer)
	})
	if err != nil {
		b.Close()
		return nil, err
	}
	return db, nil
}

// Table 返回表（桶）句柄。
func (db *DB) Table(name string) *Table {
	return &Table{db: db, name: name}
}

// Layer 返回层标识。
func (db *DB) Layer() Layer { return db.layer }

// Path 返回 db 文件路径。
func (db *DB) Path() string { return db.path }

// Close 关闭 db。
func (db *DB) Close() error { return db.b.Close() }

// ensureBucket 事务内确保桶存在。
func ensureBucket(tx *bolt.Tx, name string) error {
	_, err := tx.CreateBucketIfNotExists([]byte(name))
	return err
}

// ─── 三级路径（12-数据层）────────────────────────

// 测试隔离用覆盖值（nil / 空串 = 走 os.UserHomeDir）。用 atomic.Pointer 保护：
// SetDataHome 可能在**运行期**被调用（persist.New 注入 UsrPath、CLI 数据根准备 prepareTemp/Custom），
// 而 UserPath/DataRoot 被任意 goroutine 并发读取 —— 无同步的包级 string 读写会构成数据竞争。
var (
	userPathOverride atomic.Pointer[string]
	dataRootOverride atomic.Pointer[string]
)

// SetDataHome 重定位数据主目录的 usr 主库路径与 prjusr 数据根（**默认 = ~/.chonkpilot**；
// 传空恢复默认）。用途：测试隔离（避免污染用户配置）、便携/dev 部署的自定义数据主目录。
// 并发安全：覆盖值经 atomic 写；读侧（UserPath/DataRoot）无锁原子读，可在运行期安全调用。
func SetDataHome(usrPath, dataRoot string) {
	userPathOverride.Store(&usrPath)
	dataRootOverride.Store(&dataRoot)
}

// UserPath 返回 usr 层 db 路径：~/.chonkpilot/chonkpilot.db。
func UserPath() string {
	if p := userPathOverride.Load(); p != nil && *p != "" {
		return *p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".chonkpilot", "chonkpilot.db")
}

// DataRoot 返回项目用户级数据根：~/.chonkpilot/data。
func DataRoot() string {
	if p := dataRootOverride.Load(); p != nil && *p != "" {
		return *p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".chonkpilot", "data")
}

// UserDirName 返回当前 OS 用户目录名（`C:\Users\13661` → `13661`），用作 `users/<name>`。
// 取 home 的 basename 而非 `USERNAME`（域账号 / 改名场景两者不一致）；非法名回落 `default`。
func UserDirName() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "default"
	}
	name := filepath.Base(filepath.Clean(home))
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\:*?"<>|`) {
		return "default"
	}
	return name
}

// PrjUsrPath 返回某工作目录的 prjusr **数据根目录**（非库文件所在目录）：
//
//	dataDir 非空 → <dataDir>（临时 / 覆盖形态，与 prj 同根）
//	dataDir 为空 → <workDir>/.chonkpilot/users/<OS 用户名>
//
// 数据随项目目录走：复制工作目录即分叉全部用户历史（49 DATAROOT-1）。
// `SetDataHome` 的 dataRoot 覆盖（测试隔离 / 便携部署）优先于 `<workDir>/.chonkpilot`。
func PrjUsrPath(workDir, dataDir string) string {
	if dataDir != "" {
		return paths.ResolveDir(dataDir, workDir)
	}
	root := filepath.Join(workDir, ".chonkpilot")
	if p := dataRootOverride.Load(); p != nil && *p != "" {
		root = *p
	}
	return filepath.Join(root, "users", UserDirName())
}

// PrjUsrDBPath 返回某工作目录的 prjusr 主库文件路径：
// dataDir 非空 → 与 prj 同库（`ProjectPath`）；否则 `<PrjUsrPath>/chonkpilot.db`。
func PrjUsrDBPath(workDir, dataDir string) string {
	if dataDir != "" {
		return ProjectPath(workDir, dataDir)
	}
	return filepath.Join(PrjUsrPath(workDir, ""), "chonkpilot.db")
}

// SessionDBPath 返回某主会话的会话库路径（**旧布局**，与 prjusr 同根）：
//
//	<prjusr 目录>/sessions/<hex(session_id)>/chonkpilot.db
//
// 目录名用 hex 编码，避免任意 session_id 造成路径穿越 / 非法字符。
// 新布局（SESSION-COMP-1 P0）见 [DialogDBPath]；本函数保留至 P3 路由切换完成。
func SessionDBPath(prjUsrDBPath, sessionID string) string {
	return filepath.Join(filepath.Dir(prjUsrDBPath), "sessions", hex.EncodeToString([]byte(sessionID)), "chonkpilot.db")
}

// DialogDBPath 返回某对话（**一树一库**，SESSION-COMP-1 P0）的库文件路径：
//
//	<prjusr 目录>/dialogs/dialog-<hex(dialog_id)>.db
//
// 与 [SessionDBPath] 的差异：文件名（非子目录）承载 hex(dialog_id)，一库装整棵会话树
// （主 session + 全部子会话）；不含 prjusr 定位之外的分层。
func DialogDBPath(prjUsrDBPath, dialogID string) string {
	return filepath.Join(filepath.Dir(prjUsrDBPath), "dialogs", "dialog-"+hex.EncodeToString([]byte(dialogID))+".db")
}

// ProjectPath 返回 prj 主库路径（12-数据层）：
//
//	dataDir 非空（CLI 临时目录形态）→ <dataDir>/chonkpilot.db
//	dataDir 为空（常规形态）        → <workDir>/.chonkpilot/chonkpilot.db
func ProjectPath(workDir, dataDir string) string {
	if dataDir != "" {
		return filepath.Join(paths.ResolveDir(dataDir, workDir), "chonkpilot.db")
	}
	return filepath.Join(workDir, ".chonkpilot", "chonkpilot.db")
}

// PrjUsrDir 返回某项目的 prjusr **数据根目录**（非库文件所在目录；12-数据层 §3）。
// 等价于 [PrjUsrPath]；保留 (string, error) 签名供既有调用方复用。
//
// 用途（[24 §3.2] MW-8）：个人运行态的**非库文件** —— 日志 `logs/`、附件/截图
// `tmp/uploads/`、fileserver 白名单 —— 一律按本函数解析。**不可**用
// `<workDir>/.chonkpilot`（那是 data-dir 访问器语义 = **prj** 数据根，见 24 §3 实施注意）。
func PrjUsrDir(workDir, dataDir string) (string, error) {
	return PrjUsrPath(workDir, dataDir), nil
}
