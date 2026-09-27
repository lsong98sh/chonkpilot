// Package indexignored 实现 `data-index-ignored` 只读数据面的判定逻辑：
// 给定一组 workdir 相对路径，判定其是否被 codegraph / vfts 两引擎的「索引排除规则」排除。
//
// 供前端文件树把被排除的条目灰显（判定口径必须与引擎实际索引范围一致）。排除语义
// **完全复用** github.com/chonkpilot/chonkpilot-ignore 的单一实现（ConfigOptions + Ignored），
// 本包不另写一套匹配：`Ignored(p) ⇔ 引擎 WalkDir 会跳过 p`。
//
// 只读、零副作用：不写配置/状态、不触发索引（仅读 prj 配置值与磁盘上的 ignore 文件）。
package indexignored

import (
	"os"

	ignore "github.com/chonkpilot/chonkpilot-ignore"
)

// 引擎标识（配置键前缀 = 引擎名；<engine>.skip-dirs / <engine>.stack-gitignore / enable-<engine>）。
const (
	EngineCodegraph = "codegraph"
	EngineVfts      = "vfts"
)

// MaxPaths 单次请求判定的路径数上限：超出只判定前 MaxPaths 条并置 Truncated=true。
const MaxPaths = 20000

// EngineResult 是单引擎判定结果。
type EngineResult struct {
	// Enabled 该引擎开关（enable-<engine> == "true"）；false 时 Ignored 恒为空。
	Enabled bool
	// Ignored 入参中确被判排除的路径（原样回显入参字符串，保序、不去重）。
	Ignored []string
}

// Result 是两引擎判定结果。
type Result struct {
	Codegraph EngineResult
	Vfts      EngineResult
	// Truncated 入参超过 MaxPaths → 只判定前 MaxPaths 条，此值为 true。
	Truncated bool
}

// ConfigKeys 返回本判定需要的 prj 配置键（两引擎的 enabled / skip-dirs / stack-gitignore）。
func ConfigKeys() []string {
	return []string{
		"enable-" + EngineCodegraph, EngineCodegraph + ".skip-dirs", EngineCodegraph + ".stack-gitignore",
		"enable-" + EngineVfts, EngineVfts + ".skip-dirs", EngineVfts + ".stack-gitignore",
	}
}

// Query 判定 paths（workdir 相对、'/' 分隔）是否被两引擎排除。
//
//	get  读 prj 配置键（不存在 → ok=false）；workdir 为规则根。
//	warnf 非致命异常出口（可 nil）；workdir 无效 / 规则不可读 → 该引擎 enabled 按配置、
//	Ignored 为空（降级为「都不排除」，不抛出）。
//
// 每引擎只构建一次 Options（逐级 .gitignore 只解析一次），判定为 O(paths)。
func Query(workdir string, paths []string, get func(key string) (string, bool), warnf func(format string, args ...any)) Result {
	limited := paths
	res := Result{}
	if len(paths) > MaxPaths {
		limited = paths[:MaxPaths]
		res.Truncated = true
	}
	// workdir 不可用（未解析 / 目录不存在）→ 判定自然降级为「都不排除」，此处仅写日志
	// （空 workdir 另由 ConfigOptions 报错并经 judge 记录，不在此重复）。
	if workdir != "" {
		if fi, err := os.Stat(workdir); err != nil || !fi.IsDir() {
			warn(warnf, "data-index-ignored: workdir 不可用（%q）：%v", workdir, err)
		}
	}
	res.Codegraph = judge(EngineCodegraph, workdir, limited, get, warnf)
	res.Vfts = judge(EngineVfts, workdir, limited, get, warnf)
	return res
}

// judge 单引擎判定：ConfigOptions 一次 → 逐路径 Ignored。
// 引擎未启用（enabled=false）→ 不判排除（ignored 恒空，与前端「引擎关则不灰显」一致）。
func judge(engine, workdir string, paths []string, get func(string) (string, bool), warnf func(string, ...any)) EngineResult {
	opts, enabled, err := ignore.ConfigOptions(engine, get, workdir)
	out := EngineResult{Enabled: enabled, Ignored: []string{}}
	if err != nil {
		warn(warnf, "data-index-ignored: %s 规则不可用（workdir=%q）：%v", engine, workdir, err)
	}
	if !enabled || err != nil || opts == nil {
		return out
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if opts.Ignored(p) {
			out.Ignored = append(out.Ignored, p)
		}
	}
	return out
}

// warn 非致命异常出口（nil → 静默）。
func warn(warnf func(format string, args ...any), format string, args ...any) {
	if warnf != nil {
		warnf(format, args...)
	}
}
