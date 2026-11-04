// D-66 / ERR-002 接线白盒：llm 侧把「系统网络状态」检测注入 router 的重试选项。
package server

import "testing"

// TestRouterCallOptionsWiresNetworkReady：`routerCallOptions` 必须接入系统网络检测（非 nil），
// 且读的是**当前**检测结果（单测可替换 `systemNetworkConnected` 模拟断网 / 在线）。
func TestRouterCallOptionsWiresNetworkReady(t *testing.T) {
	orig := systemNetworkConnected
	t.Cleanup(func() { systemNetworkConnected = orig })

	systemNetworkConnected = func() bool { return false }
	co := routerCallOptions(ChatOptions{})
	if co.NetworkReady == nil {
		t.Fatal("router.CallOptions.NetworkReady 未接入系统网络检测")
	}
	if co.NetworkReady() {
		t.Fatal("应读到注入的『断网』状态")
	}

	systemNetworkConnected = func() bool { return true }
	if !co.NetworkReady() {
		t.Fatal("应读到注入的『在线』状态")
	}
}
