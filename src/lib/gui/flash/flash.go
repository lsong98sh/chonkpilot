// Package flash 是 flash 薄壳客户端的**纯逻辑**（打开语义 / 最近项目 / 项目占用锁），
// 不依赖 WebView2 与 Windows 宿主 → 供 `src/flash` 宿主与 L2 单测共用。
//
// 设计口径见 [19 §8.11](../../../../docs/spec/10-architecture/19-多租户服务端形态.md) 与
// [62 §3A](../../../../docs/spec/60-reference/62-命令行与参数.md)：
//   - **打开语义**：显式 `--work-dir` → 直接采用；未给 → 按「最近关闭 → 次近 → …」取第一个
//     **存在且未被本用户打开**的目录；都没有 → 让用户选目录；
//   - **「已打开」判定 + 持锁 = flash 侧**（本包 `AcquireProject`，per-user → RDS 多用户
//     同项目互不冲突）；
//   - **data-dir 约定** = `<work_dir>/.chonkpilot`（非空 → `data.PrjUsrDir` 纯路径拼接，
//     不打开项目库、不与 server 抢 bbolt flock）。
package flash

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-gui/models"
	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// DataDirName 是 flash 本地数据根目录名（data-dir 约定 = `<work_dir>/.chonkpilot`）。
const DataDirName = ".chonkpilot"

// recentsFileName 是「最近项目」记录文件名（落用户数据根 `<home>/.chonkpilot/`）。
const recentsFileName = "flash-recents.json"

// RecentsCap 是「最近项目」记录上限（对齐既有 recent_dirs 的 10 条截断）。
const RecentsCap = 10

// ErrNoProject 表示未能确定工作目录（无显式参数、无可用最近项目、用户取消选择）。
var ErrNoProject = errors.New("flash: 未选定项目目录")

// DataDirFor 返回 flash 的项目数据根（约定值 = `<work_dir>/.chonkpilot`；**恒非空**）。
// 非空 → `data.PrjUsrDir(workDir, dataDir)` 走纯路径拼接（不开库、不抢 flock，见包注释）。
func DataDirFor(workDir string) string {
	return filepath.Join(workDir, DataDirName)
}

// CurrentUser 返回本机 OS 用户名（instance-claim 的必填 `user`；取不到 → "unknown"）。
func CurrentUser() string {
	if u, err := user.Current(); err == nil && u != nil && strings.TrimSpace(u.Username) != "" {
		return u.Username
	}
	return "unknown"
}

// RecentsPath 返回「最近项目」记录文件路径：`<home>/.chonkpilot/flash-recents.json`
// （与 `chonkpilot-lib/lockfile` 的 work-dir 锁同根；home 不可解析 → 回落系统临时目录）。
func RecentsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".chonkpilot", recentsFileName)
}

// LoadRecents 读取「最近项目」列表（最近在前）；文件缺失/损坏 → nil（不阻断启动）。
func LoadRecents(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var dirs []string
	if json.Unmarshal(b, &dirs) != nil {
		return nil
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if s := strings.TrimSpace(d); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SaveRecent 把 dir 置为最近并落盘（去重、上限 RecentsCap，最近在前）。
func SaveRecent(path, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	kept := []string{dir}
	for _, d := range LoadRecents(path) {
		if strings.EqualFold(d, dir) {
			continue
		}
		kept = append(kept, d)
	}
	if len(kept) > RecentsCap {
		kept = kept[:RecentsCap]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// AcquireProject 非阻塞获取 work-dir 占用锁（per-user）。
//   - busy=true → 该用户已打开该项目（调用方走 `instance-bring-to-top` 置顶后退出）；
//   - err != nil（非 busy）→ 锁设施故障（调用方仅告警，不阻断 —— 与既有 I-74 口径一致）；
//   - 成功 → 返回的锁须在进程存活期持有。
func AcquireProject(workDir string) (l *lockfile.Lock, busy bool, err error) {
	l, err = lockfile.AcquireWorkDir(workDir)
	if errors.Is(err, lockfile.ErrBusy) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return l, false, nil
}

// WorkDirFree 报告 work-dir 当前是否未被**本用户**打开（选目录前置校验用；不持有锁）。
// 锁设施故障（非占用）→ 放行（true）：占用校验是体验增强，不应因锁故障阻断用户操作。
func WorkDirFree(workDir string) bool { return lockfile.WorkDirFree(workDir) }

// DirExists 判定路径存在且为目录（`DirResolver.Exists` 的生产实现）。
func DirExists(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// DirResolver 解析本次打开的工作目录（打开语义；依赖注入便于单测）。
type DirResolver struct {
	// BaseDir 是相对目录的解析基准（通常 = cwd）。
	BaseDir string
	// Exists 判定目录是否存在（nil = 不校验）。
	Exists func(string) bool
	// Free 判定目录是否**未被本用户打开**（nil = 恒可用）。生产 = lockfile.WorkDirFree。
	Free func(string) bool
	// Picker 让用户选目录（取消 → ("", nil)，视为未选定）。nil = 无 UI → ErrNoProject。
	Picker func() (string, error)
}

// Resolve 按打开语义决定工作目录：
//   - explicit 非空 → 采用之；**已打开** → 让用户选目录；
//   - 否则按 recents 顺序取第一个存在且可用的目录；
//   - 都不满足 → 让用户选目录；仍未选定 → ErrNoProject。
func (r DirResolver) Resolve(explicit string, recents []string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		d := models.ResolveDir(explicit, r.BaseDir)
		if r.free(d) {
			return d, nil
		}
		return r.pick() // 显式目录已被打开 → 选目录（62 §3A）
	}
	for _, raw := range recents {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		d := models.ResolveDir(raw, r.BaseDir)
		if r.exists(d) && r.free(d) {
			return d, nil
		}
	}
	return r.pick()
}

// ResolvePick 直接走「让用户选目录」（跨用户冲突时换目录用）。
func (r DirResolver) ResolvePick() (string, error) { return r.pick() }

func (r DirResolver) exists(dir string) bool {
	if r.Exists == nil {
		return true
	}
	return r.Exists(dir)
}

func (r DirResolver) free(dir string) bool {
	if r.Free == nil {
		return true
	}
	return r.Free(dir)
}

func (r DirResolver) pick() (string, error) {
	if r.Picker == nil {
		return "", ErrNoProject
	}
	sel, err := r.Picker()
	if err != nil {
		return "", err
	}
	d := models.ResolveDir(sel, r.BaseDir)
	if d == "" {
		return "", ErrNoProject
	}
	if !r.exists(d) {
		return "", fmt.Errorf("flash: 目录不存在: %s", d)
	}
	if !r.free(d) {
		return "", fmt.Errorf("flash: 目录已被本用户打开: %s", d)
	}
	return d, nil
}
