//go:build windows

package gui

import "testing"

// TestIsAppOrigin 虚拟宿主源判定（E5-②）：必须**精确** scheme + host 相等，非前缀匹配——
// 否则 `https://app.localhost.evil.com/...` 会因前缀命中而误走本地 handler 应答。
func TestIsAppOrigin(t *testing.T) {
	cases := []struct {
		uri  string
		want bool
	}{
		{"https://app.localhost/", true},
		{"https://app.localhost/index.html", true},
		{"https://app.localhost/publish", true},
		{"https://app.localhost.evil.com/x", false}, // 前缀匹配漏洞：host 后缀
		{"http://app.localhost/", false},            // scheme 不符
		{"https://app.localhost:8443/", false},      // host 含端口
		{"https://evil.com/app.localhost", false},   // host 不符
		{"https://APP.LOCALHOST/", false},           // host 大小写不同（URL host 小写归一后仍不等）
		{"", false},
		{"://bad", false},
	}
	for _, c := range cases {
		if got := isAppOrigin(c.uri); got != c.want {
			t.Errorf("isAppOrigin(%q) = %v, want %v", c.uri, got, c.want)
		}
	}
}
