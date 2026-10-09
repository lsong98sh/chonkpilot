package bridge_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-gui/bridge"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// dataReqTimeout 对齐 bridge/data.go 的私有超时常量（黑盒测试无法引用私有 const；
// bridge/data.go: dataReqTimeout = 3 * time.Second）。
const dataReqTimeout = 3 * time.Second

// subRaw 以 v2 On 订阅主题，把回调适配为载荷风格 func(subject, payload)——测试收集器
// 保持简洁形态（仅测试用；总线收发 API = v2 On/Emit）。
func subRaw(bus mq.Bus, subject string, h func(subject string, payload []byte)) (mq.Sub, error) {
	return bus.On(subject, 0, func(_ context.Context, subject string, v *mq.Value) error {
		h(subject, v.Payload)
		return nil
	})
}

// pubFire fire-and-forget 发布（仅测试用；= v2 Emit 不 await，忽略派发结果）。
func pubFire(bus mq.Bus, subject string, payload any) {
	_ = bus.Emit(context.Background(), subject, payload)
}

// evalEnvelope 对齐 bridge 内部事件信封（bridge.go envelope：type/payload/src），
// 黑盒测试解析 eval stub 捕获的脚本用。
type evalEnvelope struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Source  string `json:"src"`
}

// evalCollect 构造注入 bridge.New 的 eval stub（生产 = webview eval）：把收到的脚本
// 非阻塞送入 channel 供断言。
func evalCollect(ch chan<- string) bridge.Eval {
	return func(script string) {
		select {
		case ch <- script:
		default:
		}
	}
}

// newTestBridge 建内存总线 + 桥（黑盒构造，不 Start、不碰私有接线：dataViaPersist/
// gui.* 经公开面 PublishEvent/PrjConfigLoad 驱动，无需 ">" 订阅；需要转发转 eval 的
// 用例在测试内自行 Start()）。
func newTestBridge(t *testing.T, eval bridge.Eval) (*bridge.Bridge, mq.Bus) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	br := bridge.New("inst-test", t.TempDir(), t.TempDir(), eval, bus)
	return br, bus
}

// recvEnvelope 从 eval stub 通道取一条 emitRemote envelope（超时保护）。
func recvEnvelope(t *testing.T, ch <-chan string, d time.Duration) evalEnvelope {
	t.Helper()
	const p = "window.mq.emitRemote("
	select {
	case script := <-ch:
		if !strings.HasPrefix(script, p) || !strings.HasSuffix(script, ")") {
			t.Fatalf("eval 脚本形态异常: %s", script)
		}
		var env evalEnvelope
		body := strings.TrimSuffix(strings.TrimPrefix(script, p), ")")
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("envelope 解析失败: %v (%s)", err, script)
		}
		return env
	case <-time.After(d):
		t.Fatalf("等待 eval envelope 超时")
		return evalEnvelope{}
	}
}

// mockPersist 模拟 persist 服务订阅 data-<subject>：应答发布到请求同一主题
// （{req_id, ok, result} / {req_id, ok:false, error}）；跳过带 ok 的应答（对齐 persist 防环）。
func mockPersist(t *testing.T, bus mq.Bus, subject string, reply map[string]any) {
	t.Helper()
	_, err := subRaw(bus, subject, func(_ string, payload []byte) {
		var check struct {
			OK *bool `json:"ok"`
		}
		if json.Unmarshal(payload, &check) == nil && check.OK != nil {
			return
		}
		var req struct {
			ReqID string `json:"req_id"`
		}
		_ = json.Unmarshal(payload, &req)
		out := map[string]any{"req_id": req.ReqID}
		for k, v := range reply {
			out[k] = v
		}
		raw, _ := json.Marshal(out)
		pubFire(bus, subject, raw)
	})
	if err != nil {
		t.Fatalf("mock persist subscribe: %v", err)
	}
}

// TestDataViaPersistOK：成功应答 → persist result 直接作为结果返回（透传）。
// 黑盒驱动：PublishEvent 对 data- 前缀即 dataViaPersist，行为一致。
func TestDataViaPersistOK(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()
	gotReq := make(chan map[string]any, 1)
	_, err := subRaw(bus, "data-scenario-list", func(_ string, payload []byte) {
		var req map[string]any
		if err := json.Unmarshal(payload, &req); err == nil {
			select {
			case gotReq <- req:
			default:
			}
		}
	})
	if err != nil {
		t.Fatalf("capture subscribe: %v", err)
	}
	mockPersist(t, bus, "data-scenario-list", map[string]any{
		"ok":     true,
		"result": map[string]any{"ok": true, "list": []any{map[string]any{"id": 1}}},
	})

	res, errs := br.PublishEvent("data-scenario-list", `{"filter":{"x":1}}`)
	if len(errs) != 0 {
		t.Fatalf("errs 应为空: %v", errs)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result 类型异常: %T", res)
	}
	if m["ok"] != true {
		t.Fatalf("result.ok 应为 true: %+v", m)
	}
	if _, ok := m["list"].([]any); !ok {
		t.Fatalf("result.list 缺失: %+v", m)
	}
	req := <-gotReq
	if req["instance_id"] != "inst-test" {
		t.Fatalf("请求未注入 instance_id: %+v", req)
	}
	if s, _ := req["req_id"].(string); s == "" {
		t.Fatalf("请求未注入 req_id: %+v", req)
	}
	if _, ok := req["filter"].(map[string]any); !ok {
		t.Fatalf("原请求字段 filter 丢失: %+v", req)
	}
}

// TestDataViaPersistFail：persist {ok:false, error} → 错误信封 {ok:false,error} + errs。
func TestDataViaPersistFail(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()
	mockPersist(t, bus, "data-user-config-load", map[string]any{"ok": false, "error": "boom"})

	res, errs := br.PublishEvent("data-user-config-load", `{}`)
	if len(errs) != 1 {
		t.Fatalf("errs 应有一条: %v", errs)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result 类型异常: %T", res)
	}
	if m["ok"] != false || m["error"] != "boom" {
		t.Fatalf("失败信封异常: %+v", m)
	}
}

// TestEventTypeDataKeepsOriginalName：data-* 主题（含 refresh 广播）原名直通（黑盒版）——
// 桥 Start() 注册 ">" 全通配订阅后，直接在总线上发布 data-* 事件，桥经 forwardEvent 转
// 前端 eval 的信封 type 保持原名（前端 onDataRefresh 订阅 data-<domain>-refresh 才能收到）。
func TestEventTypeDataKeepsOriginalName(t *testing.T) {
	evals := make(chan string, 16)
	br, bus := newTestBridge(t, evalCollect(evals))
	defer bus.Close()
	if err := br.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, subj := range []string{"data-scenario-refresh", "data-user-config-refresh", "data-scenario-list", "data-tasktree-delete"} {
		pubFire(bus, subj, []byte(`{"note":"refresh"}`))
		env := recvEnvelope(t, evals, time.Second)
		if env.Type != subj {
			t.Errorf("转发 type = %q, want 原名直通 %q", env.Type, subj)
		}
		if env.Source != "mq" {
			t.Errorf("转发 src = %q, want mq", env.Source)
		}
	}
}

// TestDataViaPersistTimeout（X1 收口）：persist 恒在线（GUI 恒启 server），data-* 透传
// 无应答应走 3s 超时兜底——返回 {ok:false,error} 信封 + errs，不无限挂起。
// （no-server 离线快败窗已退役删除，超时兜底为唯一无应答保护。）
func TestDataViaPersistTimeout(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()
	start := time.Now()
	res, errs := br.PublishEvent("data-prj-config-load", `{"id":"layout"}`)
	if len(errs) != 1 {
		t.Fatalf("无应答应超时返回 errs: %v", errs)
	}
	if el := time.Since(start); el < dataReqTimeout-time.Second {
		t.Fatalf("应等满超时兜底（≈%v），实际耗时 %v", dataReqTimeout, el)
	}
	m, ok := res.(map[string]any)
	if !ok || m["ok"] != false {
		t.Fatalf("超时失败信封异常: %+v", res)
	}
}

// TestRecentListContract（A6，替代原 TestRecentDirsMemory）：recordRecentDir/recentDirsList
// 为桥内私有内存面（记录入口经系统目录打开对话框，黑盒不可自动化），改验公开契约空态——
// PublishEvent("gui.recent.list") 返回 {dirs:[]}（guimsg.go recent.list → callGetRecentDirs）。
func TestRecentListContract(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()

	res, errs := br.PublishEvent("gui.recent.list", "{}")
	if len(errs) != 0 {
		t.Fatalf("recent.list errs 应为空: %v", errs)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("recent.list 结果类型异常: %T", res)
	}
	dirs, ok := m["dirs"].([]any)
	if !ok {
		t.Fatalf("recent.list 应含 dirs 数组: %+v", m)
	}
	if len(dirs) != 0 {
		t.Fatalf("空态 dirs 应为空数组，got %v", dirs)
	}
}

// TestRecentRemoveContract（最近项目下拉删除）：gui.recent.remove 仅删 usr config 自由键
// recent_dirs 中的**该条记录**并回写剩余列表（保序），**不触碰项目目录 / 数据资产**。
// 黑盒驱动：PublishEvent("gui.recent.remove") → guiDo recent.remove → callRemoveRecentDir；
// data-user-config-load/save 以桩应答（同一总线），并断言目标目录的文件**仍然存在**。
func TestRecentRemoveContract(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()

	// 真实项目目录 + 标记文件（验证删除记录不删资产）。
	projB := filepath.Join(t.TempDir(), "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(projB, "keep.txt")
	if err := os.WriteFile(marker, []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := []string{"D:/a", projB, "D:/c"}

	// 桩：读取 usr 配置 → 含 recent_dirs 的 JSON 数组字符串。
	rawDirs, _ := json.Marshal(dirs)
	mockPersist(t, bus, "data-user-config-load", map[string]any{
		"ok":     true,
		"result": map[string]any{"data": map[string]any{"recent_dirs": string(rawDirs)}},
	})
	// 桩：捕获 data-user-config-save 写入并回显成功应答。
	saved := make(chan map[string]any, 1)
	_, err := subRaw(bus, "data-user-config-save", func(_ string, payload []byte) {
		var check struct {
			OK *bool `json:"ok"`
		}
		if json.Unmarshal(payload, &check) == nil && check.OK != nil {
			return // 跳过应答
		}
		var req struct {
			ReqID string         `json:"req_id"`
			Data  map[string]any `json:"data"`
		}
		if json.Unmarshal(payload, &req) != nil || req.ReqID == "" {
			return
		}
		select {
		case saved <- req.Data:
		default:
		}
		reply, _ := json.Marshal(map[string]any{
			"req_id": req.ReqID, "ok": true,
			"result": map[string]any{"ok": true, "id": "user_config"},
		})
		pubFire(bus, "data-user-config-save", reply)
	})
	if err != nil {
		t.Fatalf("data-user-config-save 桩订阅失败: %v", err)
	}

	res, errs := br.PublishEvent("gui.recent.remove", `{"path":`+mustJSON(projB)+`}`)
	if len(errs) != 0 {
		t.Fatalf("recent.remove errs 应为空: %v", errs)
	}
	if m, ok := res.(map[string]any); !ok || m["ok"] != true {
		t.Fatalf("recent.remove 应回 {ok:true}，got %+v", res)
	}

	select {
	case data := <-saved:
		got, _ := data["recent_dirs"].(string)
		var gotDirs []string
		if json.Unmarshal([]byte(got), &gotDirs) != nil {
			t.Fatalf("回写 recent_dirs 非 JSON 数组: %q", got)
		}
		if len(gotDirs) != 2 || gotDirs[0] != "D:/a" || gotDirs[1] != "D:/c" {
			t.Fatalf("仅应删该条并保序，got %v", gotDirs)
		}
		if len(data) != 1 {
			t.Fatalf("只应写 recent_dirs 一个键，got %+v", data)
		}
	case <-time.After(time.Second):
		t.Fatalf("未捕获 data-user-config-save 写入")
	}

	// 边界：项目目录与标记文件未被触碰（只删记录，不删资产）。
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("删除记录不得删除项目资产：marker 丢失 %v", err)
	}
}

// mustJSON 把字符串编为 JSON（测试内拼装路径 payload 用）。
func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// noOKFilter 只保留无 ok 字段的请求载荷（应答/回声带 ok，跳过）——供契约测试采集请求。
func noOKFilter(t *testing.T, bus mq.Bus, subject string, ch chan map[string]any) {
	t.Helper()
	_, err := subRaw(bus, subject, func(_ string, payload []byte) {
		var check struct {
			OK *bool `json:"ok"`
		}
		if json.Unmarshal(payload, &check) == nil && check.OK != nil {
			return
		}
		var req map[string]any
		if err := json.Unmarshal(payload, &req); err == nil {
			select {
			case ch <- req:
			default:
			}
		}
	})
	if err != nil {
		t.Fatalf("capture subscribe %s: %v", subject, err)
	}
}

// TestPrjConfigHelperContract（A6）：gui.* 状态落库/恢复经 prj-config 键值面透传的请求
// 载荷契约——save {data:{entries:{…}}}（批量）/ load 顶层 {id} / list {}，对齐 persist
// handleConfigKV 与前端 dataClient（改动通道后 front 契约不变）。
// 黑盒驱动：save 侧经 PublishEvent("gui.ui.save")（guiDo → callSaveLayoutState →
// dataViaPersist），load 侧经公开 PrjConfigLoad。
func TestPrjConfigHelperContract(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()
	saveCh := make(chan map[string]any, 1)
	loadCh := make(chan map[string]any, 1)
	noOKFilter(t, bus, "data-prj-config-save", saveCh)
	noOKFilter(t, bus, "data-prj-config-load", loadCh)
	mockPersist(t, bus, "data-prj-config-save", map[string]any{
		"ok": true, "result": map[string]any{"ok": true, "id": "layout"},
	})
	mockPersist(t, bus, "data-prj-config-load", map[string]any{
		"ok": true, "result": map[string]any{"data": `{"x":1}`},
	})

	// save 侧（v6 细 key，12-数据层）：layout 字段逐个落 layout.<字段> 键，
	// 值转存储字符串；persist 侧再把该前缀键路由到 prjusr（团队库不被个人运行态污染）。
	res, errs := br.PublishEvent("gui.ui.save", `{"layout":{"x":1}}`)
	if len(errs) != 0 {
		t.Fatalf("ui.save errs 应为空: %v", errs)
	}
	if m, ok := res.(map[string]any); !ok || m["ok"] != true {
		t.Fatalf("ui.save 应返回 {ok:true}: %+v", res)
	}
	req := <-saveCh
	data, _ := req["data"].(map[string]any)
	// D-33：layout/window 由「逐键 save」收敛为「一次批量 entries」（与 data-prj-config 批量 save 同口径）。
	entries, _ := data["entries"].(map[string]any)
	if v, ok := entries["layout.x"].(string); !ok || v != "1" {
		t.Fatalf("save 请求载荷异常（应为批量 entries: layout.x=1）: %+v", req)
	}

	// load 侧：PrjConfigLoad 返回应答 result.data 值字符串。
	if v := br.PrjConfigLoad("layout.x"); v != `{"x":1}` {
		t.Fatalf("PrjConfigLoad 应返回值字符串，got %q", v)
	}
	req = <-loadCh
	if req["id"] != "layout.x" {
		t.Fatalf("load 请求应携带顶层 id: %+v", req)
	}
}

// TestOpenedFilesLogicalPath（G-24）：gui.ui.save{opened_files} 落库为 workdir 相对逻辑路径
// （workdir 外路径原样）；gui.init-data 恢复时展开回绝对（兼容旧数据里的绝对路径）。
func TestOpenedFilesLogicalPath(t *testing.T) {
	wd := t.TempDir()
	outsideDir := t.TempDir()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	defer bus.Close()
	br := bridge.New("inst-test", wd, t.TempDir(), func(string) {}, bus)

	absIn := filepath.Join(wd, "src", "a.txt")
	outside := filepath.Join(outsideDir, "outside.txt")
	if err := os.MkdirAll(filepath.Dir(absIn), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, p := range []string{absIn, outside} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	// save 侧：落库为 workdir 相对（斜杠归一）；workdir 外保持绝对。
	saveCh := make(chan map[string]any, 4)
	noOKFilter(t, bus, "data-prj-config-save", saveCh)
	mockPersist(t, bus, "data-prj-config-save", map[string]any{"ok": true, "result": map[string]any{"ok": true}})
	payload, _ := json.Marshal(map[string]any{"opened_files": []string{absIn, outside}})
	if _, errs := br.PublishEvent("gui.ui.save", string(payload)); len(errs) > 0 {
		t.Fatalf("ui.save errs: %v", errs)
	}
	req := <-saveCh
	data, _ := req["data"].(map[string]any)
	if data["key"] != "opened-files" {
		t.Fatalf("应写 opened-files 键，got %+v", req)
	}
	var stored []string
	if err := json.Unmarshal([]byte(data["value"].(string)), &stored); err != nil {
		t.Fatalf("值非 JSON 数组: %v", err)
	}
	if len(stored) != 2 || stored[0] != "src/a.txt" || stored[1] != outside {
		t.Fatalf("落库应为 [workdir 相对, workdir 外原样]，got %v", stored)
	}

	// load 侧：逻辑相对 → 展开绝对；旧绝对路径原样保留。
	mockPersist(t, bus, "data-prj-config-list", map[string]any{"ok": true, "result": map[string]any{"list": map[string]any{
		"opened-files": `["src/a.txt",` + mustJSON(outside) + `]`,
	}}})
	mockPersist(t, bus, "data-user-config-load", map[string]any{"ok": true, "result": map[string]any{"data": map[string]any{}}})
	res, errs := br.PublishEvent("gui.init-data", `{}`)
	if len(errs) > 0 {
		t.Fatalf("init-data errs: %v", errs)
	}
	m, _ := res.(map[string]any)
	opened, _ := m["openedFiles"].([]any)
	if len(opened) != 2 {
		t.Fatalf("init-data 应恢复 2 个打开文件，got %+v", m["openedFiles"])
	}
	if opened[0] != absIn || opened[1] != outside {
		t.Fatalf("恢复应展开为绝对（旧绝对原样），got %v (want %v, %v)", opened, absIn, outside)
	}
}

// recvEnvelopeTyp 从 eval stub 通道取到指定 type 的 envelope（跳过其他，超时失败）。
func recvEnvelopeTyp(t *testing.T, ch <-chan string, typ string, d time.Duration) evalEnvelope {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case script := <-ch:
			const p = "window.mq.emitRemote("
			var env evalEnvelope
			body := strings.TrimSuffix(strings.TrimPrefix(script, p), ")")
			if err := json.Unmarshal([]byte(body), &env); err != nil {
				t.Fatalf("envelope 解析失败: %v (%s)", err, script)
			}
			if env.Type == typ {
				return env
			}
		case <-deadline:
			t.Fatalf("等待 %s envelope 超时", typ)
			return evalEnvelope{}
		}
	}
}

// TestCompatEmitCarriesInstanceID：compat 兼发的旧协议事件 payload 须带 instance_id
// （61-消息一览 §0 实例字段必带）——server 事件 session-receive(type=text) → 桥转发
// llm-receive + 兼发 llm-token，兼发事件注入当前实例 id。
func TestCompatEmitCarriesInstanceID(t *testing.T) {
	evals := make(chan string, 32)
	br, bus := newTestBridge(t, evalCollect(evals))
	defer bus.Close()
	if err := br.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pubFire(bus, "session-receive", []byte(`{"session":"s1","turn":"t1","type":"text","payload":{"text":"hi"}}`))

	env := recvEnvelopeTyp(t, evals, "llm-token", time.Second)
	var p map[string]any
	if err := json.Unmarshal([]byte(env.Payload), &p); err != nil {
		t.Fatalf("llm-token payload 解析失败: %v (%s)", err, env.Payload)
	}
	if p["instance_id"] != "inst-test" {
		t.Fatalf("兼发 llm-token 缺 instance_id: %s", env.Payload)
	}
}

// TestCompatServerStartingEmitsLlmStarted（I-33）：server-starting（插件全部加载完成 =
// 服务就绪）→ 兼容层兼发 llm-started（服务就绪 ack），payload {session_id, turn_id, sub, notify}
// + 兼容层注入 instance_id。
func TestCompatServerStartingEmitsLlmStarted(t *testing.T) {
	evals := make(chan string, 32)
	br, bus := newTestBridge(t, evalCollect(evals))
	defer bus.Close()
	if err := br.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pubFire(bus, "server-starting", []byte(`{"started_at":123}`))

	env := recvEnvelopeTyp(t, evals, "llm-started", time.Second)
	var p map[string]any
	if err := json.Unmarshal([]byte(env.Payload), &p); err != nil {
		t.Fatalf("llm-started payload 解析失败: %v (%s)", err, env.Payload)
	}
	if p["instance_id"] != "inst-test" {
		t.Fatalf("兼发 llm-started 缺 instance_id: %s", env.Payload)
	}
	if _, ok := p["session_id"]; !ok {
		t.Fatalf("llm-started 缺 session_id 键: %s", env.Payload)
	}
	if _, ok := p["turn_id"]; !ok {
		t.Fatalf("llm-started 缺 turn_id 键: %s", env.Payload)
	}
	if p["sub"] != false || p["notify"] != false {
		t.Fatalf("llm-started sub/notify 应为 false: %s", env.Payload)
	}
}

// TestCompatLlmStartReplyNoLongerEmitsLlmStarted（I-33 死分支删除回归）：旧 llm-start.reply
// 无发布方（llm-start 走 promise result），兼容层不得再据此兼发 llm-started。
func TestCompatLlmStartReplyNoLongerEmitsLlmStarted(t *testing.T) {
	evals := make(chan string, 32)
	br, bus := newTestBridge(t, evalCollect(evals))
	defer bus.Close()
	if err := br.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pubFire(bus, "llm-start.reply", []byte(`{"ok":true,"session":"s1","turn":"t1"}`))
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case script := <-evals:
			var env evalEnvelope
			body := strings.TrimSuffix(strings.TrimPrefix(script, "window.mq.emitRemote("), ")")
			_ = json.Unmarshal([]byte(body), &env)
			if env.Type == "llm-started" {
				t.Fatalf("llm-start.reply 不应再兼发 llm-started: %s", script)
			}
		case <-deadline:
			return
		}
	}
}

// TestUploadURLContainsInstanceID：gui.upload 返回的 url 须带 ?instance_id=
// （与前端 getFileUrl 口径一致；61-消息一览 §0）。
func TestUploadURLContainsInstanceID(t *testing.T) {
	br, bus := newTestBridge(t, func(string) {})
	defer bus.Close()
	data := base64.StdEncoding.EncodeToString([]byte("hello"))
	res, errs := br.PublishEvent("gui.upload", `{"name":"a.txt","data":"`+data+`","kind":"file"}`)
	if len(errs) != 0 {
		t.Fatalf("gui.upload errs 应为空: %v", errs)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("gui.upload 结果类型异常: %T", res)
	}
	u, _ := m["url"].(string)
	if !strings.Contains(u, "?instance_id=inst-test") {
		t.Fatalf("上传 url 未带 instance_id: %q", u)
	}
}

// TestToolNotifyPluginFailureForwarded（2026-09-20，B 批"插件失败用户可见"）：
// 宿主投递的插件失败提示（tool-notify, notice=plugin-failure）经桥**原样转前端**
// （topic 名不在 mqTypeMap → 原名直通；前端 ChatPanel 既有 tool-notify 订阅即以轻提示
// 展示 message、MessageList 仅对 completion/recover 落气泡 → 可见但不打扰、不刷屏）。
func TestToolNotifyPluginFailureForwarded(t *testing.T) {
	evals := make(chan string, 16)
	br, bus := newTestBridge(t, evalCollect(evals))
	defer bus.Close()
	if err := br.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	const msg = "⚠️ 记忆沉淀失败：开发规范：应答超时（本轮对话未受影响；详情见日志文件）"
	pubFire(bus, "tool-notify", []byte(`{"instance_id":"inst-test","session_id":"s1","turn_id":"t1",`+
		`"notice":"plugin-failure","plugin":"memory","kind":"save","reason":"开发规范：应答超时",`+
		`"message":"`+msg+`","message_id":"msg-pnotice-1"}`))

	env := recvEnvelopeTyp(t, evals, "tool-notify", time.Second)
	var p map[string]any
	if err := json.Unmarshal([]byte(env.Payload), &p); err != nil {
		t.Fatalf("tool-notify payload 解析失败: %v (%s)", err, env.Payload)
	}
	if p["notice"] != "plugin-failure" || p["plugin"] != "memory" || p["kind"] != "save" {
		t.Fatalf("插件失败提示字段丢失：%s", env.Payload)
	}
	if p["message"] != msg {
		t.Fatalf("提示文案未原样到达前端：%q", p["message"])
	}
	if p["session_id"] != "s1" || p["message_id"] != "msg-pnotice-1" {
		t.Fatalf("归属/幂等字段丢失：%s", env.Payload)
	}
}
