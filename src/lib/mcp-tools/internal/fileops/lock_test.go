package fileops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireLock(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello"), 0644)

	// 第一次加锁应成功
	release1, err := acquireLock(f, 3)
	if err != nil {
		t.Fatalf("first acquire: %s", err)
	}

	// 第二次加锁应失败（锁文件存在）
	_, err = acquireLock(f, 1)
	if err == nil {
		t.Fatal("expected lock conflict")
	}

	release1()

	// 释放后应能重新加锁
	release2, err := acquireLock(f, 3)
	if err != nil {
		t.Fatalf("re-acquire after release: %s", err)
	}
	release2()
}

func TestFileMD5(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello world"), 0644)

	md5, err := fileMD5(f)
	if err != nil {
		t.Fatalf("fileMD5: %s", err)
	}
	// md5("hello world") = 5eb63bbbe01eeed093cb22bb8f5acdc3
	expected := "5eb63bbbe01eeed093cb22bb8f5acdc3"
	if md5 != expected {
		t.Fatalf("md5 mismatch: got %q, want %q", md5, expected)
	}
}

// writeLockFile 写入锁文件（首行 = 持有者 token，次行 = 创建时刻）。
func writeLockFile(t *testing.T, lockPath, token string, modTime time.Time) {
	t.Helper()
	content := token + "\n" + modTime.Format(time.RFC3339)
	if err := os.WriteFile(lockPath, []byte(content), 0644); err != nil {
		t.Fatalf("write lock file: %s", err)
	}
	if err := os.Chtimes(lockPath, modTime, modTime); err != nil {
		t.Fatalf("chtimes: %s", err)
	}
}

// deadPID 找一个几乎必然不存在的 pid（Windows pid 按 4 对齐，取高位段扫描）。
func deadPID() int {
	for p := 4000000; p < 4000400; p += 4 {
		if !processAlive(p) {
			return p
		}
	}
	return 4194304
}

// TestCanBreakLockDeadPid：C-26——锁文件首行 token 的 pid 已死 → 立即突破（不受 30s 限制）；
// pid 存活（本测试进程）→ 不突破；存活但超 aliveHardStaleAge 硬上限 → 强制突破。
func TestCanBreakLockDeadPid(t *testing.T) {
	if !lockProbeSupported {
		t.Skip("平台不支持进程探活 → 恒回落时间规则")
	}
	host, _ := os.Hostname()
	lockPath := filepath.Join(t.TempDir(), "x.txt.chonk.lock")
	now := time.Now()

	// 死 pid：立即突破
	writeLockFile(t, lockPath, fmt.Sprintf("%s:%d:deadbeef", host, deadPID()), now)
	if !canBreakLock(lockPath, now) {
		t.Fatal("死 pid 持有的锁应立即突破（即使未超 30s）")
	}

	// 本机存活 pid：不突破
	writeLockFile(t, lockPath, fmt.Sprintf("%s:%d:feedface", host, os.Getpid()), now)
	if canBreakLock(lockPath, now) {
		t.Fatal("存活进程持有的锁不应突破")
	}

	// 存活 pid + 超硬上限：强制突破
	writeLockFile(t, lockPath, fmt.Sprintf("%s:%d:cafebabe", host, os.Getpid()), now.Add(-aliveHardStaleAge-time.Second))
	if !canBreakLock(lockPath, now.Add(-aliveHardStaleAge-time.Second)) {
		t.Fatal("存活进程锁超硬上限应强制突破")
	}
}

// TestCanBreakLockFallback：C-26 回落路径——token 解析失败（旧格式/损坏）或 host 非本机
// → 回落既有时间规则（未超 30s 不突破；超 30s 突破）。
func TestCanBreakLockFallback(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "y.txt.chonk.lock")
	now := time.Now()
	stale := now.Add(-staleLockAge - time.Second)

	// token 解析失败（旧格式/损坏）→ 回落时间规则
	writeLockFile(t, lockPath, "garbage-lock", now)
	if canBreakLock(lockPath, now) {
		t.Fatal("损坏 token + 未超时不应突破")
	}
	if !canBreakLock(lockPath, stale) {
		t.Fatal("损坏 token + 超时应按时间规则突破")
	}

	// host 非本机（无法探活）→ 回落时间规则
	writeLockFile(t, lockPath, "other-host:12345:abc", now)
	if canBreakLock(lockPath, now) {
		t.Fatal("非本机 host + 未超时不应突破")
	}
	if !canBreakLock(lockPath, stale) {
		t.Fatal("非本机 host + 超时应按时间规则突破")
	}
}

func TestHandleReadFileMD5(t *testing.T) {
	dir := t.TempDir()
	content := "hello world\nline2\n"
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte(content), 0644)

	// 先计算正确 md5
	expectedMD5, _ := fileMD5(f)

	args := map[string]interface{}{
		"files": []interface{}{
			map[string]interface{}{
				"path": f,
				"info": true,
			},
		},
	}
	res := HandleReadFile("", args)
	if !res.Success {
		t.Fatalf("HandleReadFile failed: %s", res.Error)
	}
	// 解析 Output JSON 中的 files
	var out struct {
		Files []map[string]interface{} `json:"files"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("parse output: %s", err)
	}
	if len(out.Files) == 0 {
		t.Fatal("no files returned")
	}
	md5, ok := out.Files[0]["md5"].(string)
	if !ok || md5 == "" {
		t.Fatal("md5 not returned in file info")
	}
	if md5 != expectedMD5 {
		t.Fatalf("md5 mismatch: got %q, want %q", md5, expectedMD5)
	}
}
