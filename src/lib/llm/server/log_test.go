// 本包统一日志出口（log.go，2026-09-20）白盒：
// ① 注入 sink 后同一行**同时**出现在 sink 与 stdout；② 未注入（缺省）时**只**写 stdout，
// 且与既有 fmt.Printf 逐字一致（不额外加换行/前缀）。
package server

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout 把 os.Stdout 换成管道，返回 fn 执行期间写入 stdout 的原文。
// logf 在调用时取 os.Stdout → 替换生效（与既有 fmt.Printf 同口径）。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	return out
}

// TestLogfDefaultStdoutOnly：缺省（未注入 sink）→ 仅 stdout，且逐字一致（无多余换行）。
func TestLogfDefaultStdoutOnly(t *testing.T) {
	setLogSink(nil)
	t.Cleanup(func() { setLogSink(nil) })

	out := captureStdout(t, func() { logf("%s=%d\n", "k", 7) })
	if !strings.Contains(out, "k=7\n") {
		t.Fatalf("缺省应写 stdout 且逐字一致，实际 %q", out)
	}
	if strings.Contains(out, "k=7\n\n") {
		t.Fatalf("不得额外追加换行，实际 %q", out)
	}
}

// TestLogfWritesSinkAndStdout：注入 sink → 同一行同写 sink（且 stdout 行为不变）。
func TestLogfWritesSinkAndStdout(t *testing.T) {
	var sink bytes.Buffer
	setLogSink(&sink)
	t.Cleanup(func() { setLogSink(nil) })

	out := captureStdout(t, func() { logf("[chonkpilot-server][plugin] memory: 保存记忆失败（x）：boom\n") })

	const line = "[chonkpilot-server][plugin] memory: 保存记忆失败（x）：boom\n"
	if got := sink.String(); got != line {
		t.Fatalf("sink 未逐字收到日志行：%q", got)
	}
	if strings.Count(out, line) != 1 {
		t.Fatalf("stdout 应仍收到同一行（且一次），实际 %q", out)
	}
}

// TestPluginLogfAndTaskLogfReachSink：插件日志（pluginLogf）与 task 层告警（taskLayerLogf）
// 是插件失败诊断的实际出口 → 均须经统一出口落 sink（可诊断性缺口 ① 的关键断言）。
// 同时锁定**逐字不变**：前缀 + 收尾换行与改前实现一致（插件侧格式串不带 \n，宿主补一个）。
func TestPluginLogfAndTaskLogfReachSink(t *testing.T) {
	var sink bytes.Buffer
	setLogSink(&sink)
	t.Cleanup(func() { setLogSink(nil) })

	s := &Server{}
	out := captureStdout(t, func() {
		s.pluginLogf("memory: 保存记忆失败（开发规范）：应答超时（跳过）")
		taskLayerLogf("执行侧取消失败（task=%s）: %v", "tk-1", "boom")
	})
	const (
		wantPlugin = "[chonkpilot-server][plugin] memory: 保存记忆失败（开发规范）：应答超时（跳过）\n"
		wantTask   = "[chonkpilot-task] 执行侧取消失败（task=tk-1）: boom\n"
	)
	if got := sink.String(); got != wantPlugin+wantTask {
		t.Fatalf("sink 内容 = %q，want %q", got, wantPlugin+wantTask)
	}
	if strings.Count(out, wantPlugin) != 1 || strings.Count(out, wantTask) != 1 {
		t.Fatalf("stdout 应与 sink 同内容（各一次），实际 %q", out)
	}
}
