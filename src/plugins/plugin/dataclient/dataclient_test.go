package dataclient

import "testing"

// TestStrval：配置/结果值归一（三插件原先各自一份的同名测试，随实现收口到此）。
func TestStrval(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{"hello", "hello"},
		{"", ""},
		{true, "true"},
		{false, "false"},
		{nil, ""},
		{42, "42"},
		{3.14, "3.14"},
	}
	for _, tt := range tests {
		got := Strval(tt.in)
		if got != tt.want {
			t.Errorf("Strval(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
