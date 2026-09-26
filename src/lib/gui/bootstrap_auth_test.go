// 首屏注入（`window.__ck`）的认证标记（阶段 2b-2；61 §4.6「首屏注入」）——GUI 宿主侧。
//
// 断言要点：`requireAuth` = **形态决定**（desktop=false；gui=true，由 llm server 的
// `RequireAuth()` 判定）；`authed` = 读桥持有的令牌判定（`bridge.Authed`）；`form` = 壳传入
// 的形态；`instanceId` = 入口绑定值（未认证时亦注入 —— 不构成凭据，22 §2）。
package gui

import (
	"encoding/json"
	"testing"
)

// ckPayload 取注入脚本里的 `window.__ck` 字面量并解析。
func ckPayload(t *testing.T, form, id string, requireAuth, authed bool) map[string]any {
	t.Helper()
	raw := bootstrapJSON(form, id, requireAuth, authed)
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("bootstrapJSON 应输出合法 JSON 字面量：%q err=%v", raw, err)
	}
	return m
}

// TestBootstrapJSONAuthFlags：字段齐备且与形态/凭证判定一致（desktop 免鉴权但不谎报
// authed；gui 要求认证）。
func TestBootstrapJSONAuthFlags(t *testing.T) {
	// desktop（桌面单体默认构建）：requireAuth=false（本机单用户免鉴权）
	sa := ckPayload(t, "desktop", "ins-1", false, false)
	if sa["form"] != "desktop" || sa["requireAuth"] != false || sa["instanceId"] != "ins-1" {
		t.Fatalf("desktop 注入不符：%v", sa)
	}
	// gui（分离形态）：requireAuth=true；authed 随凭证（未登录 = false）
	d0 := ckPayload(t, "gui", "ins-2", true, false)
	if d0["form"] != "gui" || d0["requireAuth"] != true || d0["authed"] != false {
		t.Fatalf("gui 未认证注入不符：%v", d0)
	}
	d1 := ckPayload(t, "gui", "ins-2", true, true)
	if d1["authed"] != true {
		t.Fatalf("gui 已认证注入不符：%v", d1)
	}
}

// TestRuntimeFormIsKnown：宿主形态取值属三形态集合（61 §4.6；由壳经 Options.Form 传入）。
func TestRuntimeFormIsKnown(t *testing.T) {
	for _, f := range []string{FormDesktop, FormGui} {
		switch f {
		case "desktop", "gui":
		default:
			t.Fatalf("Options.Form 应为 desktop / gui，got %q", f)
		}
	}
}
