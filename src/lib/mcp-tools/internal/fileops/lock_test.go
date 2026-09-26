package fileops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
