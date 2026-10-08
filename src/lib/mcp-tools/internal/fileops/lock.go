package fileops

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// lockSuffix 是跨进程锁文件后缀。
const lockSuffix = ".chonk.lock"

// staleLockAge 超过此时长的锁文件视为陈旧，可突破。
const staleLockAge = 30 * time.Second

// lockRetryCount 修改类操作默认加锁重试次数（30s：100ms × 300，见 acquireLock）。
const lockRetryCount = 300

// acquireLock 对 path 对应的文件加跨进程锁（`<path>.chonk.lock`）。
// 采用 O_CREATE|O_EXCL 原子创建，失败则重试（最多 retry 次，间隔 100ms）。
// 若锁文件超时未释放（>staleLockAge），尝试突破（删除后重试一次）。
// 返回释放函数，调用方须在操作完成后 defer release。
func acquireLock(path string, retry int) (release func(), err error) {
	lockPath := lockFilePath(path)
	// 持有者令牌 = host:pid + 随机串：random 段保证同进程内不同持有者也可区分
	// （跨进程 ABA 与同进程 goroutine ABA 均能被 release 的归属比对识别）。
	payload := lockToken()

	for i := 0; i <= retry; i++ {
		// 尝试创建锁文件
		if f, oerr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644); oerr == nil {
			f.WriteString(payload)
			f.WriteString("\n")
			f.WriteString(time.Now().Format(time.RFC3339))
			f.Close()
			release = func() { unlockFile(lockPath, payload) }
			return release, nil
		}

		// 检查是否超时（陈旧锁）
		if fi, sterr := os.Stat(lockPath); sterr == nil {
			if time.Since(fi.ModTime()) > staleLockAge {
				// 突破陈旧锁
				os.Remove(lockPath)
				continue
			}
		}

		if i < retry {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil, fmt.Errorf("file %s is locked (lockfile: %s)", path, lockPath)
}

// unlockFile 删除锁文件；**仅在归属比对通过时删除**：读回锁文件首行与本次持有者令牌一致
// 才 Remove，否则说明锁已被突破（陈旧被抢）或已由他人持有（ABA）→ 记日志放弃，不误删他人锁。
func unlockFile(lockPath, payload string) {
	b, err := os.ReadFile(lockPath)
	if err != nil {
		return
	}
	if !strings.HasPrefix(string(b), payload+"\n") {
		fmt.Fprintf(os.Stderr, "[fileops] lock owner mismatch, skip unlock: %s\n", lockPath)
		return
	}
	os.Remove(lockPath)
}

// lockToken 生成锁持有者令牌（host:pid:random）。
func lockToken() string {
	host, _ := os.Hostname()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s:%d:%s", host, os.Getpid(), hex.EncodeToString(b))
}

// lockFilePath 返回锁文件路径。
func lockFilePath(path string) string {
	return filepath.Clean(path) + lockSuffix
}

// fileMD5 计算文件内容 MD5 hex。
func fileMD5(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := md5.Sum(data)
	return fmt.Sprintf("%x", h), nil
}
