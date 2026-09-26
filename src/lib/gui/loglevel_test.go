package gui

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestParseLogLevel 解析规则：debug/info/warn(warning)/error（大小写/空白容错）；未知/空 → 不识别。
func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"debug", slog.LevelDebug, true},
		{" DEBUG ", slog.LevelDebug, true},
		{"info", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"", slog.LevelInfo, false},
		{"verbose", slog.LevelInfo, false},
	}
	for _, c := range cases {
		lv, ok := parseLogLevel(c.in)
		if lv != c.want || ok != c.ok {
			t.Fatalf("parseLogLevel(%q) = %v,%v; want %v,%v", c.in, lv, ok, c.want, c.ok)
		}
	}
}

// TestApplyLogLevelDynamic 运行时可改：未知值不改动现值；合法值即时生效（无需重启）。
func TestApplyLogLevelDynamic(t *testing.T) {
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelInfo)

	if applyLogLevel(lv, "verbose") || lv.Level() != slog.LevelInfo {
		t.Fatalf("unknown value should not change level, got %v", lv.Level())
	}
	if !applyLogLevel(lv, "debug") || lv.Level() != slog.LevelDebug {
		t.Fatalf("apply debug failed, got %v", lv.Level())
	}
	if !applyLogLevel(lv, "error") || lv.Level() != slog.LevelError {
		t.Fatalf("apply error failed, got %v", lv.Level())
	}
}

// TestLogLevelGate 级别变化对实际输出的可见效果：info 下 debug 记录消失，debug 下出现。
func TestLogLevelGate(t *testing.T) {
	lv := new(slog.LevelVar)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lv}))

	applyLogLevel(lv, "info")
	logger.Debug("hidden-debug")
	if strings.Contains(buf.String(), "hidden-debug") {
		t.Fatalf("debug record should be filtered at info level: %q", buf.String())
	}

	applyLogLevel(lv, "debug")
	buf.Reset()
	logger.Debug("shown-debug")
	if !strings.Contains(buf.String(), "shown-debug") {
		t.Fatalf("debug record should appear at debug level: %q", buf.String())
	}
}

// TestApplyLogLevelFromConfig 从 prj 平铺配置 map 取值：缺失/非字符串 → 保持现值；字符串 → 生效。
func TestApplyLogLevelFromConfig(t *testing.T) {
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelInfo)

	if applyLogLevelFromConfig(lv, nil) || lv.Level() != slog.LevelInfo {
		t.Fatalf("nil cfg should not change level")
	}
	if applyLogLevelFromConfig(lv, map[string]any{"other": "x"}) || lv.Level() != slog.LevelInfo {
		t.Fatalf("missing key should not change level")
	}
	if !applyLogLevelFromConfig(lv, map[string]any{"logLevel": "debug"}) || lv.Level() != slog.LevelDebug {
		t.Fatalf("config debug not applied, got %v", lv.Level())
	}
	if applyLogLevelFromConfig(lv, map[string]any{"logLevel": 3}) || lv.Level() != slog.LevelDebug {
		t.Fatalf("non-string value should be ignored, got %v", lv.Level())
	}
}
