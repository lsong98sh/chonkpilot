// gui.* 本地面（61-消息一览 §1）：处理方 = gui 桥本地；查询类 = publish + Promise 收集
// （POST /publish 响应承载 result/errors），本地事件无返回。
//
// 分派入口：PublishEvent 中 typ 前缀 "gui." → guiDo(动作串, payload)。gui.window.status
// 由 main 特判（applyWindowCommand），不进入本文件（若到达按 unsupported/空处理）。
//
// 多数 action 直接复用原 /call 内部实现（callX，签名统一
// func(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error)，
// 位于 local.go / upload.go / capture.go / optimize.go）——这里把消息 payload 还原成
// 原 /call 参数组后调用，并把 raw JSON 转成返回结构。
// 2026-09-04：原 dataCalls 注册表随 /call 清零，callX 降级为 guimsg 消息面内部实现。
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/chonkpilot/chonkpilot-gui/internal/messages"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// guiCallFunc 是 gui.* 消息面内部复用的 callX 处理器签名（原 dataCall 随 /call 链删除）。
type guiCallFunc = func(b *Bridge, ctx context.Context, params []json.RawMessage) ([]byte, error)

// guiDo 处理 gui.<action> 本地请求（前缀已由调用方剥掉），返回结果与错误。
// 写回风格对齐 PublishEvent 结果信封：成功返回结果对象；失败返回 {ok:false, error} 并附 errs。
func (b *Bridge) guiDo(action string, payload []byte) (result any, errs []error) {
	var p map[string]json.RawMessage
	_ = json.Unmarshal(payload, &p)

	// fail 构造失败回复。
	fail := func(format string, args ...any) (any, []error) {
		msg := fmt.Sprintf(format, args...)
		return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: msg}, []error{fmt.Errorf("%s", msg)}
	}
	// strArg 取 payload 字段的字符串值并编成 /call 字符串参数（缺省 ""）。
	strArg := func(key string) json.RawMessage {
		var s string
		if raw, ok := p[key]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &s)
		}
		buf, _ := json.Marshal(s)
		return json.RawMessage(buf)
	}
	// callMap 调用 callX（params 即原 /call 参数组）并把 raw JSON 结果解析为对象。
	callMap := func(h guiCallFunc, args ...json.RawMessage) (map[string]any, error) {
		raw, err := h(b, context.Background(), args)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &m)
		}
		if m == nil {
			m = map[string]any{}
		}
		return m, nil
	}

	switch action {
	// init-data：启动初始化数据（桥本地单点回答；应答键 = 实际下发形态（camelCase）：
	// treeData / expandedKeys / selectedKey / workDir / filetreeWidth / layout / ui /
	// openedFile / openedFiles，另可选 logDir / wizard_required；前端直接读取该对象）。
	case "init-data":
		m, err := callMap(callLoadInitData)
		if err != nil {
			return fail("init-data: %v", err)
		}
		// 场景向导启动检测（2026-10-04；设计 01 §1 / 05 §3 方案 A 兜底 + 触发）：
		// workdir 下工程规格文件缺失 → 需初始化：下发 agent-wizard 事件（前端据此打开向导），
		// 并在应答中补 add-only 字段 wizard_required（无事件通道时也可兜底）。
		specPath := filepath.ToSlash(filepath.Join(b.workDir, ".chonkpilot", "project_spec.md"))
		wizardRequired := true
		if _, statErr := os.Stat(filepath.Join(b.workDir, ".chonkpilot", "project_spec.md")); statErr == nil {
			wizardRequired = false
		}
		m[msgkeys.GuiInitDataResultWizardRequired] = wizardRequired
		if wizardRequired {
			b.EmitFrontend(messages.MsgAgentWizard, jsonEnvelope(map[string]any{
				msgkeys.AgentWizardEventReason:   "missing_spec",
				msgkeys.AgentWizardEventWorkDir:  b.workDir,
				msgkeys.AgentWizardEventSpecPath: specPath,
			}))
		}
		return m, nil

	// ui.save：按 payload 中存在的字段逐项保存（合并原 layout.save/window.save/
	// filetree.save/opened.save/ui.save；打开文件走 opened_files 复数形态）。
	case "ui.save":
		var errList []error
		save := func(h guiCallFunc, v json.RawMessage) {
			if len(v) == 0 || string(v) == "null" {
				return
			}
			if _, err := h(b, context.Background(), []json.RawMessage{v}); err != nil {
				errList = append(errList, err)
			}
		}
		if v, ok := p[msgkeys.GuiUiSavePayloadLayout]; ok {
			save(callSaveLayoutState, v)
		}
		if v, ok := p[msgkeys.GuiUiSavePayloadWindow]; ok {
			save(callSaveWindowState, v)
		}
		if v, ok := p[msgkeys.GuiUiSavePayloadFiletree]; ok {
			save(callSaveFileTreeState, v)
		}
		if v, ok := p[msgkeys.GuiUiSavePayloadOpenedFiles]; ok {
			save(callSaveOpenedFiles, v)
		}
		if v, ok := p[msgkeys.GuiUiSavePayloadUi]; ok {
			save(callSaveUIState, v)
		}
		if len(errList) > 0 {
			return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: errList[0].Error()}, errList
		}
		return map[string]any{msgkeys.FieldOk: true}, nil

	// recent.list：最近项目目录（usr config recent_dirs）→ 原结果 {dirs}。
	case "recent.list":
		m, err := callMap(callGetRecentDirs)
		if err != nil {
			return fail("recent.list: %v", err)
		}
		return m, nil

	// recent.remove：从「最近项目」记录中移除一条（payload {path}）→ {ok}。
	// **仅删记录** —— 不删除对应项目目录 / 数据资产，不关闭当前窗口；不存在 → 幂等成功。
	case "recent.remove":
		if _, err := callMap(callRemoveRecentDir, strArg(msgkeys.FieldPath)); err != nil {
			return fail("recent.remove: %v", err)
		}
		return map[string]any{msgkeys.FieldOk: true}, nil

	// dir.open-dialog：系统目录选择框（**纯选择、无副作用**）→ {path}（取消 {path:null}）。
	// 「以新窗口打开」是另一条动作 dir.open；两处语义分离（信任目录 / 登录选工作目录等只取路径）。
	case "dir.open-dialog":
		m, err := callMap(callOpenDirDialog)
		if err != nil {
			return fail("dir.open-dialog: %v", err)
		}
		return m, nil

	// pick-executable：系统文件选择框挑可执行文件（纯 picker 无副作用）→ {path}（取消 {path:""}）。
	case "pick-executable":
		m, err := callMap(callPickExecutable)
		if err != nil {
			return fail("pick-executable: %v", err)
		}
		return m, nil

	// file.save：配置快照 JSON 落盘（批 3 · ⑯）—— payload {name, content, mode?}；单对象参。
	// mode=backup → 直接落 <prjusr 数据根>/backup/<name>（自动备份）；缺省/「dialog」→ 系统「另存为」。
	// 返回 {path}（取消 = ""）。**不记录 content**（可能含 API Key）。
	case "file.save":
		if len(payload) == 0 || string(payload) == "null" {
			return fail("file.save: body required")
		}
		m, err := callMap(callSaveConfigFile, json.RawMessage(payload))
		if err != nil {
			return fail("file.save: %v", err)
		}
		return m, nil

	// toolchain.detect：探测 6 个工具链 + Chrome 的路径与版本（系统级候选，不落库）
	// → {tools:[{id,name,path,version}]}。
	case "toolchain.detect":
		m, err := callMap(callDetectToolchains)
		if err != nil {
			return fail("toolchain.detect: %v", err)
		}
		return m, nil

	// prompt-vars：提示词「变量插入」分组目录（只读，单源 = 后端常量）
	// → {groups:[{id,label,items:[{key,desc,dslOnly}]}]}（OP-12；见 promptvars.go）。
	case "prompt-vars":
		m, err := callMap(callPromptVars)
		if err != nil {
			return fail("prompt-vars: %v", err)
		}
		return m, nil

	// system.builtins：系统级只读 MCP 内置项（exe 同目录 config.json；OEM 资源）
	// → {mcpServers:[...]}。
	case "system.builtins":
		m, err := callMap(callSystemBuiltins)
		if err != nil {
			return fail("system.builtins: %v", err)
		}
		return m, nil

	// dir.open：以新进程打开目录。
	case "dir.open":
		if _, err := callMap(callOpenDir, strArg(msgkeys.FieldPath)); err != nil {
			return fail("dir.open: %v", err)
		}
		return map[string]any{msgkeys.FieldOk: true}, nil

	// console.open：系统控制台打开到路径所在目录。
	case "console.open":
		if _, err := callMap(callOpenInConsole, strArg(msgkeys.FieldPath)); err != nil {
			return fail("console.open: %v", err)
		}
		return map[string]any{msgkeys.FieldOk: true}, nil

	// vcs.info：探测工作目录 VCS 类型（callGetVCSInfo 以桥 workDir 为准，无参调用）
	// → {git, svn, gitInstalled}（gitInstalled = 系统可执行 git，供文件历史可用性区分文案）。
	case "vcs.info":
		m, err := callMap(callGetVCSInfo)
		if err != nil {
			return fail("vcs.info: %v", err)
		}
		return m, nil

	// reveal：Windows 资源管理器定位文件/目录（fire-and-forget；OS 能力，桥无旧实现）。
	case "reveal":
		path := svalStr(p, msgkeys.FieldPath)
		if path == "" {
			return fail("reveal: path required")
		}
		cmd := exec.Command("explorer", "/select,", path)
		if err := cmd.Start(); err != nil {
			return fail("reveal: %v", err)
		}
		_ = cmd.Process.Release()
		return map[string]any{msgkeys.FieldOk: true}, nil

	// open-with：Windows「打开方式」对话框（OS 能力，桥无旧实现）。
	case "open-with":
		path := svalStr(p, msgkeys.FieldPath)
		if path == "" {
			return fail("open-with: path required")
		}
		cmd := exec.Command("rundll32", "shell32.dll,OpenAs_RunDLL", path)
		if err := cmd.Start(); err != nil {
			return fail("open-with: %v", err)
		}
		_ = cmd.Process.Release()
		return map[string]any{msgkeys.FieldOk: true}, nil

	// search：项目内检索（复用 SearchProjectFiles 路径匹配占位实现）→ {results}。
	case "search":
		raw, err := callSearchProjectFiles(b, context.Background(), []json.RawMessage{strArg(msgkeys.FieldQuery)})
		if err != nil {
			return fail("search: %v", err)
		}
		var results []any
		_ = json.Unmarshal(raw, &results)
		if results == nil {
			results = []any{}
		}
		return map[string]any{msgkeys.FieldResults: results}, nil

	// capture：窗口隐藏 + 全屏截图 → 原结果 {file_id, name, path, url, size, b64}。
	case "capture":
		m, err := callMap(callCaptureScreen)
		if err != nil {
			return fail("capture: %v", err)
		}
		return m, nil

	// upload：附件/图片上传（payload {name, data, kind} → callUploadAttachment 单对象参）→ 原结果 {url,...}。
	case "upload":
		if len(payload) == 0 || string(payload) == "null" {
			return fail("upload: body required")
		}
		m, err := callMap(callUploadAttachment, json.RawMessage(payload))
		if err != nil {
			return fail("upload: %v", err)
		}
		return m, nil

	// prompt-optimise：AI 优化提示词（payload {title, useCase, prompt} → callOptimizeAgentPrompt
	// 单对象参，对齐原 call('OptimizeAgentPrompt', data)）→ 立即返回 {ok, started}；
	// 流式结果（optimize-token/optimize-done/optimize-error）由实现 EmitFrontend 推送，不在此返回。
	// 注：gui action 后缀 "prompt-optimise" 与 server 契约主题同名（值同）→ 按 50 §8.8
	// 以 msgkeys 常量引用（避免契约主题字面量；语义 = gui 动作后缀，非 server 主题）。
	case msgkeys.TopicPromptOptimise:
		if len(payload) == 0 || string(payload) == "null" {
			return fail("prompt-optimise: body required")
		}
		m, err := callMap(callOptimizeAgentPrompt, json.RawMessage(payload))
		if err != nil {
			return fail("prompt-optimise: %v", err)
		}
		return m, nil

	// devtools.open：宿主程序化打开 DevTools（61 §1；B3）。DevTools 的**用户入口**
	// （F12 / Ctrl+Shift+I 快捷键、右键菜单 Inspect）已在宿主层屏蔽，本动作 = 唯一入口。
	// **无返回**（61 §1 该主题「返回 = 无（本地事件）」）；宿主不支持 → 明确失败，不静默。
	case "devtools.open":
		if b.openDevTools == nil {
			return fail("devtools.open: not supported by host")
		}
		b.openDevTools()
		return nil, nil

	// window.status：不应到达（main 已特判）。防御性空返回。
	case "window.status":
		return nil, nil

	default:
		return map[string]any{msgkeys.FieldOk: false, msgkeys.FieldError: "unsupported gui action: " + action}, nil
	}
}

// svalStr 取 payload 顶层字符串字段（缺省 ""）。
func svalStr(p map[string]json.RawMessage, key string) string {
	raw, ok := p[key]
	if !ok || len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
