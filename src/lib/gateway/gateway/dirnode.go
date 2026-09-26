// dir 目录节点（2026-09-07）：servers/register 的 dir 类型（如 <workdir>/@mcp）——
// 契约根的扫描/建官方 go-sdk server 由装配方注入的 **ContractScanner**（RB-2：依赖倒置）完成，
// gateway 只接成品：in-memory 节点聚合（scope = 注册载荷 instance_id；tools 归属 instance，
// 不污染全局）。
//
// RB-3（2026-09-22，能力面共享化）：注册表**按 node key**（dir = 规范化根路径）**唯一**——
// 同根跨 instance 只扫描/只建一份 in-memory 会话与工具对象；instance 只持**引用集**
// （`dirRef`），暴露名按「前缀 → instance+node+tool → 查 refs 校验 → 命中共享 node」解析
// （见 registry.findDirLocked），**不在路由表物化 N×K**。对外零变更：暴露名仍是
// `<节点名>_<原名>`（= 既有 applyPrefix 规则）。
package mcpgateway

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ContractScanner 是**契约目录扫描器**窄接口（RB-2：gateway 不读「源」，由装配方注入；
// 手法同 ExecSink —— 在 gateway 包内声明，避免 gateway lib 反向依赖 chonkpilot-mcp-server）：
// Scan 扫契约根（tools/prompts/skills/resources 四原语）并返回已注册原语的官方 go-sdk server。
type ContractScanner interface {
	Scan(root string) (*mcp.Server, error)
}

// dirNodeKey 返回 dir 目录节点的 **node key**（RB-3 ①：按根路径唯一）：
// 规范化绝对路径（同根跨 instance 命中同一共享节点）。
func dirNodeKey(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
}

// dirNode 是一个目录节点的运行态（node = in-memory 会话，scope = 归属 instance）。
type dirNode struct {
	key     string
	nodeKey string // 共享节点 key（同根多引用共享同一 node）
	scope   string
	name    string
	node    *memNode
}

// registerDirNode 注册 dir 类型节点：经注入的 ContractScanner 扫描 dir 契约根 → 官方 server →
// in-memory 接入（prompts/skills/resources 由 list/read 方法面实时经节点拉取，见 eachMemNode）。
// RB-3 ①：同根（node key）已建 → **复用共享节点**（不再扫描、不再建会话），只加一条引用。
func (g *Gateway) registerDirNode(name, dir, scope string) error {
	key := provKey("dir", scope, name)
	g.mu.Lock()
	_, dup := g.nodes[key]
	g.mu.Unlock()
	if dup {
		return fmt.Errorf("dir node %s already registered", key)
	}

	nodeKey := dirNodeKey(dir)
	sn, ok := g.reg.sharedNodeOf(nodeKey)
	if !ok {
		sc := g.params.ContractScanner
		if sc == nil {
			return fmt.Errorf("dir %s scan: no contract scanner injected", dir)
		}
		ms, err := sc.Scan(dir)
		if err != nil {
			return fmt.Errorf("dir %s scan: %w", dir, err)
		}
		node, err := newMemNode(nodeKey, ms)
		if err != nil {
			return err
		}
		tools, err := node.ListTools(context.Background())
		if err != nil {
			node.Close()
			return err
		}
		// 展示名注入只做一次（共享 schema 供全部引用复用，不再按 instance 复制工具对象）。
		byName := make(map[string]*mcp.Tool, len(tools))
		for _, t := range tools {
			injectDisplayName(t)
			byName[t.Name] = t
		}
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		// 竞态：并发下同根已被别处建好 → 保留先到者，丢弃本次。
		sn = g.reg.swapSharedNode(&sharedNode{key: nodeKey, node: node, tools: tools, byName: byName})
		if sn.node != node {
			node.Close()
		}
	}

	ref := &dirRef{nodeKey: nodeKey, name: name, scope: scope, pk: key}
	ps := &providerState{
		key:    key,
		entry:  &ServerEntry{ID: name, Name: name, Category: "dir", Origin: OriginBuiltin},
		prov:   &memNodeProvider{node: sn.node, inject: true}, // dir=内置来源（本仓 spawn executor）：_meta 透传
		status: "connected",
		cb:     newBreaker(g.params.CBThreshold, g.params.CBCooldown, true),
		scope:  scope,
		ref:    ref,
	}
	if err := g.reg.addDirRef(sn, ref, ps); err != nil {
		// 引用失败：若无其它引用则回收本次可能新建的共享节点（保持"删掉新增路径即回现状"）。
		if node := g.reg.dropSharedIfUnused(nodeKey); node != nil {
			node.Close()
		}
		return err
	}
	g.mu.Lock()
	g.nodes[key] = &dirNode{key: key, nodeKey: nodeKey, scope: scope, name: name, node: sn.node}
	g.mu.Unlock()
	g.logf("[gateway] dir node %s registered (scope=%q, node=%s): %d tools", name, scope, nodeKey, len(sn.tools))
	return nil
}

// unregisterDirNode 注销 dir 类型节点（RB-3 ①：**回收引用**）：断连 + 清理句柄/提供方视图；
// 引用归零才关闭共享节点（其它引用仍在 → 共享 node 保留）。返回 (是否存在, 移除工具数)。
func (g *Gateway) unregisterDirNode(name, scope string) (bool, int) {
	key := provKey("dir", scope, name)
	g.mu.Lock()
	dn := g.nodes[key]
	delete(g.nodes, key)
	g.mu.Unlock()
	if dn == nil {
		return false, 0
	}
	removed, node, release := g.reg.removeDirRef(key)
	g.reg.removeProvider(key) // 清提供方视图（dir 无 routes 物化 → 移除工具数为 0）
	if release && node != nil {
		node.Close() // 引用归零 → 关闭共享节点
	}
	g.logf("[gateway] dir node %s unregistered (removed %d tools, release=%v)", name, removed, release)
	return true, removed
}

// memItem 是全量节点遍历项（node 句柄 + 名 + scope）。
type memItem struct {
	node  *memNode
	name  string
	scope string
}

// forEachMemNodeAll 遍历全部原语节点（self = node "self" scope global；dir 节点全量）——
// list 全量输出用（UI 按 scope/node 分流）。eachMemNode（instance 过滤）供 find/load/调用保留。
func (g *Gateway) forEachMemNodeAll(fn func(n *memNode, nodeName, scope string) error) error {
	var items []memItem
	if g.self != nil {
		items = append(items, memItem{node: g.self, name: "self", scope: scopeGlobal})
	}
	g.mu.Lock()
	for _, dn := range g.nodes {
		items = append(items, memItem{node: dn.node, name: dn.name, scope: dn.scope})
	}
	g.mu.Unlock()
	for _, it := range items {
		if err := fn(it.node, it.name, it.scope); err != nil {
			return err
		}
	}
	return nil
}

// eachMemNode 遍历请求 instance 可见的原语节点（self 恒可见 = global；dir 节点
// global ∪ 归属 instance）——find/load 的 instance 隔离用。
func (g *Gateway) eachMemNode(inst string, fn func(node *memNode) error) error {
	var nodes []*memNode
	if g.self != nil {
		nodes = append(nodes, g.self)
	}
	g.mu.Lock()
	for _, dn := range g.nodes {
		if dn.scope == scopeGlobal || dn.scope == inst {
			nodes = append(nodes, dn.node)
		}
	}
	g.mu.Unlock()
	for _, n := range nodes {
		if err := fn(n); err != nil {
			return err
		}
	}
	return nil
}

// eachMemNodeVisible 同 eachMemNode，但**附节点名与归属域**（RB-4 ②：find/load 的资产视图
// 需带 node/scope 以与注册资产同形）。
func (g *Gateway) eachMemNodeVisible(inst string, fn func(n *memNode, nodeName, scope string) error) error {
	var items []memItem
	if g.self != nil {
		items = append(items, memItem{node: g.self, name: "self", scope: scopeGlobal})
	}
	g.mu.Lock()
	for _, dn := range g.nodes {
		if dn.scope == scopeGlobal || dn.scope == inst {
			items = append(items, memItem{node: dn.node, name: dn.name, scope: dn.scope})
		}
	}
	g.mu.Unlock()
	for _, it := range items {
		if err := fn(it.node, it.name, it.scope); err != nil {
			return err
		}
	}
	return nil
}
