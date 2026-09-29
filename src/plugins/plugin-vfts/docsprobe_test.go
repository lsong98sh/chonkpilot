package vfts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeStateFile 写一个转换器状态文件，返回其路径。
func writeStateFile(t *testing.T, dir string, st docState) string {
	t.Helper()
	p := filepath.Join(dir, "state.json")
	b, _ := json.Marshal(st)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestProbeConverterPaths：状态文件 + pid 校验通过 → 可用（取 port/token/version）；token 不入日志。
func TestProbeConverterPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vfts/health" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "pid": 4321, "port": 7317, "version": "0.1.0"})
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))

	dir := t.TempDir()
	path := writeStateFile(t, dir, docState{Pid: 4321, Port: port, Token: "SECRET-TOKEN", Version: "0.1.0"})

	var logs []string
	svc := probeConverterPaths([]string{path}, func(f string, a ...any) { logs = append(logs, f) })
	if svc == nil {
		t.Fatal("pid 一致且探活成功 → 应视为可用")
	}
	if svc.Port != port || svc.Token != "SECRET-TOKEN" || svc.Version != "0.1.0" {
		t.Fatalf("探测结果不符：%+v", svc)
	}
	if got := svc.baseURL(); got != "http://127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("baseURL=%q", got)
	}
	// 安全：日志不得含 token
	for _, l := range logs {
		if strings.Contains(l, "SECRET-TOKEN") {
			t.Fatalf("日志泄露 token：%q", l)
		}
	}
}

// TestProbeConverterAbsent：pid 不一致 / 探活失败 / 状态文件缺失 → 一律视为「未运行」（不报错）。
func TestProbeConverterAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "pid": 111, "version": "0.1.0"})
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))

	dir := t.TempDir()
	// ① pid 不一致（陈旧状态文件）
	sub := filepath.Join(dir, "a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	mismatch := writeStateFile(t, sub, docState{Pid: 999, Port: port, Token: "t"})
	if svc := probeConverterPaths([]string{mismatch}, nil); svc != nil {
		t.Fatalf("pid 不一致应视为未运行：%+v", svc)
	}
	// ② 状态文件缺失
	if svc := probeConverterPaths([]string{filepath.Join(dir, "nope.json")}, nil); svc != nil {
		t.Fatalf("状态文件缺失应视为未运行：%+v", svc)
	}
	// ③ 探活失败（端口无服务）
	subB := filepath.Join(dir, "b")
	if err := os.MkdirAll(subB, 0o755); err != nil {
		t.Fatal(err)
	}
	dead := writeStateFile(t, subB, docState{Pid: 1, Port: 1, Token: "t"})
	if svc := probeConverterPaths([]string{dead}, nil); svc != nil {
		t.Fatalf("探活失败应视为未运行：%+v", svc)
	}
}

// TestReadDocStateInvalid：非法/半截状态文件 → ok=false（不 panic）。
func TestReadDocStateInvalid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	_ = os.WriteFile(p, []byte("{ not json"), 0o644)
	if _, ok := readDocState(p); ok {
		t.Fatal("非法 JSON 应 ok=false")
	}
	_ = os.WriteFile(p, []byte(`{"pid":0,"port":0}`), 0o644)
	if _, ok := readDocState(p); ok {
		t.Fatal("pid/port 缺失应 ok=false")
	}
}
