// server 域工具经 gateway 分组注册方法面注入（61-消息一览 §5.1.1，2026-09-06）：
// 域工具 = tools/register（相对主题 mcp-tools-register，payload {name, description, schema,
// handler_subject, owner, hot, category}）——取代旧 mcp.register/unregister 统一 kind 与
// server-register 主题。
//
// 25 §5/T2（2026-09-25）：agent（内置 agent 集）**不再**经 prompts/register asset_kind=agent
// 注册为资产（agent 只"注入"不"注册"），仅登记到内存表（见 registerDomainAgents /
// registeredAgentDef）。25 §8.1 #8（T6，2026-09-25）：内置 agent 集**不再是代码内嵌契约**，
// 统一为 **app 级场景**（`<exeDir>/scenarios/*/`，随发布只读资源），经数据层门面读取（规则单源）。
//
// 注册：Start 中 gateway 就绪后逐工具 await（同主题 promise）；执行命中时 gateway
// regProv 向注册声明的回调主题（SubjectDomainToolCall）发 {tool, args, context}，本模块
// 订阅者据此恢复 server turn 执行（写回 v.Result）。
//
// 执行路径：
//   - task 型（tool_stop/tool_result）：turn 经 gateway tools/call 唯一入口 → regProv 回调
//     （本文件）→ execTaskTool 同步执行并返回文本（节点建/广播/收尾在此）。
//   - ask_user / llm 型（llm_run）：异步交互（等用户回答 / 子会话 DSL），turn 内保留内部通道；
//     本回调提供兜底（外部经 gateway 直接调用时可执行，返回提交提示文本）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// registerDomainAgents 读 **app 级场景**（`<exeDir>/scenarios/<场景>/`，与 `<exeDir>/capability/`
// 平级、随发布只读）内的**全部 agent** 并登记到 s.agents：内存注册表供校验（lookupAgent）与子轮次
// 提示词回落（registeredAgentDef；`*.agent.md` 的 content = 该 agent 的提示词）。
//
// 来源单源 = **数据层门面**（`facade.ScenarioAPI.ScenarioList`）——本层不拼路径、不读文件，路径与
// 三级根规则留在数据层（capfs，25 §6）。
//
// 25 §5 / T2（2026-09-25）：agent **只"注入"不"注册"** —— 不经 gateway `prompts/register`
// （asset_kind=agent）注册资产，故本函数只做内存登记。
// 25 §8.1 #8（T6，2026-09-25）：原 `//go:embed contracts/agents/*.agent.md` 读法**已撤**。
//
// 命名与唯一性（2026-09-26）：agent **允许跨场景重名** → 注册键 = `<场景id>/<agent名>`
// （见 agentRegKey），故同名不再互相覆盖；**同一场景内**同名 agent 属异常数据 —— 数据层保存时
// 已拦截（`capfs.ValidateScenarioAgents`，42 §2 (175)），此处仅**兜底**保留先注册者并记 **Warn**
// 日志（**不 fatal**，避免影响启动）。
func (s *Server) registerDomainAgents(_ context.Context) error {
	if s.data == nil {
		return nil // 无数据层（裸构造） → 不登记（lookupAgent 走空注册表宽松语义）
	}
	res, err := s.data.ScenarioList(facade.ScenarioListRequest{})
	if err != nil {
		return fmt.Errorf("读 app 级场景: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range res.List {
		if sc.Level != appScenarioLevel {
			continue // global 级 = app 级场景（user/project 属 instance/项目面，见 resolveAgentDef）
		}
		scn := strings.TrimSpace(sc.ID)
		for _, a := range sc.Agents {
			name := strings.TrimSpace(a.Name)
			if name == "" {
				continue
			}
			key := agentRegKey(scn, name)
			if _, dup := s.agents[key]; dup {
				// Warn 级兜底：同一场景内同名 agent 本应被数据层保存校验拦截（capfs.ValidateScenarioAgents），
				// 走到这里说明是异常数据（如手工落盘）→ 保留先注册者，不 fatal（避免影响启动）。
				logf("[warn] [chonkpilot-server] app 级场景 %q 内 agent %q 重名，保留先注册者（不覆盖）；"+
					"数据层保存已拦截，此处为兜底\n", scn, name)
				continue
			}
			s.agents[key] = AgentDef{Name: name, Description: a.Description, Content: a.Prompt}
		}
	}
	return nil
}

// appScenarioLevel 是场景级别「app」（系统级、随发布只读）的取值（25 §6）。
// 门面（facade.Scenario.Level）为字符串域字段、不导出级别常量，故本层以本常量比对口径。
const appScenarioLevel = "app"

// agentRegKey 是 in-memory agent 注册表的键 = `<场景id>/<agent名>`（**场景前缀消歧**：
// agent 允许跨场景重名，键含场景 id 保证注册表内唯一、互不覆盖）。无场景 id（异常数据）→ 裸名。
func agentRegKey(scenarioID, name string) string {
	if scenarioID == "" {
		return name
	}
	return scenarioID + "/" + name
}

// splitAgentRef 拆分 agent 引用（`<场景id>/<agent名>` 或裸名）为 (场景id, agent名)。
func splitAgentRef(ref string) (scenarioID, name string) {
	if i := strings.Index(ref, "/"); i >= 0 {
		return strings.TrimSpace(ref[:i]), strings.TrimSpace(ref[i+1:])
	}
	return "", strings.TrimSpace(ref)
}

// uniqueAgentByName 在注册表中按 **agent 名（键末段）唯一**匹配：恰好 1 条 → 命中；0 条或 ≥2 条
// （跨场景重名，须用 `<场景id>/<agent名>` 精确引用）→ 未命中（不猜）。
func uniqueAgentByName(reg map[string]AgentDef, name string) (AgentDef, bool) {
	var hit AgentDef
	n := 0
	for k, d := range reg {
		if _, kn := splitAgentRef(k); kn == name {
			hit, n = d, n+1
		}
	}
	if n == 1 {
		return hit, true
	}
	return AgentDef{}, false
}

// lookupAgent 查 agent 名是否已注册（存在性校验；llm_run DSL 指令第一段委派对象标识）。
// 注册表为空（DisableMCP/无网关注册）时放行任意非空名——DSL 委派对象名由场景/脚本自由给定。
// 引用可带场景前缀（`<场景id>/<agent名>`，精确命中）；裸名 → 注册表内唯一同名才命中。
func (s *Server) lookupAgent(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.agents) == 0 {
		return true // 无注册表 = 不过滤（未注册网关时的宽松语义）
	}
	if _, ok := s.agents[ref]; ok {
		return true
	}
	scn, name := splitAgentRef(ref)
	if scn != "" || name == "" {
		return false // 带前缀未命中 → 不回落
	}
	_, ok := uniqueAgentByName(s.agents, name)
	return ok
}

// registeredAgentDef 取内置 agent 注册表中该**引用**的定义（AG-1：子轮次提示词回落的第二来源；
// 注册表 = app 级场景内的 agent，见 registerDomainAgents）：
//   - 引用含场景前缀（`<场景id>/<agent名>`）→ **精确**命中该场景内 agent；
//   - 裸名 → 精确键 / 注册表内**唯一**同名才命中（跨场景重名须用前缀引用）。
func (s *Server) registeredAgentDef(ref string) (AgentDef, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.agents[ref]; ok {
		return d, true
	}
	scn, name := splitAgentRef(ref)
	if scn != "" || name == "" {
		return AgentDef{}, false
	}
	return uniqueAgentByName(s.agents, name)
}

// registerDomainTools 把全部域工具经 gateway tools/register（mcp-tools-register）注入。
// schema 为契约 parameters 段 JSON 原文（对齐 regMsg.Schema 语义）；回调主题统一
// SubjectDomainToolCall；hot=true（对齐原"内嵌 server 整体 hot"，TestEmbeddedGatewayDomainToolsHot）。
// 逐工具 await（同主题 promise）：任一失败即中断返回。
func (s *Server) registerDomainTools(ctx context.Context) error {
	defs, err := loadDomainTools()
	if err != nil {
		return err
	}
	for _, d := range defs {
		body := map[string]any{
			"name":            d.Name,
			"description":     d.Description,
			"handler_subject": SubjectDomainToolCall,
			"owner":           "server",
			"hot":             true,
		}
		if d.Parameters != nil {
			if sb, err := json.Marshal(d.Parameters); err == nil {
				body["schema"] = string(sb)
			}
		}
		if _, err := s.emitGateway(ctx, "tools/register", body); err != nil {
			return err
		}
	}
	return nil
}

// domainToolMsg 是 domain-tool-call 回调载荷（gateway registered_provider.Call 构造）。
type domainToolMsg struct {
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
	Context *struct {
		Turn       string `json:"turn"`
		Session    string `json:"session"`
		InstanceID string `json:"instance_id"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"context"`
}

// onDomainToolCall 处理 gateway regProv 回调（域工具执行）：
// 从回调 context 恢复 server turn（对齐原 inprocess ctx 注入语义），结果写回 v.Result
// （map{content, isError}，regProv.toolResultFromValue 归一）。
func (s *Server) onDomainToolCall(_ context.Context, _ string, v *mq.Value) error {
	var msg domainToolMsg
	if err := json.Unmarshal(v.Payload, &msg); err != nil {
		return err
	}
	if msg.Tool == "" {
		return methodErr("tool is required")
	}
	tc, callID := (*turnCtx)(nil), ""
	if msg.Context != nil {
		tc = s.lookupTurnIn(msg.Context.InstanceID, msg.Context.Turn)
		callID = msg.Context.ToolCallID
	}
	text, isErr := s.domainExecute(tc, callID, msg.Tool, msg.Args)
	v.Result = map[string]any{
		"resultType": "complete",
		"content":    []any{map[string]any{"type": "text", "text": text}},
		"isError":    isErr,
	}
	return nil
}

// domainExecute 执行域工具并返回结果文本（回调统一路径）：
//   - ask_user → ask 通道（广播 ask-user → 等 ask-user-reply → 回填 done）；
//   - llm 型（llm_run）→ runSubJob goroutine（DSL/委派子会话完成回填父轮次）；
//   - task 型 → execTaskTool 同步执行（节点 done 广播在收尾）。
//
// isErr=true 表示工具级失败（无 turn 兜底 / 节点构建失败）。
func (s *Server) domainExecute(tc *turnCtx, callID, tool string, args map[string]any) (string, bool) {
	if tc == nil {
		return "域工具必须经 server turn 调用（turn 上下文缺失）", true
	}
	node, derr := s.domainNode(tc, callID, tool, args)
	if derr != nil {
		return derr.Error(), true
	}
	switch {
	case isAskTool(tool):
		// 异步交互：广播 ask-user 等待回答（结果经 ask-user-reply 回填 turn）
		s.ask(tc.req.Turn, callID, args, node.TaskID)
		return "已向用户提问，等待回答", false
	case isLLMTool(tool):
		// 异步子会话：开 goroutine 执行 llm_run DSL（结果回填父轮次）
		go s.runSubJob(tc, callID, node, args)
		return "已提交子会话执行，结果稍后回填", false
	default:
		// task 型：同步执行并返回文本（节点建/广播/收尾在 handler）
		text := s.execTaskTool(tc, callID, node, tool, args)
		if strings.HasPrefix(text, "错误") {
			s.tasks.done(node.TaskID, TaskStateError, "", text)
		} else {
			s.tasks.done(node.TaskID, TaskStateDone, text, "")
		}
		return text, false
	}
}

// lookupTurn 按 turn id 查会话状态机（s.mu 保护）。instance 维度缺省（全桶回退）——旧调用方/
// 测试用；已知实例归属时用 lookupTurnIn（缺口 5：不跨 instance 命中同 turn id 的轮次）。
func (s *Server) lookupTurn(turn string) *turnCtx {
	if turn == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupTurnLocked("", turn)
}

// lookupTurnIn 按 (instance, turn) 查会话状态机（s.mu 保护；缺口 5：隔离命中）。
func (s *Server) lookupTurnIn(instanceID, turn string) *turnCtx {
	if turn == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupTurnLocked(instanceID, turn)
}

// domainNode 构建并广播域工具任务节点（对齐 turn.go 建 node 逻辑）。
func (s *Server) domainNode(tc *turnCtx, callID, tool string, args map[string]any) (*TaskNode, error) {
	node := &TaskNode{
		Tool:       tool,
		ToolCallID: callID,
		TopSession: tc.req.Session,
		Kind:       taskKindOf(tool),
		SessionID:  tc.req.Session,
		TurnID:     tc.req.Turn,
		InstanceID: tc.req.InstanceID,
	}
	node.Name, node.Purpose, node.Simplified = taskDisplay(tool, args)
	node.args = args
	return s.tasks.start(node)
}

// methodErr 构造普通错误（mq 订阅者 error 返回 → 收集进 v.Errors）。
func methodErr(msg string) error { return &toolError{msg: msg} }

// ── agent 定义读取（AG-1 / AG-2，40-演进计划 §AG，2026-09-25）──────────
//
// 委派（llm_run 的 `LLM "agent" "prompt"`）与顶层轮次在**每轮开始处**读取 agent 定义
// （AG-C5 热生效：按 turn 生效，不缓存到进程级/包级变量）。定义来源两处，按序回退：
//  1. 场景内 agent（`data-scenario-load` 的 `agents` 项，字段对齐门面 DTO
//     facade.ScenarioAgent）—— 提示词 / 工具白名单 / LLM 引用 / 委派条件四字段齐备；
//  2. 内置 agent 内存注册表（**app 级场景** `<exeDir>/scenarios/*/` 内的 agent，见
//     registeredAgentDef）—— 仅提示词。
//     25 §5/T2：agent **不再**注册为 gateway 资产；25 §8.1 #8（T6）：来源 = app 级场景（非 embed）。
//
// 空值语义（AG-C4）：`prompt` 空 = 不加；`tools` 空 = 不限制；`delegateCond` 空 = 不附加；
// `llmRef` 空 = 逐级回落（resolveAgentLLMName）。

// scenarioAgent 是场景内一个 agent 的定义（AG-1 读取面；字段对齐 facade.ScenarioAgent，
// 门面不交路径规则，此处只消费其领域字段）。
type scenarioAgent struct {
	Name         string
	IsMain       bool
	Description  string // 描述（25 §3：进**场景层**团队成员段；不再注册为资产）
	RoleTag      string // 角色标签（团队成员段的角色标注）
	Prompt       string // 提示词（主 agent 的提示词 = **agent 层**；25 §3）
	Tools        string // 工具白名单（JSON 数组文本；空 = 不设白名单）
	LLMRef       string // LLM 引用（provider name；空 = 逐级回落）
	DelegateCond string // 委派条件（空 = 无）
}

// loadScenario 读场景记录（data-scenario-load）：返回**场景描述**（场景层正文，25 §3）与
// agent 列表。scenario_id 空 / 读取失败 → ("", nil)。
//
// 25 §3/T3 取舍：数据层的 `systemPrompt` 字段（派生 = 主 agent 的 prompt）**不再取用** ——
// 系统提示词改由 llm server 侧按三层自拼（全局层 + 场景层 + agent 层），主 agent 的 prompt
// 从 `agents` 里的主 agent（main.agent.md）取，故此处只需 description + agents，数据层字段
// 保持兼容不变（不牵动其他消费方）。
func (s *Server) loadScenario(instanceID, scenarioID string) (string, []scenarioAgent) {
	if scenarioID == "" {
		return "", nil
	}
	res, err := dataRequest(s.bus, "data-scenario-load", map[string]any{
		"instance_id": instanceID, "data": map[string]any{"id": scenarioID},
	})
	if err != nil {
		logf("[chonkpilot-server] loadScenario: data-scenario-load failed (scenario_id=%s): %v\n", scenarioID, err)
		return "", nil
	}
	rec, _ := res["data"].(map[string]any)
	if rec == nil {
		return "", nil
	}
	desc, _ := rec["description"].(string)
	raw, _ := rec["agents"].([]any)
	agents := make([]scenarioAgent, 0, len(raw))
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		agents = append(agents, scenarioAgent{
			Name:         strings.TrimSpace(agentFieldStr(m["name"])),
			IsMain:       agentMainField(m["isMain"]),
			Description:  agentFieldStr(m["description"]),
			RoleTag:      agentFieldStr(m["roleTag"]),
			Prompt:       agentFieldStr(m["prompt"]),
			Tools:        agentFieldStr(m["tools"]),
			LLMRef:       strings.TrimSpace(agentFieldStr(m["llmRef"])),
			DelegateCond: strings.TrimSpace(agentFieldStr(m["delegateCond"])),
		})
	}
	return desc, agents
}

// scenarioAgentOf 取场景内指定名字的 agent（未找到 → nil）。
func scenarioAgentOf(agents []scenarioAgent, name string) *scenarioAgent {
	if name == "" {
		return nil
	}
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}

// mainScenarioAgent 取场景主 agent（isMain 为真；缺省回落首项——「主 agent 恒列首位」）。
// 无 agent → nil。
func mainScenarioAgent(agents []scenarioAgent) *scenarioAgent {
	for i := range agents {
		if agents[i].IsMain {
			return &agents[i]
		}
	}
	if len(agents) == 0 {
		return nil
	}
	return &agents[0]
}

// resolveAgentDef 是 agent 定义的**统一判据**（供 llm_run 委派判定 agentDelegable 与各轮次定义
// 读取 newTurnCtx 共用同一入口 —— 消除「可委派对象」与「取回的定义」不同源）：
//  1. instance 级 = `agents`（当前场景内同名 agent，**优先级最高**）；
//  2. global 级 = 内置 agent 内存注册表（**app 级场景**内的 agent，仅提示词，见 registeredAgentDef）。
//
// 命名与唯一性（2026-09-26）：引用格式 = `<场景id>/<agent名>`（可省场景前缀 → 裸名）——
//
//	场景前缀 == 当前场景 id 或为空时按当前场景内 agent 匹配；否则按注册表 `场景id/agent名` 精确匹配。
//
// 同名时以 instance 级为准（25 §5/T2：agent 不注册资产，判据与 gateway 资产面无关）。
// 返回：(instance 级定义或 nil, global 级定义；两者都未命中 → (nil, AgentDef{}))。
func (s *Server) resolveAgentDef(scenarioID string, agents []scenarioAgent, ref string) (*scenarioAgent, AgentDef) {
	scn, name := splitAgentRef(ref)
	if scn == "" || scn == scenarioID {
		if a := scenarioAgentOf(agents, name); a != nil {
			return a, AgentDef{}
		}
	}
	if d, ok := s.registeredAgentDef(ref); ok {
		return nil, d
	}
	return nil, AgentDef{}
}

// agentDelegable 判定 llm_run 的委派对象名是否可委派。**统一判据**（与 newTurnCtx 的定义读取
// 同入口）：instance 级（当前场景内同名 agent）**优先**命中 → 可委派；未命中才回落 global 级
// （内置 agent 内存注册表 = app 级场景内的 agent，含注册表为空的宽松语义，见 lookupAgent）。
// 同名时以 instance 级为准。引用格式见 resolveAgentDef（`<场景id>/<agent名>` 或裸名）。
func (s *Server) agentDelegable(instanceID, scenarioID, name string) bool {
	if name == "" {
		return false
	}
	_, agents := s.loadScenario(instanceID, scenarioID)
	if a, _ := s.resolveAgentDef(scenarioID, agents, name); a != nil {
		return true
	}
	return s.lookupAgent(name)
}

// childAgentSystem 构造子轮次 persona 的 system 文本（AG-1）：agent 标注（既有语义）
// + 该 agent 的提示词 + 委派条件（各自空 = 不附加）。
func childAgentSystem(name, prompt, delegateCond string) string {
	parts := []string{"[子会话 agent: " + name + "]"}
	if p := strings.TrimSpace(prompt); p != "" {
		parts = append(parts, p)
	}
	if c := strings.TrimSpace(delegateCond); c != "" {
		parts = append(parts, "[委派条件] "+c)
	}
	return strings.Join(parts, "\n")
}

// resolveAgentLLMName 解析 agent 在本轮生效的 LLM（provider name；AG-C1 逐级回落，取第一个非空）：
//  1. agent.llmRef（agent 级显式选择）；
//  2. 子系统默认 LLM 键 `llm.<子系统>`（仅当 subKey 非空 = 该 agent 由子系统场景调用；键缺失 /
//     空串已由数据层读侧回落 defaultLLM，SL-1 → 此处**不重复**回落）；
//  3. inherit = 现有通道的「环境 LLM」（顶层轮次 = llm-start.llm 客户端选择；子轮次 = 父轮次 llm）；
//  4. 全局 defaultLLM（数据层读侧值；旧 int 索引经 data.LLMRefName 折算为 name）；
//  5. 均空 → ""（= 系统默认：exe 启动参数，调用方保持现状不覆盖）。
//
// 每轮开始调用一次（AG-C5：热生效，不缓存）。③ 与 ④ 的先后 = 「现有通道优先」：agent 未配
// llmRef 时沿用本轮既定 LLM，不因 defaultLLM 而偏离；仅当无既定 LLM 时才取 defaultLLM。
// 无子系统键且 inherit 非空时零配置读取（与 ③ 直接返回等价，避开每轮一次 usr 配置读）。
//
// 生产调用方现状：newTurnCtx 传 subKey="" —— agent 委派路径（llm_run）无子系统上下文，故第 ② 级
// 不参与；该级保留 = AG-C1 完整回落链（由子系统场景调用的 agent 按需传入 `llm.<子系统>` 键）。
func (s *Server) resolveAgentLLMName(instanceID, agentRef, subKey, inherit string) string {
	if ref := strings.TrimSpace(agentRef); ref != "" {
		return ref
	}
	if subKey == "" && inherit != "" {
		return inherit
	}
	if s.cfg == nil {
		return inherit
	}
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] resolveAgentLLMName: UserConfigGet failed: %v\n", err)
		return inherit
	}
	cfg := res.Config
	if subKey != "" {
		// 键缺失 / 空串已由数据层读侧回落 defaultLLM（SL-1）。
		if name := data.LLMRefName(cfg[subKey], cfg["llms"]); name != "" {
			return name
		}
	}
	if inherit != "" {
		return inherit
	}
	return data.LLMRefName(cfg["defaultLLM"], cfg["llms"])
}

// parseToolWhitelist 解析 agent 的 tools 白名单文本（JSON 数组文本 = 场景编辑器写入形态；
// 兼容逗号 / 换行分隔的历史文本）→ 名字集合。空 / 解析不到任何名字 → nil（= 不限制，AG-C4）。
func parseToolWhitelist(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var names []string
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		names = arr
	} else {
		names = strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r'
		})
	}
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// agentFieldStr 取 agent 字段文本：字符串原样；非字符串（数组等）→ JSON 文本
// （对齐门面 DTO 的 string 承载形态 facade.ScenarioAgent.Tools）；nil → ""。
func agentFieldStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// agentMainField 取 agent isMain 判定（bool 或 "true" 文本；对齐 capfs isMainAgent 口径）。
func agentMainField(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	}
	return false
}

// ── 场景子 agent 只"注入"不"注册"（25 §5 / T2，2026-09-25）──────────────
//
// 原段（`syncScenarioAgents` / `unregisterScenarioAgents` / `scenAgentAsset` / `scenarioAgentReg`
// / `emitScenarioAgent` / `agentAssetDesc`）把当前场景的**子 agent** 经 `prompts/register`
// （asset_kind=agent，instance 级 scope）注册进 gateway 资产目录，使 `mcp_find(type=agent)` 可
// 发现——**已整体撤除**（25 §5「agent 只注入不注册」：`mcp_find` 收敛为 `tool|skill|resource|all`，
// agent 已退出资产面）。
//
// 现行为：LLM 通过**系统提示词的场景层**（25 §3：`scenario.description` + 代码按场景 agents 自动
// 拼接的成员段，见 turn.go `scenarioLayer`）知道可委派谁；委派判定的定义来源仍是 `resolveAgentDef`
// （instance 级场景 + global 级内置 agent 注册表〔app 级场景〕，见上），与资产目录无关。
// 成员段/委派引用统一带**场景前缀**（`<场景id>/<agent名>`，命名与唯一性，2026-09-26）。
