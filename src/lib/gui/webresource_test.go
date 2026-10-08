//go:build windows

package gui

import (
	"strings"
	"testing"
)

// TestReadRequestBodyLimit 请求体读取上限（D-26）：limit 内正常读入；超出 limit 判
// tooLarge（serveWebResource 据此回 413），limit 复用响应方向 maxRecordedBody 常量。
func TestReadRequestBodyLimit(t *testing.T) {
	// ① 恰等于 limit → 正常，不判超限
	body, tooLarge := readRequestBody(strings.NewReader("0123456789abcdef"), 16)
	if tooLarge || string(body) != "0123456789abcdef" {
		t.Fatalf("limit 内应完整读入且不超限：tooLarge=%v body=%q", tooLarge, body)
	}
	// ② limit+1 字节 → tooLarge（LimitReader(limit+1) 探测出越界）
	body, tooLarge = readRequestBody(strings.NewReader("0123456789abcdefg"), 16)
	if !tooLarge {
		t.Fatalf("超出 limit 应判 tooLarge：body=%q", body)
	}
	// ③ 空体 → 正常空读
	body, tooLarge = readRequestBody(strings.NewReader(""), 16)
	if tooLarge || len(body) != 0 {
		t.Fatalf("空体应正常返回：tooLarge=%v body=%q", tooLarge, body)
	}
	// ④ 生产口径：maxRecordedBody 常量本身可作 limit 传入（编译期校验类型口径）
	if _, tooLarge := readRequestBody(strings.NewReader("x"), maxRecordedBody); tooLarge {
		t.Fatal("maxRecordedBody limit 下单字节不应判超限")
	}
}
