package server

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-assembly"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	"github.com/lsong98sh/chonkpilot-framework/host/core/exedir"
)

// capNodeSpec 是要接入的一级 capability 根（节点名后缀 + 契约根目录）。
type capNodeSpec struct {
	suffix string // 节点名后缀：user / project / prjusr（节点名 = instanceID + "-" + suffix）
	root   string // 契约根（含 tools/skills/prompts/resources 四类子目录）
	// sig 是契约根签名（根路径 + 契约文件相对路径/大小/修改时间）：热重扫只重建变化节点。
	sig string
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
		specs = append(specs, capNodeSpec{suffix: "user", root: root, sig: capRootSignature(root)})
	}
	if workDir != "" {
		if root := persist.CapProjectRoot(workDir); dirExists(root) {
			specs = append(specs, capNodeSpec{suffix: "project", root: root, sig: capRootSignature(root)})
		}
	}
	if root := s.prjUsrCapRoot(instanceID); dirExists(root) {
		specs = append(specs, capNodeSpec{suffix: "prjusr", root: root, sig: capRootSignature(root)})
	}
	return specs
}

// capRootSignature 计算 capability 根签名（根路径 + 契约文件相对路径/大小/修改时间，字典序拼接）：
// 热重扫据此判断某节点是否需要重建（根消失/新增后缀/契约文件改动）；未变化节点不注销不注册。
//
// 只读本地小目录（capability 根），由 capWatcher 去抖后调用，不在会话循环内。
func capRootSignature(root string) string {
	var b strings.Builder
	b.WriteString(root)
	type entry struct {
		rel  string
		size int64
		mod  int64
	}
	var files []entry
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// 契约文件后缀固定四类（*.tool.md / *.prompt.md / *.skill.md / *.resource.md）。
		if filepath.Ext(path) != ".md" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		files = append(files, entry{rel: filepath.ToSlash(rel), size: info.Size(), mod: info.ModTime().UnixNano()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	for _, f := range files {
		fmt.Fprintf(&b, "|%s:%d:%d", f.rel, f.size, f.mod)
	}
	return b.String()
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
			if s.dirSigs == nil {
				s.dirSigs = map[string]map[string]string{}
			}
			if s.dirSigs[instanceID] == nil {
				s.dirSigs[instanceID] = map[string]string{}
			}
			s.dirSigs[instanceID][nodeName] = spec.sig
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
	delete(s.dirSigs, instanceID)
	s.mu.Unlock()
	for _, nodeName := range names {
		s.unregisterDirNode(instanceID, nodeName)
	}
	if len(names) > 0 {
		s.refreshTools()
	}
}

// reconcileCapabilityNodes 重扫全部在用 capability 根并**只重建变化节点**（T-21：原语保存后热生效）。
//
// 判据 = capRootSignature（根路径 + 契约文件清单）；未变化节点**不动**（不注销不注册）→
// 热重扫不再出现「先拆全部再重建」的毫秒级工具面缺口。变化节点（根消失/新增/契约文件改动）
// 走既有 servers/unregister + servers/register；顺序 = 先注册新增 → 再注销+重建变更 → 最后注销废弃。
// 由 capWatcher 去抖/周期兜底重扫后调用；网关 unregister/register 亦广播 mcp-gateway-changed，
// 工具缓存同步刷新。返回本次产生的 dir 节点操作数（0 = 未变更；周期重扫据此断无重复注册）。
func (s *Server) reconcileCapabilityNodes() int {
	if s.gw == nil {
		return 0
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
	// 锁内只维护内存态 + 收集待发送 ops（B-35）：unregister/register 与 refreshTools 等
	// 总线往返一律挪到解锁后执行。
	specsByInst := make(map[string][]capNodeSpec, len(insts))
	for inst, wd := range insts {
		specsByInst[inst] = s.capNodeSpecs(inst, wd)
	}
	s.capMu.Lock()
	s.mu.Lock()
	existing := make(map[string][]string, len(s.dirNodes))
	oldSigs := make(map[string]map[string]string, len(s.dirSigs))
	for inst := range insts {
		existing[inst] = append([]string(nil), s.dirNodes[inst]...)
		if sigs := s.dirSigs[inst]; len(sigs) > 0 {
			cp := make(map[string]string, len(sigs))
			for k, v := range sigs {
				cp[k] = v
			}
			oldSigs[inst] = cp
		}
	}
	s.mu.Unlock()
	plan := planCapabilityNodes(insts, specsByInst, existing, oldSigs)
	for inst, nodes := range plan.nodes {
		if len(nodes) == 0 {
			delete(s.dirNodes, inst)
			delete(s.dirSigs, inst)
			continue
		}
		s.mu.Lock()
		s.dirNodes[inst] = nodes
		s.dirSigs[inst] = plan.sigs[inst]
		s.mu.Unlock()
	}
	s.capMu.Unlock()
	ops := len(plan.adds) + len(plan.changes) + len(plan.removes)
	s.applyCapDirOps(plan.ops())
	if ops > 0 {
		s.refreshTools()
	}
	return ops
}

// capPlan 是热重扫的待发送操作与收敛后的内存态。
type capPlan struct {
	adds    []capDirOp                   // 新增节点：先注册
	changes []capDirOp                   // 变更节点：注销 + 重建成对
	removes []capDirOp                   // 废弃节点：最后注销
	nodes   map[string][]string          // instanceID → 收敛后的节点名（capNodeSpecs 顺序）
	sigs    map[string]map[string]string // instanceID → 节点名 → 根签名
}

// ops 按「先注册新增 → 再注销+重建变更 → 最后注销废弃」排序。
func (p capPlan) ops() []capDirOp {
	out := make([]capDirOp, 0, len(p.adds)+len(p.changes)+len(p.removes))
	out = append(out, p.adds...)
	out = append(out, p.changes...)
	out = append(out, p.removes...)
	return out
}

// planCapabilityNodes 是热重扫的**纯决策**（无 IO、无锁）：给定实例、当前根清单与已登记态，
// 产出最小操作集与收敛后的内存态。判据 = 根签名不变即不动（避免工具面窗口）。
func planCapabilityNodes(insts map[string]string, specsByInst map[string][]capNodeSpec,
	existing map[string][]string, oldSigs map[string]map[string]string,
) capPlan {
	plan := capPlan{
		nodes: make(map[string][]string, len(insts)),
		sigs:  make(map[string]map[string]string, len(insts)),
	}
	for inst := range insts {
		specs := specsByInst[inst]
		order := make([]string, 0, len(specs))
		desired := make(map[string]capDirOp, len(specs))
		sigs := make(map[string]string, len(specs))
		for _, spec := range specs {
			nodeName := inst + "-" + spec.suffix
			order = append(order, nodeName)
			desired[nodeName] = capDirOp{register: true, instanceID: inst, nodeName: nodeName, root: spec.root}
			sigs[nodeName] = spec.sig
		}
		have := make(map[string]bool, len(existing[inst]))
		for _, n := range existing[inst] {
			have[n] = true
		}
		// ① 新增：根刚出现 → 先注册（不影响既有节点）。
		for _, name := range order {
			if !have[name] {
				plan.adds = append(plan.adds, desired[name])
				continue
			}
			// ② 变更：同名节点契约根有改动 → 注销后重建（网关拒绝重复注册同名 connected 节点）。
			if oldSigs[inst][name] != sigs[name] {
				plan.changes = append(plan.changes, capDirOp{instanceID: inst, nodeName: name})
				plan.changes = append(plan.changes, desired[name])
			}
		}
		// ③ 废弃：根消失 / 后缀不再需要 → 最后注销。
		for _, name := range existing[inst] {
			if _, ok := desired[name]; !ok {
				plan.removes = append(plan.removes, capDirOp{instanceID: inst, nodeName: name})
			}
		}
		plan.nodes[inst] = order
		plan.sigs[inst] = sigs
	}
	return plan
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

// coreMCPPath 定位 chonkpilot-core-mcp-server 可执行文件（dir 节点 = 每节点一个
// `core-mcp-server --stdio --root <folder>` 普通 stdio 下游，见 26-mcp-gateway「不做进程收敛」）。
//
// 覆盖：环境变量 `CHONKPILOT_CORE_MCP`（显式可执行文件路径）——供测试/便携部署指定非
// exe 目录下的实现（测试环境为 go test 临时目录，产物内无该 exe）。
//
// 未设 env 时由 coreMCPPathIn 按**实际部署布局**定位：桌面构建把 core-mcp-server 与
// 独立 MCP 部署同置于 `dist/other/`（03-构建与部署 · build-mcp-server.ps1），仅认
// `<exeDir>/<name>` 会让全部用户/项目/项目私有级 dir 节点注册失败（capability 原语永不
// 进入工具面，且看起来像"热生效失效"——实测 2026-11-07）。
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
	return coreMCPPathIn(dir)
}

// coreMCPPathIn 在给定 exe 目录下按部署布局候选定位 core-mcp-server（纯路径探测、无全局态，
// 便于单测）。候选顺序 = 现行部署优先，历史/兼容布局兜底：
//  1. `<exeDir>/<name>`                         —— 扁平产物（03-构建与部署 §1 根目录）
//  2. `<exeDir>/mcps/core-mcp/<name>`           —— 62-命令行与参数 §6 文档口径
//  3. `<exeDir>/other/<name>`                   —— build-mcp-server.ps1 部署区（03 §1 表格）
//  4. `<exeDir>/capability/executors/<name>`    —— 历史便携布局（build-desktop 注释口径）
//  5. `<exeDir>/../other/<name>`                —— 旧二元布局 dist/desktop → dist/other
func coreMCPPathIn(exeDir string) (string, error) {
	name := "chonkpilot-core-mcp-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates := []string{
		filepath.Join(exeDir, name),
		filepath.Join(exeDir, "mcps", "core-mcp", name),
		filepath.Join(exeDir, "other", name),
		filepath.Join(exeDir, "capability", "executors", name),
		filepath.Join(filepath.Dir(exeDir), "other", name),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("core-mcp: 未找到 %s（已查找 %s）", name, strings.Join(candidates, "、"))
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
