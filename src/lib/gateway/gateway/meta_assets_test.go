// RB-4 白盒（②③④）：资产检索面统一（节点原语 ∪ 注册资产）+ mcp_load 实时读取 + type 收敛。
//
// 覆盖（RB-4 验收列）：
//   - 「知识库资产可被 find 命中」：dir 节点（知识库原语）的 prompt/resource 可经 mcp_find 命中
//     （此前只查 registry 段 → 检索不到）；
//   - 「改文件后 load 取到新内容」：mcp_load 对节点原语**实时**读取（与 prompts/get 同语义）；
//   - 「未知 type 报错」：mcp_find type 收敛后非合法取值 → 明确报错（不再静默空结果）。
package mcpgateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// kbScanner 是 ContractScanner 测试替身：提供**文件背书的** skill + resource 原语
// （模拟知识库：内容不驻留，handler 每次读盘）。
// 25 §5/T2：prompt 已移出 LLM 检索面（mcp_find/mcp_load 不受理）→ 此处原语用 skill 承载。
type kbScanner struct {
	skillName string
	skillFile string
	resName   string
	resURI    string
	resFile   string
	mimeType  string
}

func (s kbScanner) Scan(root string) (*mcp.Server, error) {
	ms := mcp.NewServer(&mcp.Implementation{Name: "kb", Version: "1.0.0"}, nil)
	if s.skillName != "" {
		p := &mcp.Prompt{Name: s.skillName, Description: "知识库技能", Meta: mcp.Meta{"type": "skill"}}
		file := s.skillFile
		ms.AddPrompt(p, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			b, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}
			return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: string(b)}},
			}}, nil
		})
	}
	if s.resName != "" {
		r := &mcp.Resource{URI: s.resURI, Name: s.resName, Description: "知识库资源", MIMEType: s.mimeType}
		file := s.resFile
		ms.AddResource(r, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			b, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}
			uri := s.resURI
			if req != nil && req.Params != nil && req.Params.URI != "" {
				uri = req.Params.URI
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: uri, MIMEType: s.mimeType, Text: string(b)},
			}}, nil
		})
	}
	return ms, nil
}

// newKBGateway 建一个带「知识库 dir 节点」的 gateway（不经总线，直接调用 handler 白盒断言）。
// skillName/skillContent = 知识库 skill 原语（文件背书）；resName/resURI/resContent = resource 原语。
func newKBGateway(t *testing.T, skillName, skillContent, resName, resURI, resContent string) (*Gateway, string) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	root := t.TempDir()
	pf := ""
	if skillName != "" {
		pf = filepath.Join(root, skillName+".txt")
		if err := os.WriteFile(pf, []byte(skillContent), 0o644); err != nil {
			t.Fatalf("write skill file: %v", err)
		}
	}
	rf := ""
	if resName != "" {
		rf = filepath.Join(root, resName+".txt")
		if err := os.WriteFile(rf, []byte(resContent), 0o644); err != nil {
			t.Fatalf("write resource file: %v", err)
		}
	}
	sc := kbScanner{skillName: skillName, skillFile: pf, resName: resName, resURI: resURI, resFile: rf, mimeType: "text/plain"}
	g, err := New(Params{Bus: bus, ContractScanner: sc, Logf: t.Logf})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })
	if err := g.registerDirNode("kb", root, "ins-kb"); err != nil {
		t.Fatalf("registerDirNode: %v", err)
	}
	return g, root
}

// instCtx 构造带 instance 归属的执行 ctx（find/load 可见性判定用）。
func instCtx(id string) context.Context {
	return withTurnContext(context.Background(), Context{InstanceID: id})
}

// findNames 取 mcp_find 结果的 name 列表（解析 content 文本）。
func findNames(t *testing.T, res *mcp.CallToolResult) []string {
	t.Helper()
	if res.IsError {
		t.Fatalf("mcp_find 报错: %v", res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content 类型 = %T", res.Content[0])
	}
	var out struct {
		Tools []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("解析 find 结果: %v (%s)", err, tc.Text)
	}
	names := make([]string, 0, len(out.Tools))
	for _, x := range out.Tools {
		names = append(names, x.Kind+":"+x.Name)
	}
	return names
}

// TestMCPFindHitsNodePrimitiveAssets：知识库原语（dir 节点 skill/resource）可被 mcp_find 命中
// ——RB-4 ②「find/load 资产视图 = 节点原语 ∪ 注册资产」；25 §5/T2：prompt 类原语**不**被命中。
func TestMCPFindHitsNodePrimitiveAssets(t *testing.T) {
	g, _ := newKBGateway(t, "kb_skill", "v1", "kb_res", "file://kb_res", "r1")

	res, err := g.handleMCPFind(instCtx("ins-kb"), map[string]any{"type": "skill", "query": "kb"})
	if err != nil {
		t.Fatalf("handleMCPFind: %v", err)
	}
	got := findNames(t, res)
	if len(got) != 1 || got[0] != "skill:kb_skill" {
		t.Fatalf("type=skill 应命中知识库原语：%v", got)
	}

	res, err = g.handleMCPFind(instCtx("ins-kb"), map[string]any{"type": "resource", "query": "kb"})
	if err != nil {
		t.Fatalf("handleMCPFind(resource): %v", err)
	}
	got = findNames(t, res)
	if len(got) != 1 || got[0] != "resource:kb_res" {
		t.Fatalf("type=resource 应命中知识库原语：%v", got)
	}

	// 25 §5/T2：type=prompt → 明确报错（prompt 移出 LLM 检索面，知识库 prompt 不可被 LLM 检索）
	res, err = g.handleMCPFind(instCtx("ins-kb"), map[string]any{"type": "prompt", "query": "kb"})
	if err != nil {
		t.Fatalf("handleMCPFind(prompt): %v", err)
	}
	if !res.IsError {
		t.Fatalf("type=prompt 应报错（unknown type），got %+v", res.Content)
	}

	// 跨 instance 不可见（原语按归属域过滤）
	res, err = g.handleMCPFind(instCtx("ins-other"), map[string]any{"type": "skill", "query": "kb"})
	if err != nil {
		t.Fatalf("handleMCPFind(other): %v", err)
	}
	if got = findNames(t, res); len(got) != 0 {
		t.Fatalf("跨 instance 不应命中 scoped 原语：%v", got)
	}
}

// TestMCPFindUnknownTypeErrors：type 收敛后未知取值**明确报错**（RB-4 ④；25 §5/T2：prompt/agent
// 亦属未知）。
func TestMCPFindUnknownTypeErrors(t *testing.T) {
	g, _ := newKBGateway(t, "kb_skill", "v1", "", "", "")
	for _, typ := range []string{"kb", "knowledge", "codebase", "file", "category", "assets", "*", "toolz", "prompt", "agent"} {
		res, err := g.handleMCPFind(instCtx("ins-kb"), map[string]any{"type": typ, "query": "kb"})
		if err != nil {
			t.Fatalf("handleMCPFind(%q): %v", typ, err)
		}
		if !res.IsError {
			t.Fatalf("type=%q 应明确报错，got %+v", typ, res.Content)
		}
		tc, _ := res.Content[0].(*mcp.TextContent)
		if tc == nil || !strings.Contains(tc.Text, "unknown type") {
			t.Fatalf("type=%q 错误消息应含 unknown type: %+v", typ, res.Content)
		}
	}
	// 合法取值不报错（25 §5/T2：tool|skill|resource|all）
	for _, typ := range []string{"", "all", "tool", "skill", "resource"} {
		res, err := g.handleMCPFind(instCtx("ins-kb"), map[string]any{"type": typ})
		if err != nil {
			t.Fatalf("handleMCPFind(%q): %v", typ, err)
		}
		if res.IsError {
			t.Fatalf("type=%q 不应报错: %+v", typ, res.Content)
		}
	}
}

// TestMCPLoadNodeAssetReadsLive：mcp_load 对节点原语**实时**读取（改文件后取到新内容）
// ——RB-4 ③「mcp_load 与 prompts/get 同语义」；25 §5/T2：受理 kind = skill / resource。
func TestMCPLoadNodeAssetReadsLive(t *testing.T) {
	g, root := newKBGateway(t, "kb_skill", "v1", "kb_res", "file://kb_res", "r1")

	load := func(kind, name string) string {
		t.Helper()
		res, err := g.handleMCPLoad(instCtx("ins-kb"), map[string]any{"kind": kind, "name": name})
		if err != nil {
			t.Fatalf("handleMCPLoad(%s/%s): %v", kind, name, err)
		}
		if res.IsError {
			t.Fatalf("handleMCPLoad(%s/%s) 报错: %+v", kind, name, res.Content)
		}
		tc, ok := res.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("content 类型 = %T", res.Content[0])
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &m); err != nil {
			t.Fatalf("解析 load 结果: %v (%s)", err, tc.Text)
		}
		s, _ := m["content"].(string)
		return s
	}

	if got := load("skill", "kb_skill"); got != "v1" {
		t.Fatalf("首次 load = %q, want v1", got)
	}
	if err := os.WriteFile(filepath.Join(root, "kb_skill.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got := load("skill", "kb_skill"); got != "v2" {
		t.Fatalf("改文件后 load 应取到新内容（实时读取），got %q", got)
	}
	if got := load("resource", "kb_res"); got != "r1" {
		t.Fatalf("resource load = %q, want r1", got)
	}
	if err := os.WriteFile(filepath.Join(root, "kb_res.txt"), []byte("r2"), 0o644); err != nil {
		t.Fatalf("rewrite res: %v", err)
	}
	if got := load("resource", "kb_res"); got != "r2" {
		t.Fatalf("resource 改文件后应取到新内容，got %q", got)
	}
	// 25 §5/T2：kind=prompt 已移出 LLM 检索面 → 明确报错（不静默返回空）
	res, err := g.handleMCPLoad(instCtx("ins-kb"), map[string]any{"kind": "prompt", "name": "kb_skill"})
	if err != nil {
		t.Fatalf("handleMCPLoad(prompt): %v", err)
	}
	if !res.IsError {
		t.Fatalf("kind=prompt 应报错（unknown kind），got %+v", res.Content)
	}
}

// TestRegisteredAssetPathReadsLive：RB-4 ①（2026-09-22）—— 注册资产带 `path` = 内容不驻留 + 实时读盘。
// 覆盖验收：① 改文件后 mcp_load 取到新内容；② 无 path 的内嵌资产携 content → mcp_load 不回归；
// ③ registry 内不常驻「有 path 资产」的内容（catalogAsset.Content 为空）。
// 25 §5/T2：资产 kind 由 agent 改为 **skill**（agent 已撤出资产面），且 prompts/get 只服务节点原语。
func TestRegisteredAssetPathReadsLive(t *testing.T) {
	g, _ := newKBGateway(t, "kb_skill", "节点技能内容", "", "", "")
	dir := t.TempDir()
	skillFile := filepath.Join(dir, "coder.skill.md")
	if err := os.WriteFile(skillFile, []byte("你是编码技能 v1"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	register := func(payload map[string]any) error {
		t.Helper()
		b, _ := json.Marshal(payload)
		return g.handleRegByMethod("prompts/register", &mq.Value{Payload: b})
	}
	// ①/③：带 path 的 skill 资产
	if err := register(map[string]any{"name": "coder", "description": "编码技能", "asset_kind": "skill", "path": skillFile}); err != nil {
		t.Fatalf("prompts/register(skill/path): %v", err)
	}
	// ②：无 path 的内嵌 skill（携 content，走兼容兜底）
	if err := register(map[string]any{"name": "embed-skill", "description": "内嵌技能", "asset_kind": "skill", "content": "内嵌内容"}); err != nil {
		t.Fatalf("prompts/register(内嵌 skill): %v", err)
	}
	// 25 §5/T2：asset_kind=agent 不再受理（agent 已撤出资产面）
	if err := register(map[string]any{"name": "coder-agent", "asset_kind": "agent", "content": "x"}); err == nil {
		t.Fatal("asset_kind=agent 应被拒绝（25 §5/T2：agent 已撤出资产面）")
	}

	// ③ registry 内不驻留「有 path 资产」的内容
	a, ok := g.reg.getAsset(KindSkill, "coder")
	if !ok || a.Path != skillFile || a.Content != "" {
		t.Fatalf("带 path 资产不应驻留 content: %+v ok=%v", a, ok)
	}

	loadSkill := func(name string) string {
		t.Helper()
		res, err := g.handleMCPLoad(instCtx("ins-kb"), map[string]any{"kind": "skill", "name": name})
		if err != nil || res.IsError {
			t.Fatalf("mcp_load(skill=%s): err=%v res=%+v", name, err, res)
		}
		tc, _ := res.Content[0].(*mcp.TextContent)
		if tc == nil {
			t.Fatalf("content 类型 = %T", res.Content[0])
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &m); err != nil {
			t.Fatalf("解析 load: %v (%s)", err, tc.Text)
		}
		s, _ := m["content"].(string)
		return s
	}

	// ① 带 path：首次读到文件 → 改文件 → 再读**取到新内容**（实时读盘）
	if got := loadSkill("coder"); got != "你是编码技能 v1" {
		t.Fatalf("mcp_load 首次 = %q, want v1", got)
	}
	if err := os.WriteFile(skillFile, []byte("你是编码技能 v2"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got := loadSkill("coder"); got != "你是编码技能 v2" {
		t.Fatalf("改文件后 mcp_load 应取新内容（实时读盘），got %q", got)
	}
	// ② 无 path：mcp_load 取驻留 content（不回归）
	if got := loadSkill("embed-skill"); got != "内嵌内容" {
		t.Fatalf("无 path 内嵌资产的 mcp_load 不回归：got %q", got)
	}

	// prompts/get 只服务**节点原语**（25 §5/T2：agent 资产分支已撤）→ 命中 skill 原语
	v := &mq.Value{Payload: jbReg(map[string]any{"name": "kb_skill", "instance_id": "ins-kb"})}
	if err := g.handlePromptsGet(v); err != nil {
		t.Fatalf("prompts/get(kb_skill): %v", err)
	}
	m, _ := v.Result.(map[string]any)
	if s, _ := m["content"].(string); s != "节点技能内容" {
		t.Fatalf("prompts/get(节点原语) = %q，want 节点技能内容", s)
	}
}

// jbReg 构造 register/get 载荷 JSON（测试内联）。
func jbReg(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
