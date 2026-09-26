// Package bridge 实现 GUI 与 server 生态之间的桥（20-gui / 61-消息一览 §9）。
//
// 职责（薄桥，不做协议翻译；2026-09-04 A6 收敛态；2026-09-21 阶段 4 第二批增 config 门面）：
//   - instance 注册：启动自生成 uuid → instance-register{instance_id, work_dir, data_dir}
//     （相对主题；persist/server 经 instance-* 消息自持实例视图，桥不再直连 prj/usr 库）
//   - 事件透传：POST /publish（前端消息）→ 总线对应服务；总线事件 → 前端 emitRemote
//   - 分派：gui.* 本地面（guimsg.go）→ **config 类 data-<domain>-*（prj-config / prompt /
//     prj-security / user-config）走 data 门面**（data.go: dataViaFacade，装配处注入
//     inline 绑定；不再"再发一条 MQ 给 persist"，见 41 G-34）→ 其余 data-* 经总线 persist
//     （dataViaPersist）→ filesys.* 经总线 chonkpilot-filesys 服务 → 其余点分/单字方法
//     主题经 publishV 直通（server/gateway 写回 Result/Errors）
//   - 心跳/退出：instance-exit（GUI 退出注销）
//
// 主题（2026-09-03 域化）：业务一律写**相对主题**（session-* / task-* / instance-* /
// data-* / filesys.* / gui.* 等），命名空间前缀 chonk. 由宿主在 mq 初始化（Options.Prefix）
// 注入一次，本包不出现 chonk. 字面。
// 桥收到总线事件后按显式映射输出**稳定前端 type**（llm-receive/llm-complete/ask-user/
// tasks.started 等字符串不变）；data-*/filesys.* 等点分相对主题原名直通（不裁剪）。
//
// 文件变更跟踪（旧 dirWatcher 轮询）已随 filesys 组件化移除：前端文件变化走
// chonkpilot-filesys 服务（fsnotify → filesys.changed 广播 → 桥 > 订阅转发前端）。
package bridge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// Eval 是后端 → 前端脚本执行器（WebView2 w.Eval）。
type Eval func(script string)

// Bridge 是 GUI 与 server 生态之间的桥。
type Bridge struct {
	bus        mq.Bus
	instanceID string
	workDir    string
	dataDir    string
	eval       Eval
	// hwnd 是宿主窗口句柄（main 注入，截图 callCaptureScreen 等本地能力使用）。
	hwnd uintptr
	// logDir 是 GUI 文件日志目录（main 挂上文件 sink 后注入；随 init-data 只读下发，供前端/用户定位。
	// 空 = 未挂文件 sink，init-data 字段缺省不出现，等价旧行为）。
	logDir string
	// prjUsrRoot 是 prjusr 数据根目录（个人运行态：附件/截图等非库文件的落盘根）。
	// main 在启动期按 12-数据层 §3 解析后注入（`SetPrjUsrRoot`）：data_dir 空 →
	// ~/.chonkpilot/data/<prj-id>（B 方案，[24 §3.2] MW-8）；显式 data_dir → 与 prj 同根。
	// 空 = 未注入 → 回落 <workDir>/.chonkpilot（兼容旧行为，见 uploadRoot）。
	prjUsrRoot string
	// openDevTools 由 main 注入（宿主程序化打开 DevTools；gui.devtools.open 用）。
	// nil = 未接线（宿主不支持该能力）→ gui.devtools.open 明确失败，不静默。
	openDevTools func()
	// published 记录本实例近期发布的主题（src 防环：桥订阅了 ">"（相对全通配），
	// 本实例 /publish 的事件会被自己转发回前端造成二次 dispatch，需跳过）。
	publishedMu sync.Mutex
	published   map[string]time.Time

	// eventWaiters 供测试脚本阻塞等待特定事件（--test-port /wait-event 端点）。
	eventWaitersMu sync.Mutex
	eventWaiters   map[string][]chan<- map[string]any

	// ── 认证域（61 §4.6；阶段 2b-1/2b-2）─────────────────
	// authToken 是桥**持有**的免登录令牌（desktop 形态；由 login-in/login-register 应答
	// 取走并落 `~/chonkpilot/remember-token`）——**不下发前端**，仅随上行请求注入（injectAuth）。
	// authCheck 是宿主注入的**令牌有效性判定**（= llm server 的 Authenticated），供首屏注入
	// `authed`（61 §4.6：读凭证判定）；两者都由 authMu 守护。
	authMu    sync.Mutex
	authToken string
	authCheck func(token string) bool

	// cfg 是 data 门面（阶段 4 第二/三/四批，41 G-34 / G-35 / G-36）：宿主注入（inline 绑定，
	// 同进程直调）后，**data 面各域**（config 类 · session 域 · tasktree / knowledge / filelist /
	// scenario / memory）data-<domain>-* 由桥**识别后走门面**做数据操作，不再"再发一条 MQ 给
	// persist"（data.go: dataViaFacade）。**前端消息面一字不变**（主题名 / payload / 应答键名与
	// MQ 路径逐字一致）；nil = 未接线（如 -no-server 薄客户端/分离形态）→ 回落总线转发
	// （dataViaPersist），行为同改前。
	cfg facade.API
}

// New 创建桥（inprocess 内存总线；由 main 传入共享 mq.Bus）。
func New(instanceID, workDir, dataDir string, eval Eval, bus mq.Bus) *Bridge {
	return &Bridge{
		bus:          bus,
		instanceID:   instanceID,
		workDir:      workDir,
		dataDir:      dataDir,
		eval:         eval,
		published:    make(map[string]time.Time),
		eventWaiters: make(map[string][]chan<- map[string]any),
	}
}

// InstanceID 返回实例 ID（main 读取 prj DB 恢复窗口状态用）。
func (b *Bridge) InstanceID() string { return b.instanceID }

// PrjConfigLoad 读 prj-config 域键（main 启动恢复窗口状态等用；经总线 persist 键值面，
// 与 gui.* 状态保存同源）。
func (b *Bridge) PrjConfigLoad(key string) string { return b.prjConfigLoad(key) }

// PrjConfigList 返回 prj-config 平铺 map（含 prjusr 层覆盖，见 persist 分层路由）；
// 供宿主启动期恢复窗口几何等（一次总线往返取全部键）。
func (b *Bridge) PrjConfigList() map[string]any { return b.prjConfigList() }

// WorkDir 返回工作目录。
func (b *Bridge) WorkDir() string { return b.workDir }

// SetHWND 由 main 在窗口创建后注入（截图 callCaptureScreen 等本地能力使用 b.hwnd）。
func (b *Bridge) SetHWND(h uintptr) { b.hwnd = h }

// SetLogDir 由 main 在挂上文件日志 sink 后注入（GUI 日志目录；随 init-data 只读下发）。
func (b *Bridge) SetLogDir(dir string) { b.logDir = dir }

// SetPrjUsrRoot 由 main 在启动期注入 prjusr 数据根目录（12-数据层 §3；[24 §3.2] MW-8）：
// 附件/截图等个人运行态非库文件按它落盘（见 uploadRoot）。空 = 未注入（回落旧行为）。
func (b *Bridge) SetPrjUsrRoot(dir string) { b.prjUsrRoot = dir }

// uploadRoot 返回附件/截图落盘根：注入的 prjusr 数据根优先；未注入 → 回落 <workDir>/.chonkpilot
// （= prj 数据根，旧行为；-no-server 薄客户端/分离形态宿主不解析 prjusr 根时仍可用）。
func (b *Bridge) uploadRoot() string {
	if b.prjUsrRoot != "" {
		return b.prjUsrRoot
	}
	return filepath.Join(b.workDir, ".chonkpilot")
}

// SetFacade 由宿主注入 data 门面绑定（阶段 4 第二/三批，41 G-34；装配处 = `src/gui/main.go`：
// 内嵌 server 形态注入 inline 绑定，同进程直调）。
// 注入后 **config 类 + session 域**（data-session-*）data-* 由桥走门面（dataViaFacade）；
// 未注入（nil，如 -no-server 薄客户端）→ 保留既有总线转发路径（行为不变）。
func (b *Bridge) SetFacade(cfg facade.API) { b.cfg = cfg }

// SetDevToolsOpener 由 main 注入「程序化打开 DevTools」能力（gui.devtools.open）。
// 与「用户能否用快捷键/右键菜单打开 DevTools」的宿主开关解耦（B3：用户入口屏蔽后，
// 仍可经状态栏调试图标 → 本回调打开）。
func (b *Bridge) SetDevToolsOpener(fn func()) { b.openDevTools = fn }

// markPublished 记录一次本实例发布（发布前调用）。
func (b *Bridge) markPublished(subject string) {
	b.publishedMu.Lock()
	defer b.publishedMu.Unlock()
	b.published[subject] = time.Now()
}

// isSelfPublished 检查 subject 是否为本实例刚发布（1s 窗口，一次性消耗）。
// 命中 → 删除并返回 true，forwardEvent 跳过该回环消息。
func (b *Bridge) isSelfPublished(subject string) bool {
	b.publishedMu.Lock()
	defer b.publishedMu.Unlock()
	t, ok := b.published[subject]
	if !ok {
		return false
	}
	delete(b.published, subject)
	return time.Since(t) < time.Second
}

// 事件信封（对齐前端 mq.js emitRemote：payload 为 JSON 字符串）。
type envelope struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Source  string `json:"src"`
}

// Start 注册 instance（persist/server 经 instance-register 绑定数据根）并订阅总线事件
// 转发前端。数据/文件服务由 main 以同一总线先行启动（server 内嵌 persist + gateway、
// chonkpilot-filesys 服务），桥本身不再直连存储。
func (b *Bridge) Start() error {
	// 恢复免登录令牌（61 §4.6；阶段 2b-1）：`~/chonkpilot/remember-token` 存在则持有入内存，
	// 随后续上行请求注入（自证型 → 服务端重启后仍可解析）。
	b.loadRememberToken()
	// instance 注册：无 req_id、无 reply（客户端自生成 uuid；相对主题 instance-register）。
	b.publish("instance-register", map[string]interface{}{
		"instance_id": b.instanceID,
		"work_dir":    b.workDir,
		"data_dir":    b.dataDir,
		"client_type": "gui",
	})
	// 订阅总线生态事件 → 前端（相对主题全通配 ">"：总线补 chonk. 前缀后命中全部
	// 业务事件；handler 收到去前缀后的相对主题，见 mq Options.Prefix 语义）。
	if _, err := b.bus.On(">", 0, func(_ context.Context, subj string, v *mq.Value) error {
		b.forwardEvent(subj, v.Payload)
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// forwardEvent 把 mq 主题事件转换为前端 type 并转发（2026-09-03 域化后按显式映射输出
// **稳定前端字符串**：session-receive → llm-receive、task-started → tasks.started 等，
// 见 mqTypeMap；未映射主题原名直通——data-*/filesys.* 等点分/连字符相对主题本就不裁剪）。
// 本实例刚发布的事件（src 防环）跳过，避免前端二次 dispatch。
//
// 2026-09-19（实例隔离第二批，缺口 8）：转发前按 **instance 归属过滤** —— 事件载荷带
// 非空 `instance_id` 且不等于本实例 → 丢弃（split/browser 下同进程多 instance 时 A 的
// `tasks.*` / `mcp-*` 事件不投给 B 的前端）。载荷无 `instance_id`（data-*/filesys.* 等
// 按 workdir 管理的事件、server 级广播）或本实例未标识（instanceID 空）→ 放行（单 instance
// 行为与引入过滤前逐字节等价，过滤条件恒真）。
// 转发后按旧 chonkpilot 协议事件面兼发兼容事件（compat.go：server 定稿协议不变，gui 对齐旧生态）。
func (b *Bridge) forwardEvent(subject string, payload []byte) {
	if b.isSelfPublished(subject) {
		return
	}
	if !b.acceptEventInstance(eventInstanceID(payload)) {
		return
	}
	typ := b.eventType(subject)
	b.evalEmit(typ, string(payload), "mq")
	b.compatEmit(typ, payload)
	// 通知等待该事件的测试 waiter（非阻塞）
	b.notifyEventWaiters(typ, payload)
}

// acceptEventInstance 判定事件归属是否属本实例（缺口 8）：载荷 instance 为空（无归属字段）
// 或本实例未标识（桥无 instanceID）→ 放行（兼容旧行为）；否则要求**完全相等**。
func (b *Bridge) acceptEventInstance(evInstance string) bool {
	if b.instanceID == "" || evInstance == "" {
		return true
	}
	return evInstance == b.instanceID
}

// eventInstanceID 从事件 payload 提取 `instance_id`（无字段 / 空串 / 非 JSON 对象 → ""，即无归属）。
func eventInstanceID(payload []byte) string {
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ""
	}
	return probe.InstanceID
}

// WaitForEvent 阻塞等待指定 type 的事件到达（超时 = ctx deadline / 30s max）。
// 返回事件 payload 的 map 解析结果。用于 --test-port /wait-event 端点。
func (b *Bridge) WaitForEvent(ctx context.Context, typ string) (map[string]any, error) {
	ch := make(chan map[string]any, 1)
	b.eventWaitersMu.Lock()
	b.eventWaiters[typ] = append(b.eventWaiters[typ], ch)
	b.eventWaitersMu.Unlock()
	select {
	case ev := <-ch:
		return ev, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notifyEventWaiters 通知所有等待该事件类型的 waiter（非阻塞，chan 满则跳过）。
func (b *Bridge) notifyEventWaiters(typ string, payload []byte) {
	var ev map[string]any
	_ = json.Unmarshal(payload, &ev)
	b.eventWaitersMu.Lock()
	waiters := b.eventWaiters[typ]
	delete(b.eventWaiters, typ)
	b.eventWaitersMu.Unlock()
	for _, ch := range waiters {
		select {
		case ch <- ev:
		default:
		}
	}
}

// mqTypeMap 相对主题 → 前端 type（保持域化前的 UI 事件字符串；键不含 chonk. 前缀；
// 未命中的相对主题原名直通）。
var mqTypeMap = map[string]string{
	// session 域
	"session-receive":    "llm-receive",
	"session-complete":   "llm-complete",
	"session-compress":   "llm-compress",
	"session-ask":        "ask-user",
	"session-turn-start": "turn-start",
	// task 域
	"task-started": "tasks.started",
	"task-updated": "tasks.updated",
	"task-done":    "tasks.done",
	// server 域
	"server-starting":       "server-starting",
	"server-status-changed": "servers.status_changed",
	// tool 域
	"tool-changed": "tools.list_changed",
	// prompt 域（事件）
	"prompt-optimised": "prompt-optimised",
	// instance 域（GUI 客户端实例消息，转发保持原字符串）
	"instance-register":  "instance-register",
	"instance-heartbeat": "instance-heartbeat",
	"instance-exit":      "instance-exit",
}

// eventType 从相对主题提取前端事件 type（显式映射优先；兜底**原名直通**：
// data-<domain>-<op>（含 refresh 广播）与 filesys.* 等相对主题本身就是稳定的前端
// type，不做"去前缀/取最后一段"裁剪）。
func (b *Bridge) eventType(subject string) string {
	if typ, ok := mqTypeMap[subject]; ok {
		return typ
	}
	return subject
}

func (b *Bridge) evalEmit(typ, payloadJSON, src string) {
	env, _ := json.Marshal(envelope{Type: typ, Payload: payloadJSON, Source: src})
	b.eval("window.mq.emitRemote(" + string(env) + ")")
}

// EmitFrontend 把事件广播给前端（main 等外部包调用；如窗口最大化状态同步）。
func (b *Bridge) EmitFrontend(typ, payloadJSON string) {
	b.evalEmit(typ, payloadJSON, "gui")
}

// PublishEvent 处理 POST /publish：前端 type → 对应处理面，返回结果信封
// （result/errors，61-消息一览 §9.1 Bridge=传输门面：上行 HTTP 响应承载处理结果）。
// 映射规则（2026-09-04 消息面收敛）：
//   - llm-start：拆两步（20-gui）——前端只发一个 llm-start 事件
//     （载荷含 session/turn/q/llm/think/effort/scenario_id，turn 前端自分配 uuid），
//     桥拆为 session-start{session,turn,llm} + session-send{type:text-user, content}（相对主题）
//   - gui.* 本地面（61-消息一览 §1）：桥本地处理（guiDo，guimsg.go）
//   - data-<domain>-<action> 消息面（20-gui / 61-消息一览 §3）：经总线 persist
//     数据服务应答（dataViaPersist 注入 instance_id + req_id）
//   - 点分相对主题（filesys.* 等）：直通总线服务（chonkpilot-filesys/gateway 写回
//     Result/Errors；filesys.watch/unwatch 单向无返回）
//   - 单字方法面 type（llm-send/llm-cancel/ask-user-reply/task-stop/tool-retry/
//     prompt-optimise…）→ frontMethodSubjects 相对域主题（注入 instance_id，
//     server 方法处理写回 Result/Errors）
//   - 本地事件（window-* 等）由调用方先行过滤，不进入本方法。
func (b *Bridge) PublishEvent(typ, payloadJSON string) (result any, errs []error) {
	if typ == "llm-start" {
		b.splitLLMStart(payloadJSON)
		return nil, nil
	}
	// gui.* 本地面（61-消息一览 §1）：桥本地实现（guiDo 内部复用 callX；gui.window.status
	// 由 main/testServer 特判，不进入本方法）。
	if strings.HasPrefix(typ, "gui.") {
		return b.guiDo(strings.TrimPrefix(typ, "gui."), []byte(payloadJSON))
	}
	// data-<domain>-<action> 消息面（20-gui / 61-消息一览 §3）：**config 类（prj-config /
	// prompt / prj-security / user-config）走 data 门面**（阶段 4 第二批，41 G-34：同进程
	// 直调，不再"再发一条 MQ 给 persist"）；其余域与未注入门面时经总线 persist 应答
	// （dataCall 内部分派；前端消息面一字不变）。
	if strings.HasPrefix(typ, "data-") {
		return b.dataCall(typ, []byte(payloadJSON))
	}
	// 点分相对主题（filesys.* 等）→ 直通总线服务（处理方写回 Value.Result/Errors）。
	if strings.Contains(typ, ".") {
		return b.publishV(typ, b.injectAuth(b.injectInstance(payloadJSON)))
	}
	// 认证域（61 §4.6；阶段 2b-1）：login-* 由**本入口承载令牌**（持有并注入 / 落文件），
	// 应答里的内部 token 字段在此取走并剥除 → **令牌不进前端 payload**（见 login.go）。
	if subject, ok := loginSubjects[typ]; ok {
		return b.loginEvent(subject, typ, payloadJSON)
	}
	// 单字方法面 type → 域主题（显式映射；未知名 = 无订阅方，静默丢弃近似旧行为）。
	subject, ok := frontMethodSubjects[typ]
	if !ok {
		return nil, nil
	}
	result, errs = b.publishV(subject, b.injectAuth(b.injectInstance(payloadJSON)))
	// T-31：客户端能力面三主题（tools-list/prompts-list/resources-list）返回**全量**
	// 条目（含所有 instance），桥侧按当前实例作用域过滤后再返回前端。
	b.filterCapabilityScope(typ, result)
	return result, errs
}

// frontMethodSubjects 前端单字方法 type → 相对域主题（61-消息一览 §4.5；chonk. 前缀由总线补）。
// 2026-09-04 C1：servers-list/reload 客户端面已移除（前端零调用），白名单不保留。
// 2026-09-11 T-31：新增客户端能力面主题 tools-list/prompts-list/resources-list
// → gateway 方法面 mcp-tools-list/mcp-prompts-list/mcp-resources-list（前端经桥直取
// 运行时能力面；桥自动注入 instance_id，gateway 应答写回 v.Result 后原样返回前端）。
//
// 注：认证域 `login-register` / `login-in` / `login-out` **不在此表** —— 需桥承载令牌
// （持有 + 落文件 + 注入）→ 走 `loginEvent` 专用分支（login.go，61 §4.6）。
var frontMethodSubjects = map[string]string{
	"llm-send":        "session-send",
	"llm-cancel":      "session-cancel",
	"ask-user-reply":  "session-ask-reply",
	"task-stop":       "task-stop",
	"task-background": "task-background",
	"tool-retry":      "tool-retry",
	"prompt-optimise": "prompt-optimise",
	// 实例消息（61 §4.1，阶段 2a）：instance-claim = 前端启动认领（请求-响应，桥按
	// publishV 把 server 写回的 v.Result 作为 /publish 响应回发起者 → 多客户端不串号）；
	// instance-heartbeat = **发布方改为前端 SPA**（客户端续期 30s；-tags split 门控语义不变：
	// 单体形态前端不发布、不判超时，见 61 §4.1 ②订正）。
	"instance-claim":     "instance-claim",
	"instance-heartbeat": "instance-heartbeat",
	"tools-list":         "mcp-tools-list",
	"prompts-list":       "mcp-prompts-list",
	"resources-list":     "mcp-resources-list",
	// 2026-09-13 统一异步模型：超时「等待完成」裁决（前端 type 与下行事件 mcp-tools-timeout 对称）。
	// 注意：gateway 方法面相对主题 = methodSubject("tools/wait") = **mcp-tools-wait**
	// （subjects.go:63-65 `mcp-` + `/`→`-`），**不是** `tools/wait` —— 写成错主题会静默丢弃。
	"mcp-tools-wait": "mcp-tools-wait",
	// 2026-09-18：超时裁决条「取消」不再直发 gateway 方法面——`mcp-tasks-cancel`（及
	// `mcp-tasks-status|result|list`、`mcp-tools-background`）方法面已移除；「停止」统一走
	// **task-stop**（见上表；服务端按 tool_call_id / task_id 归一后经层 → sink → gateway
	// 真打断）。本白名单不再保留已删主题（否则只会在总线上无订阅方、静默丢弃）。
}

// capabilityListKeys 客户端能力面 topic → 结果数组键（T-31 桥侧作用域过滤用）。
var capabilityListKeys = map[string]string{
	"tools-list":     "tools",
	"prompts-list":   "prompts",
	"resources-list": "resources",
}

// filterCapabilityScope 对客户端能力面（tools-list/prompts-list/resources-list）的返回结果
// 做**当前实例作用域**过滤：gateway 原样返回**全量**条目（含所有 instance），前端无
// instance_id 通道，故由桥侧按 b.instanceID 过滤——仅保留 scope == ""（全局，含缺 scope）
// 或 scope == 当前实例 id 的条目。仅当 result 为 map 且含对应数组时筛数组内容，
// 不新增/不删除 result 的键，避免形态变化。
func (b *Bridge) filterCapabilityScope(typ string, result any) {
	key, ok := capabilityListKeys[typ]
	if !ok {
		return
	}
	m, ok := result.(map[string]any)
	if !ok {
		return
	}
	arr, ok := m[key].([]any)
	if !ok {
		return
	}
	filtered := make([]any, 0, len(arr))
	for _, it := range arr {
		entry, ok := it.(map[string]any)
		if !ok {
			filtered = append(filtered, it) // 非对象条目原样保留（视为全局）
			continue
		}
		scope, _ := entry["scope"].(string)
		if scope == "" || scope == b.instanceID {
			filtered = append(filtered, it)
		}
	}
	m[key] = filtered
}

// splitLLMStart 把前端 llm-start 事件拆为 server 协议两步（21-llm-server，相对主题）：
//
//	session-start  {req_id, instance_id, session, turn, llm, think, effort, scenario_id, continue}
//	session-send   {instance_id, session, turn, type: "text-user", content}
//
// continue=true 为同轮次继续：复用既有 turn，桥不再自动分配 turn（turn 空时由 server
// 按 session 取最近一轮）。
func (b *Bridge) splitLLMStart(payloadJSON string) {
	var p struct {
		Session    string `json:"session"`
		SessionID  string `json:"session_id"`
		Turn       string `json:"turn"`
		TurnID     string `json:"turn_id"`
		LLM        string `json:"llm"`
		Think      string `json:"think"`
		Effort     string `json:"effort"`
		ScenarioID string `json:"scenario_id"`
		Continue   bool   `json:"continue"`
		Q          string `json:"q"`
		Content    string `json:"content"`
	}
	_ = json.Unmarshal([]byte(payloadJSON), &p)
	session := p.Session
	if session == "" {
		session = p.SessionID
	}
	turn := p.Turn
	if turn == "" {
		turn = p.TurnID
	}
	if session == "" {
		slog.Warn("bridge llm-start: session required", "payload", payloadJSON)
		return
	}
	// turn 缺省自动分配（旧脚本/外部注入不带 turn；server 协议 turn 必填）。
	// 同轮次继续（continue）例外：不分配新 turn，交 server 按 session 解析最近一轮复用。
	if turn == "" && !p.Continue {
		turn = "t-" + strings.ReplaceAll(newUUID(), "-", "")[:12]
	}
	content := p.Q
	if content == "" {
		content = p.Content
	}
	b.publish("session-start", map[string]interface{}{
		"req_id":      newUUID(),
		"instance_id": b.instanceID,
		"session":     session,
		"turn":        turn,
		"llm":         p.LLM,
		"think":       p.Think,
		"effort":      p.Effort,
		"scenario_id": p.ScenarioID,
		"continue":    p.Continue,
	})
	b.markPublished("session-start")
	b.publish("session-send", map[string]interface{}{
		"instance_id": b.instanceID,
		"session":     session,
		"turn":        turn,
		"type":        "text-user",
		"content":     content,
	})
	b.markPublished("session-send")
}

// injectInstance 给 server 方法面载荷补 instance_id（payload 为 JSON 对象时）。
// 兜底：前端 mq.emit 已统一注入 instance_id（纯前端事件 + 路由到后端；注入入口见
// frontend/src/utils/mq.js withInstanceId），此处为幂等兜底（已存在则不覆盖），
// 兼容未走前端注入的直接发布（如测试脚本、外部 IMPORT）。
func (b *Bridge) injectInstance(payloadJSON string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil {
		return payloadJSON
	}
	if _, ok := m["instance_id"]; !ok {
		m["instance_id"] = b.instanceID
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return payloadJSON
	}
	return string(raw)
}

func (b *Bridge) publish(subject string, payload interface{}) {
	var raw []byte
	switch v := payload.(type) {
	case string:
		raw = []byte(v)
	default:
		var err error
		raw, err = json.Marshal(v)
		if err != nil {
			slog.Error("bridge publish marshal failed", "subject", subject, "err", err)
			return
		}
	}
	v := b.bus.Emit(context.Background(), subject, raw).Wait()
	if v.Err() != nil {
		slog.Error("bridge publish failed", "subject", subject, "err", v.Err())
	}
}

// publishV 发布并等待派发结果（promise 语义）：返回订阅者写回的 Result 与收集的 Errors。
// 事件类主题（无订阅者写回）→ result=nil、errs=nil，行为与 publish 一致。
func (b *Bridge) publishV(subject string, payload interface{}) (result any, errs []error) {
	b.markPublished(subject)
	var raw []byte
	switch v := payload.(type) {
	case string:
		raw = []byte(v)
	default:
		var err error
		raw, err = json.Marshal(v)
		if err != nil {
			return nil, []error{err}
		}
	}
	v := b.bus.Emit(context.Background(), subject, raw).Wait()
	return v.Result, v.Errors
}

// CloseInstance 注销本实例（发 instance-exit；persist 移除实例绑定 + server 取消名下
// running turn / 释放锁；61-消息一览 §4.1），**不关闭总线**。
//
// 多窗口（24 §4.1）下每窗口一个桥、共享同一条**进程级**总线：任一路径关闭总线都会打断
// 其它窗口，故窗口关闭只注销本实例；总线由宿主进程收尾时统一 Close。
func (b *Bridge) CloseInstance() {
	b.publish("instance-exit", map[string]interface{}{"instance_id": b.instanceID})
}

// Close 注销实例并关闭总线（**进程收尾**用；单窗口历史语义不变）。合并单进程仍发
// instance-exit，保证 server 停机前清理。
func (b *Bridge) Close() {
	b.CloseInstance()
	if b.bus != nil {
		_ = b.bus.Close()
	}
}

// ── 本地事件（window-* 等，不进入 mq）──

var locals = make(map[string][]func())

// OnLocal 注册本地事件处理器（如窗口控制）。
func (b *Bridge) OnLocal(typ string, h func()) {
	locals[typ] = append(locals[typ], h)
}

// DispatchLocal 分发本地事件（main 处理 /publish 时先查）。
func (b *Bridge) DispatchLocal(typ string) bool {
	hs, ok := locals[typ]
	if !ok {
		return false
	}
	for _, h := range hs {
		h()
	}
	return true
}

// newUUID 生成 RFC 4122 v4 风格 UUID（不引入额外依赖）。
func newUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
