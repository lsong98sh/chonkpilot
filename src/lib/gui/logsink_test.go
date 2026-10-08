// GUI 装配侧「同一文件 sink」集成测试（2026-09-20）：
// 宿主把 `logWriter.fileSink()`（= 同一个滚动文件 `<dataDir>/logs/gui.log`）注入内嵌
// chonkpilot-llm/server（`Options.LogWriter`）→ server/插件的**真实诊断行**同时落
// stdout 与 gui.log（此前只写 stdout：-H windowsgui 下无控制台 → 文件里一行都没有）。
package gui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-llm/server"
)

// captureFileStdout 把 os.Stdout 换成管道，返回 fn 执行期间写入 stdout 的原文。
func captureFileStdout(t *testing.T, fn func()) string {
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

// TestServerLogsReachGuiLogFile：真实 server（含内嵌装配）的一条失败诊断行
// → 既写 stdout（行为不变）又落 <dataDir>/logs/gui.log（本次修复的判据）。
func TestServerLogsReachGuiLogFile(t *testing.T) {
	dir := t.TempDir()
	logDir, err := attachFileLog(dir)
	if err != nil {
		t.Fatalf("attachFileLog: %v", err)
	}
	t.Cleanup(detachFileLog)
	t.Cleanup(data.Reset)

	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	// 实测触发点：`usr.db` 指向**已存在的目录** → bbolt 打不开 → 装配期读 usr 配置失败 →
	// server 记 `reconcileLLMProviders: UserConfigGet failed`（改前该行只进 stdout、gui.log 中查不到）。
	out := captureFileStdout(t, func() {
		_ = server.New(bus, server.Options{
			UsrPath:   dir,                  // 目录（非文件）→ OpenLayer 失败，触发失败日志
			LogWriter: logWriter.fileSink(), // ← 装配方注入：与宿主 slog 同一文件 sink
		})
	})

	b, err := os.ReadFile(filepath.Join(logDir, guiLogFileName))
	if err != nil {
		t.Fatalf("read gui.log: %v", err)
	}
	const want = "UserConfigGet failed"
	if !strings.Contains(string(b), want) {
		t.Fatalf("gui.log 未收到 server 诊断行（want %q），实际文件内容：\n%s", want, string(b))
	}
	if !strings.Contains(out, want) {
		t.Fatalf("stdout 行为应保持不变（仍收到同一行），实际：%q", out)
	}
	t.Logf("raw evidence: gui.log 含 server 失败行 = %v；stdout 含同一行 = %v",
		strings.Contains(string(b), want), strings.Contains(out, want))
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, want) {
			t.Logf("raw evidence: gui.log 行摘录 = %s", line)
		}
	}
}
