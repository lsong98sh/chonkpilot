// LR-10 白盒：`llms` 表 → Router 注册的增量对账（reconcile.go）——新增 / 覆盖更新 / 移除 /
// 未变更不动 + 失败项显式登记。
package router

import (
	"errors"
	"testing"
)

// testSpec 构造一个合法 Spec（BaseURL / APIKey 非空，以便验证「未变更不动」的逐字段比较）。
func testSpec(name, protocol, model, key string) Spec {
	return Spec{Name: name, Protocol: protocol, BaseURL: "https://" + name + ".example/v1", APIKey: key, DefaultModel: model}
}

// TestReconcileInitialAdd：空注册表 → 全部新增（按期望集顺序）；注册表内容与期望集一致。
func TestReconcileInitialAdd(t *testing.T) {
	r := New(Config{})
	rep, err := r.Reconcile([]Spec{
		testSpec("a", ProtocolOpenAI, "m-a", "sk-a"),
		testSpec("b", " Anthropic ", "m-b", "ak-b"), // 协议名归一
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.Added) != 2 || rep.Added[0] != "a" || rep.Added[1] != "b" {
		t.Fatalf("Added=%v want [a b]", rep.Added)
	}
	if len(rep.Updated)+len(rep.Removed)+len(rep.Unchanged)+len(rep.Failed) != 0 {
		t.Fatalf("其余分类应为空：%+v", rep)
	}
	if len(r.specs) != 2 || r.specs["b"].Protocol != ProtocolAnthropic || r.specs["a"].APIKey != "sk-a" {
		t.Fatalf("注册表内容=%+v", r.specs)
	}
	// 适配器表不受对账影响（能力矩阵仍来自内置适配器）。
	if caps := r.Capabilities("a"); !caps.Stream || !caps.Tools {
		t.Fatalf("对账后 Capabilities(a)=%+v want 内置 openai 矩阵", caps)
	}
}

// TestReconcileIncremental：一次对账覆盖四类增量 —— 新增 / 覆盖更新 / 移除 / 未变更不动。
func TestReconcileIncremental(t *testing.T) {
	r := New(Config{})
	base := []Spec{
		testSpec("a", ProtocolOpenAI, "m-a", "sk-a"),
		testSpec("b", ProtocolOpenAI, "m-b", "sk-b"),
		testSpec("c", ProtocolAnthropic, "m-c", "ak-c"),
	}
	if _, err := r.Reconcile(base); err != nil {
		t.Fatalf("首次 Reconcile: %v", err)
	}

	// a 未变 / b 改 model / c 移除 / d 新增。
	desired := []Spec{
		testSpec("a", ProtocolOpenAI, "m-a", "sk-a"),
		testSpec("b", ProtocolOpenAI, "m-b2", "sk-b"),
		testSpec("d", ProtocolOpenAI, "m-d", "sk-d"),
	}
	rep, err := r.Reconcile(desired)
	if err != nil {
		t.Fatalf("增量 Reconcile: %v", err)
	}
	if len(rep.Unchanged) != 1 || rep.Unchanged[0] != "a" {
		t.Fatalf("Unchanged=%v want [a]", rep.Unchanged)
	}
	if len(rep.Updated) != 1 || rep.Updated[0] != "b" {
		t.Fatalf("Updated=%v want [b]", rep.Updated)
	}
	if len(rep.Added) != 1 || rep.Added[0] != "d" {
		t.Fatalf("Added=%v want [d]", rep.Added)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != "c" {
		t.Fatalf("Removed=%v want [c]", rep.Removed)
	}
	if len(r.specs) != 3 || r.specs["b"].DefaultModel != "m-b2" {
		t.Fatalf("对账后注册表=%+v", r.specs)
	}
	if _, ok := r.specs["c"]; ok {
		t.Fatal("移除项 c 仍注册")
	}
	// 幂等：同一期望集再对账一次 → 全部未变更、无副作用。
	rep2, err := r.Reconcile(desired)
	if err != nil {
		t.Fatalf("重复 Reconcile: %v", err)
	}
	if len(rep2.Unchanged) != 3 || len(rep2.Added)+len(rep2.Updated)+len(rep2.Removed) != 0 {
		t.Fatalf("第二次对账=%+v want 全 Unchanged", rep2)
	}
}

// TestReconcileRemovesAllWhenEmpty：期望集为空 → 现有 provider 全部注销。
func TestReconcileRemovesAllWhenEmpty(t *testing.T) {
	r := New(Config{})
	if _, err := r.Reconcile([]Spec{testSpec("a", ProtocolOpenAI, "m", "k")}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rep, err := r.Reconcile(nil)
	if err != nil {
		t.Fatalf("Reconcile(nil): %v", err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != "a" || len(r.specs) != 0 {
		t.Fatalf("rep=%+v specs=%+v", rep, r.specs)
	}
}

// TestReconcileFailedSpecs：非法 Spec（空 Name / 空 Protocol）不注册、计入 Failed、返回分类错误；
// 同一批中的合法项仍照常生效（不因一项失败而整批失效，也不静默跳过）。
func TestReconcileFailedSpecs(t *testing.T) {
	r := New(Config{})
	rep, err := r.Reconcile([]Spec{
		testSpec("a", ProtocolOpenAI, "m", "k"),
		{Name: "  ", Protocol: ProtocolOpenAI},
		{Name: "c"},
	})
	if err == nil {
		t.Fatal("存在非法 Spec 时应返回错误")
	}
	var e *Error
	if !errors.As(err, &e) || e.Kind != ErrorInvalid {
		t.Fatalf("err=%v want 包住 *Error{invalid}", err)
	}
	if len(rep.Failed) != 2 || len(rep.Added) != 1 || rep.Added[0] != "a" {
		t.Fatalf("rep=%+v want Failed 2 / Added [a]", rep)
	}
	if len(r.specs) != 1 {
		t.Fatalf("注册表=%+v want 仅合法项", r.specs)
	}
}

// TestReconcileDuplicateNamesLastWins：期望集内同名重复 → 后者为准（与 Register 幂等覆盖一致），
// 且只登记一次。
func TestReconcileDuplicateNamesLastWins(t *testing.T) {
	r := New(Config{})
	rep, err := r.Reconcile([]Spec{
		testSpec("a", ProtocolOpenAI, "旧", "k1"),
		testSpec("a", ProtocolOpenAI, "新", "k2"),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.Added) != 1 || rep.Added[0] != "a" {
		t.Fatalf("Added=%v want [a]（同名只登记一次）", rep.Added)
	}
	if got := r.specs["a"].DefaultModel; got != "新" {
		t.Fatalf("同名取后者：DefaultModel=%q want 新", got)
	}
	if len(r.specs) != 1 {
		t.Fatalf("注册表条数=%d want 1", len(r.specs))
	}
}
