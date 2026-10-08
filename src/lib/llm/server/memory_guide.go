// 记忆库带出指引（42 §2 (27) / 41 D-16 / I-68 ②）：在场景 system prompt 组装处注入一条说明，
// 让 LLM **按当前场景到记忆目录按需读取**相关记忆，而非把记忆全文注入上下文。
//
// 落点由持久化侧唯一来源（chonkpilot-data/persist 记忆库域）提供；类别清单**动态获取**，
// 避免清单两处维护、且让用户新增的自定义类别即时出现在指引中：
//
//	项目记忆：<workdir>/.chonkpilot/memory/<类别>.md
//	用户偏好：~/.chonkpilot/用户偏好.md（唯一用户级、不可配置）
//
// 清单取自 data-memory-list（目录扫描 + token 估算，代价不低），故在 server 侧做**短时 TTL 缓存**
// （I-68 ②）：TTL 内多轮/多轮内复用同一清单，仅在 data-memory-refresh（既有主题，save/delete 后
// 广播）或 TTL 过期后重新取数，消除"每轮都发 data-memory-list"的开销。
//
// **门控**（P0 修复）：指引与沉淀必须同一口径——读同一份 prj 配置（data-prj-config-list，与
// plugin-memory 一致）：记忆库关闭（memory.enabled 非 true）→ **完全不注入指引**；类别被禁用
// （memory.category.<类别> = false，含"用户偏好"）→ 该类别不列入指引。门控按次读取（配置可即时
// 生效），类别清单缓存语义不变（缓存的是原始清单，过滤在取用处）。
package server

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// memoryCategoryTTL 类别清单缓存存活时长（I-68 ②）：清单请求含目录扫描与 token 估算，
// 短时缓存避免同一实例每轮重复请求；内存变更经 data-memory-refresh 即时失效，TTL 只是
// "漏失效"时的兜底上限。
const memoryCategoryTTL = 60 * time.Second

// memoryCategoryCache 是记忆类别清单的短时 TTL 缓存（I-68 ②，并发安全）。
// 键 = instanceID：实例↔workDir 一一绑定，等价于按工作区分桶，多实例/多工作区不串味。
type memoryCategoryCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]memoryCategoryEntry
}

// memoryCategoryEntry 是单个实例的缓存项（清单 + 过期时刻）。
type memoryCategoryEntry struct {
	names   []string
	expires time.Time
}

// newMemoryCategoryCache 建缓存（ttl <= 0 回落 memoryCategoryTTL）。
func newMemoryCategoryCache(ttl time.Duration) *memoryCategoryCache {
	if ttl <= 0 {
		ttl = memoryCategoryTTL
	}
	return &memoryCategoryCache{ttl: ttl, entries: make(map[string]memoryCategoryEntry)}
}

// get 取未过期的清单（未命中/已过期 → ok=false）；nil 接收者安全（裸构造的 Server 未初始化缓存）。
func (c *memoryCategoryCache) get(key string) ([]string, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.names, true
}

// put 写入清单（TTL 自写入时刻起算）；nil 接收者安全。
func (c *memoryCategoryCache) put(key string, names []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = memoryCategoryEntry{names: names, expires: time.Now().Add(c.ttl)}
}

// invalidate 失效缓存：instanceID 空 → 全清（refresh 未带归属时的保守处置），否则只清该实例。
// nil 接收者安全。
func (c *memoryCategoryCache) invalidate(instanceID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if instanceID == "" {
		c.entries = make(map[string]memoryCategoryEntry)
		return
	}
	delete(c.entries, instanceID)
}

// memoryGuide 生成记忆库带出指引（工作目录未知 → 空串，不注入）。
// 指引只给目录/类别与用法，不含记忆正文（正文由 LLM 按需读取）。
// 返回前替换 {{toolchain.<key>}} 占位符（指引随每轮请求拼在最前，属 prompt 文本；
// 无占位符时零成本直返，不影响既有文本与 {{env.CHONKPILOT_WORKDIR}}）。
//
// 门控告：记忆库未启用（配置缺失/非 true，与 plugin-memory 默认关闭一致）→ 空串（不注入，也不取
// 类别清单——避免"关闭态仍被指引去读类别"、也避免触发 persist 侧预置文件）；启用时**被禁用的类别
// 不列入**（项目类别逐个过滤，"用户偏好"按同键口径）；无可指引项 → 空串。
func (s *Server) memoryGuide(instanceID, workDir string) string {
	if workDir == "" {
		return ""
	}
	cfg := s.memoryPrjConfig(instanceID)
	if memoryCfgStr(cfg[memoryEnabledKey]) != "true" {
		return ""
	}
	projectDir := filepath.Join(workDir, ".chonkpilot", "memory")
	userFile := filepath.Join(filepath.Dir(data.UserPath()), persist.MemoryUserCategory+".md")
	cats := make([]string, 0)
	for _, name := range s.memoryCategoryNames(instanceID) {
		if memoryCategoryOn(cfg, name) {
			cats = append(cats, name)
		}
	}
	userPrefOn := memoryCategoryOn(cfg, persist.MemoryUserCategory)
	if len(cats) == 0 && !userPrefOn {
		return "" // 全部类别禁用 → 无可指引项
	}
	var b strings.Builder
	b.WriteString("【记忆库】以下是本机沉淀的项目/用户记忆文件，请根据当前场景到记忆目录按需读取相关记忆内容（用工具读文件、传绝对路径），不必全部读取：\n")
	if len(cats) > 0 {
		b.WriteString("- 项目记忆目录：" + projectDir + "（类别文件：" + strings.Join(cats, ".md、") + ".md）\n")
	}
	if userPrefOn {
		b.WriteString("- 用户偏好：" + userFile + "\n")
	}
	b.WriteString("路径须为绝对路径；在 DSL 中拼项目内路径请用 {{env.CHONKPILOT_WORKDIR}}。")
	return s.replaceToolchain(instanceID, b.String())
}

// assetGuide 生成**知识库资产检索**指引（RB-7，2026-09-21）：与 memoryGuide 并列注入，写清
// 「资产」与「记忆」两条路径的区别（42 §2 知识获取的两条路径）。
//
//	① 记忆 = 沉淀的对话/工作记录（本机文件，用文件工具按绝对路径读）→ memoryGuide；
//	② 资产 = 工具 + 知识库原语（tool/skill/resource）→ mcp_find 检索 + mcp_load 取内容。
//	   （25 §5/T2：prompt 与 agent 已移出 LLM 检索面，不在本指引范围内。）
//
// **门控**（RB-7：知识库未启用 / 无资产时不误导）：仅当本实例**已接入 capability dir 节点**
// （= 用户级/项目级知识库已启用且有内容，见 registerCapabilityNodes）且内嵌 gateway 可用
// （mcp_find/mcp_load 存在）时注入；否则空串（不注入）。
func (s *Server) assetGuide(instanceID string) string {
	if s.gw == nil {
		return "" // 无内嵌 gateway：meta 工具（mcp_find/mcp_load）不存在 → 不指引
	}
	s.mu.Lock()
	dirs := append([]string(nil), s.dirNodes[instanceID]...)
	s.mu.Unlock()
	if len(dirs) == 0 {
		return "" // 知识库未启用 / 无资产 → 不注入（避免误导）
	}
	var b strings.Builder
	b.WriteString("【知识库资产】本实例已接入知识库（用户级/项目级能力原语），可检索的资产含工具（tool）、技能（skill）、资源（resource）：\n")
	b.WriteString("- 检索：工具 mcp_find（type=tool|skill|resource|all；可给 query 关键词或 purpose 任务目标）。\n")
	b.WriteString("- 取内容：工具 mcp_load（name=资产名，kind=资产类型；内容实时读取）。\n")
	b.WriteString("与【记忆库】的区别：记忆 = 本机沉淀的工作/对话记录（用文件工具按绝对路径读文件）；资产 = 知识库原语（用 mcp_find 检索、mcp_load 读取内容）。")
	return s.replaceToolchain(instanceID, b.String())
}

// memoryEnabledKey / memoryCategoryPrefix 是记忆库项目配置键（与 plugin-memory 同源口径）。
const (
	memoryEnabledKey     = "memory.enabled"
	memoryCategoryPrefix = "memory.category."
)

// memoryPrjConfig 读本实例的 prj config 平铺表（**data 门面** ConfigKVList(prj-config)，
// 阶段 4 第二批 / 41 G-34：原 data-prj-config-list MQ 请求改为门面 inline 调用）——与
// plugin-memory 读同一份配置（键 memory.enabled / memory.category.<类别>），保证"指引门控"
// 与"沉淀门控"一致。读失败 → nil（按未启用处置：不注入指引，保守不误导 LLM）。
func (s *Server) memoryPrjConfig(instanceID string) map[string]any {
	res, err := s.cfg.ConfigKVList(facade.ConfigKVListRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		return nil
	}
	out := make(map[string]any, len(res.List))
	for k, v := range res.List {
		out[k] = v
	}
	return out
}

// memoryCfgStr 取配置值字符串（JSON 解码后可能为字符串或布尔；其余 → 空串）。
func memoryCfgStr(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return ""
}

// memoryCategoryOn 类别开关：键缺失 → 启用（与 plugin-memory.memoryConfig.categoryEnabled 同口径）。
func memoryCategoryOn(cfg map[string]any, category string) bool {
	v, ok := cfg[memoryCategoryPrefix+category]
	if !ok {
		return true
	}
	return memoryCfgStr(v) == "true"
}

// memoryCategoryNames 取**项目级**记忆类别名（动态）：经既有 data-memory-list 面（不新增主题）
// 取「预置 ∪ 自定义」清单，仅保留 level=project（用户偏好恒单列、不入类别文件列举）。
// 命中 TTL 缓存 → 直接复用（不发 data-memory-list）；未命中才取数。取数失败 / 空清单 →
// 回落静态预置清单 persist.MemoryCategoryNames()（指引不缺失；失败/空不缓存，下次重试）。
func (s *Server) memoryCategoryNames(instanceID string) []string {
	if names, ok := s.memCache.get(instanceID); ok {
		return names
	}
	res, err := dataRequest(s.bus, msgkeys.TopicDataMemoryList, map[string]any{"instance_id": instanceID})
	if err != nil {
		return persist.MemoryCategoryNames()
	}
	raw, _ := res["list"].([]any)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		m, _ := e.(map[string]any)
		if m == nil || m["level"] != "project" {
			continue
		}
		if name, _ := m["category"].(string); name != "" {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return persist.MemoryCategoryNames()
	}
	s.memCache.put(instanceID, out)
	return out
}
