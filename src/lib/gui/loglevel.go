// 日志级别动态接线：`logLevel`（prj 键）恢复运行时读点。
// 结论依据：本进程日志走标准库 `log/slog`（非旧 wails `app.go` 的 zap），
// `slog.LevelVar` 支持**运行时 Set**——故 C1 摘除理由「logger 初始化早于配置可用」
// （42-决策记录 §2 C1 / 64-配置项一览 §3.1/§4.1）已不成立：可先用初始级别装好
// logger，待数据面就绪后再读配置覆盖，无需重启。
// 读取一律走**既有**消息面（勿新增）：启动初值 = `data-prj-config-list`；
// 变更即时生效 = 订阅 `data-prj-config-refresh`（persist refresh 广播，载荷含最新 list）。
package gui

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// logLevelKey 是 prj config 表中的日志级别键（64-配置项一览 §4.1）。
const logLevelKey = "logLevel"

// logLevelVar 全局日志级别（`slog.LevelVar` 支持运行时改；默认 info = stdlib 缺省，行为不变）。
var logLevelVar = new(slog.LevelVar)

// initLogging 安装默认 slog logger：stderr（**并可挂文件 sink**，见 logfile.go 的 logWriter）
// + LevelVar 级别（进程内全部 `slog.*` 全局调用据此过滤）。早于配置可用即可调用（初值 info），
// 级别随后由配置动态覆盖；文件 sink 待数据根解析后经 attachFileLog 挂上（此前只进 stderr）。
func initLogging() {
	logLevelVar.Set(slog.LevelInfo)
	slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: logLevelVar})))
	// 把**标准库 log** 也接到同一 sink（D-43）：内嵌库（如 go-webview2 fork）用 `log.Printf`
	// 诊断——windowsgui 下 stderr 不可见，若不接 sink 则建窗失败根因完全不可见。接后与 slog
	// 同落 stderr + 文件 sink（文件 sink 由 attachFileLog 后挂，二者共享同一 logWriter）。
	log.SetOutput(logWriter)
}

// parseLogLevel 把配置值解析为 slog.Level：可识别 debug/info/warn(warning)/error
// （大小写不敏感）；空/未知 → (info, false)。
func parseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// applyLogLevel 应用配置值到 lv：未知/空值**不改动**当前级别，返回是否生效。
func applyLogLevel(lv *slog.LevelVar, raw string) bool {
	lvl, ok := parseLogLevel(raw)
	if !ok {
		return false
	}
	lv.Set(lvl)
	return true
}

// applyLogLevelFromConfig 从 prj 平铺配置 map 取 logLevel 并应用（缺失/非字符串 → false，
// 保持现值）。
func applyLogLevelFromConfig(lv *slog.LevelVar, cfg map[string]any) bool {
	if len(cfg) == 0 {
		return false
	}
	raw, ok := cfg[logLevelKey]
	if !ok {
		return false
	}
	s, _ := raw.(string)
	return applyLogLevel(lv, s)
}

// watchLogLevel 订阅既有 `data-prj-config-refresh`（persist save/delete 后广播，载荷含最新
// `list` 平铺 map）：prj 配置一变更即应用 logLevel（即时生效、无需重启）。解析失败/无 list →
// 静默跳过（读通道异常不阻塞宿主）。
func watchLogLevel(bus mq.Bus) {
	_, _ = bus.On(msgkeys.TopicDataPrjConfigRefresh, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var ev struct {
			List map[string]any `json:"list"`
		}
		if json.Unmarshal(v.Payload, &ev) != nil || ev.List == nil {
			return nil
		}
		applyLogLevelFromConfig(logLevelVar, ev.List)
		return nil
	})
}
