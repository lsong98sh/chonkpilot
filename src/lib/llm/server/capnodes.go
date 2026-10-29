package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-assembly"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/exedir"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

// capNodeSpec 是要接入的一级 capability 根（节点名后缀 + 契约根目录）。
type capNodeSpec struct {
	suffix string // 节点名后缀：user / project / prjusr（节点名 = instanceID + "-" + suffix）
	root   string // 契约根（含 tools/skills/prompts/resources 四类子目录）
}

// capNodeSpecs 解析实例可见的用户级/项目级/项目私有级 capability 根（仅返回存在的目录）。
// 系统级 = <exeDir>/capability（内嵌 self 节点，New 时装配），此处不重复。
// 用户级根跨项目共享（同一 usr 根），但仍按 instance 各注册一份节点：
// 生命周期与实例对称、幂等无需引用计数（scope=instance 下异实例同名可并存），
// 且每个实例都接入同一用户根 → 跨项目可见的实际效果不丢。
//
// prjusr（项目私有级，P4 2026-10-01）：根 = <prjusr 数据根>/capability，由数据层门面
// KnowledgeRoot(kind=prjusr) 解析（与知识库/场景写读同源）；解析不出（实例未登记 / prj 打不开）
// → 跳过，不影响既有三级。
func (s *Server) capNodeSpecs(instanceID, workDir string) []capNodeSpec {
	specs := []capNodeSpec{}
	if root := persist.CapUserRoot(s.opts.UsrPath); dirExists(root) {
		specs = append(specs, capNodeSpec{suffix: "user", root: root})
	}
	if workDir != "" {
		if root := persist.CapProjectRoot(workDir); dirExists(root) {
			specs = append(specs, capNodeSpec{suffix: "project", root: root})
		}
	}
	if root := s.prjUsrCapRoot(instanceID); dirExists(root) {
		specs = append(specs, capNodeSpec{suffix: "prjusr", root: root})
	}
	return specs
}

// prjUsrCapRoot 解析实例的**项目私有级 capability 根**（<prjusr 数据根>/capability）。
// 经数据层门面 KnowledgeRoot(kind=prjusr) 解析——与知识库 / 场景的 prjusr 写读**同一根**
// （避免路径规则分叉）。解析不出（实例未登记 / prj 打不开 / project-id 缺失）→ 空串（不含该级）。
func (s *Server) prjUsrCapRoot(instanceID string) string {
	if s.data == nil || instanceID == "" {
		return ""
	}
	res, err := s.data.KnowledgeRoot(facade.KnowledgeRootRequest{
		Kind: persist.KindPrjUsr, InstanceID: instanceID,
	})
	if err != nil || res.Root == "" {
		return ""
	}
	return res.Root
}

// capDirOp 是一次待发送的 capability dir 节点总线操作（register=true 注册 / false 注销）。
// registerCapNodesLocked / reconcileCapabilityNodes 在 capMu 内**只收集**操作与维护内存态，
// 解锁后统一 applyCapDirOps 发送（B-35）：避免跨 gateway 总线往返（registerDirNode/unregisterDirNode
// 15s 超时、refreshTools ListTools 15s 超时）持锁串行阻塞全部 capability 接入/热重扫。
type capDirOp struct {
	register   bool
	instanceID string
	nodeName   string
	root       string // register 时生效
}

// registerCapabilityNodes 逐根经 gateway 方法面登记 dir 节点（幂等：同名节点已登记则跳过）。
// 同名冲突不可能发生：四级根各由独立节点承载且暴露名带节点前缀
// （P0a 2026-10-29 起内置 self 节点 = 原名；dir 节点 = <instanceID>-user_ / -project_ / -prjusr_），
// gateway 仅在同 (scope, 暴露名) 时报错；
// 注册失败记日志、跳过，不影响其他节点与启动。注册后主动刷新工具缓存（见 refreshTools）。
func (s *Server) registerCapabilityNodes(instanceID, workDir string) {
	s.capMu.Lock()
	ops, hadSpecs := s.registerCapNodesLocked(instanceID, workDir)
	s.capMu.Unlock()
	s.applyCapDirOps(ops)
	// 刷新工具定义缓存：dir 节点注册 gateway 不发 mcp-gateway-changed，故此处主动重拉，
	// 使 hot=true 的用户/项目级工具进入 LLM 工具面（toolsForLLM）。
	if hadSpecs {
		s.refreshTools()
	}
}

// registerCapNodesLocked 是 registerCapabilityNodes 的无锁实现（调用方须持 capMu；
// T-21 重扫 reconcileCapabilityNodes 复用，避免自锁）。**只在锁内**维护 dirNodes 内存态
// 并返回待发送 ops，总线往返交由调用方解锁后 applyCapDirOps（B-35）。
// 返回 hadSpecs = 该实例解析出了非空 capability 根（= 调用方需刷新工具缓存，与现状一致）。
func (s *Server) registerCapNodesLocked(instanceID, workDir string) (ops []capDirOp, hadSpecs bool) {
	specs := s.capNodeSpecs(instanceID, workDir)
	if len(specs) == 0 {
		return nil, false
	}
	for _, spec := range specs {
		nodeName := instanceID + "-" + spec.suffix
		s.mu.Lock()
		dup := false
		for _, n := range s.dirNodes[instanceID] {
			if n == nodeName {
				dup = true
				break
			}
		}
		if !dup {
			s.dirNodes[instanceID] = append(s.dirNodes[instanceID], nodeName)
		}
		s.mu.Unlock()
		if dup {
			continue // 幂等：重复 instance-register（重连/换绑）不重复建节点
		}
		ops = append(ops, capDirOp{register: true, instanceID: instanceID, nodeName: nodeName, root: spec.root})
	}
	return ops, true
}

// applyCapDirOps 解锁后统一发送收集到的 capability dir 节点总线操作（B-35）。
// 注册失败记日志、跳过（语义与现状一致：不影响其他节点）。
func (s *Server) applyCapDirOps(ops []capDirOp) {
	// 总线应用串行化（B-35 补丁）：内存态可在 capMu 内并发维护，但 unregister→register
	// 的**顺序**必须跨调用保持——两次并发重扫的交错会出现「同名节点被 spawn 两次」
	// （gateway 报 tool 名冲突）或「注销晚于注册」。此处只串行化 IO，仍不返回 capMu。
	s.capApplyMu.Lock()
	defer s.capApplyMu.Unlock()
	for _, op := range ops {
		if op.register {
			if err := s.registerDirNode(op.instanceID, op.nodeName, op.root); err != nil {
				logf("[chonkpilot-server] capability dir 节点注册失败（跳过 %s）: %v\n", op.nodeName, err)
			}
			continue
		}
		s.unregisterDirNode(op.instanceID, op.nodeName)
	}
}

// unregisterCapabilityNodes 注销实例名下全部 capability dir 节点（onExit 对称清理）。
func (s *Server) unregisterCapabilityNodes(instanceID string) {
	if s.gw == nil {
		return // DisableMCP（无内嵌 gateway）
	}
	s.capMu.Lock()
	defer s.capMu.Unlock()
	s.mu.Lock()
	names := s.dirNodes[instanceID]
	delete(s.dirNodes, instanceID)
	s.mu.Unlock()
	for _, nodeName := range names {
		s.unregisterDirNode(instanceID, nodeName)
	}
	if len(names) > 0 {
		s.refreshTools()
	}
}

// reconcileCapabilityNodes 重扫全部在用 capability 根并重建 dir 节点（T-21：原语保存后热生效）。
// 复用既有消息面 servers/unregister + servers/register（零新增主题）：逐实例先拆除全部已登记
// 节点（含根已消失者），再按当前文件树（capNodeSpecs）重新扫描注册并刷新工具缓存。
// 由 capWatcher 去抖后调用；网关 unregister/register 亦广播 mcp-gateway-changed，工具缓存同步刷新。
func (s *Server) reconcileCapabilityNodes() {
	if s.gw == nil {
		return
	}
	// 实例 → work_dir 的权威映射 = 共通实例视图 `s.im`（instance.Manager，见 49 §2.4
	// 「映射表只有一份」）：本层不另存副本，按 Manager 快照逐实例重建 dir 节点。
	insts := make(map[string]string)
	for _, info := range s.im.List() {
		if info.ID == "" || info.WorkDir == "" {
			continue
		}
		insts[info.ID] = info.WorkDir
	}
	s.capMu.Lock()
	// 锁内只维护内存态 + 收集待发送 ops（B-35）：unregister/register 与 refreshTools 等
	// 总线往返一律挪到解锁后执行；逐实例保序（先注销旧节点、再重建）。
	var ops []capDirOp
	removed, hadSpecs := false, false
	for inst, wd := range insts {
		s.mu.Lock()
		names := append([]string(nil), s.dirNodes[inst]...)
		delete(s.dirNodes, inst)
		s.mu.Unlock()
		if len(names) > 0 {
			removed = true
		}
		for _, nodeName := range names {
			ops = append(ops, capDirOp{instanceID: inst, nodeName: nodeName})
		}
		regs, spec := s.registerCapNodesLocked(inst, wd) // 按当前根重建（根不存在 → 自动跳过）
		ops = append(ops, regs...)
		hadSpecs = hadSpecs || spec
	}
	s.capMu.Unlock()
	s.applyCapDirOps(ops)
	if removed || hadSpecs {
		// registerCapNodesLocked 在无根时提前返回、不刷新缓存；此处兜底确保被移除的工具退出工具面。
		s.refreshTools()
	}
}

// reloadAppContracts 重扫系统级 app capability 根（T-21②：三级根中 <exeDir>/capability 由本层
// **能力源**（assembly.Stack.Server）在 New 时一次性承载，不能像 user/project 两级那样经
// dir 节点 unregister+register 重建）。
// 链路：重新 RegisterContracts 进同一内嵌 go-sdk server（同名原语覆盖更新 = 契约改动生效、
// 新增原语注入）→ RemoveTools 回收已删除/改名的 app 工具（RegisterContracts 只增改不删）→
// 复用既有方法面 gateway/reload 让已连接下游重拉 list → 聚合工具面刷新（零新增主题）。
// 只读语义不变：UI 不可改 app 根，仅外部/构建期更新后热生效。
func (s *Server) reloadAppContracts() {
	if s.gw == nil || s.mcpServer == nil || s.mcpCfg == nil {
		return
	}
	root := s.mcpCfg.Root
	// P6（2026-10-19）：外部工具模式 → 只重扫**资产**（RegisterContracts 内部因 SkipTools 跳过
	// tools）；工具生命周期由外部进程（chonkpilot-core-mcp-server 监听同一根）自行
	// register/unregister，宿主不得 RemoveTools（否则会误删外部进程已提供的工具）。
	if err := mcpms.RegisterContracts(s.mcpServer, root, s.mcpCfg); err != nil {
		logf("[chonkpilot-server] app capability 契约重扫失败: %v\n", err)
		return
	}
	cur := appToolNames(root)
	s.mu.Lock()
	var stale []string
	if !s.mcpCfg.SkipTools {
		for name := range s.appTools {
			if !cur[name] {
				stale = append(stale, name)
			}
		}
	}
	s.appTools = cur
	s.mu.Unlock()
	if len(stale) > 0 {
		s.mcpServer.RemoveTools(stale...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = s.emitGateway(ctx, "gateway/reload", nil) // 已连接下游重拉 list（含新契约工具）
	s.refreshTools()                                 // 本端工具定义缓存同步刷新（toolsForLLM 依据）
}

// appToolNames 递归收集 app capability 根下全部 *.tool.md 的原语名（T-21② 重扫回收用；
// **RB-5 L5**：实现已收敛到入口装配器 `assembly.AppToolNames`，此处保留为包内薄别名）。
func appToolNames(root string) map[string]bool { return assembly.AppToolNames(root) }

// registerDirNode 经 gateway 方法面登记 capability 目录节点（servers/register → mcp-servers-register）。
// GATEWAY-2 L5：gateway 不再持有 dir 能力源，目录根改以**普通 stdio 下游**接入 ——
// `chonkpilot-core-mcp-server --stdio --root <folder>` 用同一 RegisterContracts 暴露
// tools + prompts/skills/resources；载荷走既有 `mcp_server` 键（不新增主题）。
func (s *Server) registerDirNode(instanceID, nodeName, folder string) error {
	runtimePath, err := coreMCPPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(withInstance(context.Background(), instanceID), 15*time.Second)
	defer cancel()
	_, err = s.emitGateway(ctx, "servers/register", map[string]any{
		"name":   nodeName,
		"scope":  instanceID,
		"origin": "builtin",
		"mcp_server": map[string]any{
			"runtime":   runtimePath,
			"args":      []string{"--stdio", "--root", folder},
			"transport": "stdio",
			"category":  "dir",
		},
	})
	return err
}

// coreMCPPath 定位 chonkpilot-core-mcp-server 可执行文件（与 coretools 监管器同一口径：
// `<exe目录>/<name>` 或 `<exe目录>/mcps/core-mcp/<name>`）。
//
// 覆盖：环境变量 `CHONKPILOT_CORE_MCP`（显式可执行文件路径）——供测试/便携部署指定非
// exe 目录下的实现（测试环境为 go test 临时目录，产物内无该 exe）。
func coreMCPPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("CHONKPILOT_CORE_MCP")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("core-mcp: CHONKPILOT_CORE_MCP 指向的文件不存在: %s", p)
	}
	dir, err := exedir.Dir()
	if err != nil {
		return "", fmt.Errorf("core-mcp: exe 目录: %w", err)
	}
	name := "chonkpilot-core-mcp-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, p := range []string{
		filepath.Join(dir, name),
		filepath.Join(dir, "mcps", "core-mcp", name),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("core-mcp: 未找到 %s（查找 %s 与 %s）", name, dir, filepath.Join(dir, "mcps", "core-mcp"))
}

// unregisterDirNode 经 gateway 方法面注销 dir 类型节点（servers/unregister → 拆除）。
func (s *Server) unregisterDirNode(instanceID, nodeName string) {
	ctx, cancel := context.WithTimeout(withInstance(context.Background(), instanceID), 15*time.Second)
	defer cancel()
	_, _ = s.emitGateway(ctx, "servers/unregister", map[string]any{"name": nodeName, "scope": instanceID})
}

// refreshTools 重新拉取 gateway tools/list 刷新工具定义缓存（toolsForLLM 的 hot 过滤依据）。
func (s *Server) refreshTools() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = s.gc.ListTools(ctx)
}

// dirExists 判定路径存在且为目录（capability 根接入前检查；不存在则跳过）。
func dirExists(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
