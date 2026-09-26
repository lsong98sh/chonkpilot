// 执行层路径键**分层读取**（prj > usr > 默认 ""，2026-09-19 接线）白盒：
//   - 纯函数 layeredPathValues 的优先级/空值口径；
//   - 端到端：prj 路径键写入 → 执行层（mcp-server 解释器注入 / {{toolchain.*}}）实际取到 prj 值，
//     删 prj 键 → 回落 usr（既有读法）。
package server

import "testing"

// TestLayeredPathValuesPrecedence：prj > usr > 默认；prj 空串视为未覆盖（回落 usr）。
func TestLayeredPathValuesPrecedence(t *testing.T) {
	usr := map[string]any{"javaPath": `C:\usr\java.exe`, "pythonPath": `C:\usr\py.exe`}
	const prjJava = `C:\prj\java.exe`

	// ① prj 覆盖 usr；prj 缺省项回落 usr；两层皆缺 → 默认 ""
	// 注（41 G-34）：prj 侧取值改为门面 DTO 的领域形态（键 → **值字符串**，ConfigKVList 的
	// list 即此形状），故用例字面量类型随之由 map[string]any 改为 map[string]string。
	got := layeredPathValues(usr, map[string]string{"javaPath": prjJava})
	if got["javaPath"] != prjJava {
		t.Fatalf("prj 应覆盖 usr: javaPath = %q，want %q", got["javaPath"], prjJava)
	}
	if got["pythonPath"] != `C:\usr\py.exe` {
		t.Fatalf("prj 缺省应回落 usr: pythonPath = %q", got["pythonPath"])
	}
	if got["goPath"] != "" {
		t.Fatalf("两层皆缺应为默认空: goPath = %q", got["goPath"])
	}

	// ② prj 空串 = 未覆盖 → 回落 usr（避免"置空"误清 usr）
	got = layeredPathValues(usr, map[string]string{"javaPath": "  "})
	if got["javaPath"] != `C:\usr\java.exe` {
		t.Fatalf("prj 空值应回落 usr: javaPath = %q", got["javaPath"])
	}

	// ③ 两层皆缺 → 全部默认 ""
	got = layeredPathValues(nil, nil)
	for _, k := range pathConfigKeys {
		if got[k] != "" {
			t.Fatalf("两层皆缺应全空: %s = %q", k, got[k])
		}
	}

	// ④ 仅 prj 有值（usr 无）→ 取 prj
	got = layeredPathValues(nil, map[string]string{"rustPath": `C:\prj\rust.exe`})
	if got["rustPath"] != `C:\prj\rust.exe` {
		t.Fatalf("仅 prj 有值时应取 prj: rustPath = %q", got["rustPath"])
	}
}

// TestPrjPathOverridesUsrAtExecutionLayer：真机链路 —— prj 路径键写入后执行层取 prj 值，
// 删 prj 键回落 usr（含 mcp-server 解释器注入与 toolchain 占位符同源）。
func TestPrjPathOverridesUsrAtExecutionLayer(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)
	registerTestInstance(t, s)

	const usrPy, prjPy = `C:\usr\python.exe`, `C:\prj\python.exe`
	saveTestUserConfig(t, s, map[string]any{"pythonPath": usrPy})

	// ① 仅 usr → 执行层取 usr（既有行为不变）
	s.loadExecConfig("ins-test")
	if got := s.mcpCfg.Interpreters["python"]; got != usrPy {
		t.Fatalf("仅 usr 时解释器应取 usr: %q，want %q", got, usrPy)
	}

	// ② 写 prj → 执行层取 prj（prj > usr）
	saveTestPrjConfig(t, s, "pythonPath", prjPy)
	s.loadExecConfig("ins-test")
	if got := s.mcpCfg.Interpreters["python"]; got != prjPy {
		t.Fatalf("prj 应覆盖 usr: 解释器 = %q，want %q", got, prjPy)
	}
	if got := s.toolchainVars("ins-test")["python"]; got != prjPy {
		t.Fatalf("{{toolchain.python}} 应与执行层同源取 prj: %q，want %q", got, prjPy)
	}

	// ③ 删 prj 键 → 回落 usr
	res := dataCall(t, s, "data-prj-config-delete", map[string]any{
		"req_id": "path-del", "instance_id": "ins-test",
		"data": map[string]any{"id": "pythonPath"},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("删 prj 键失败: %+v", res)
	}
	s.loadExecConfig("ins-test")
	if got := s.mcpCfg.Interpreters["python"]; got != usrPy {
		t.Fatalf("删 prj 后应回落 usr: 解释器 = %q，want %q", got, usrPy)
	}
}
