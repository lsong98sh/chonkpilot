package bridge

import (
	"encoding/json"
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	pftopics "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/topics"
)

// MQ 主题控制面最小验证：register → registry.Lookup；unregister 只限自己的 owner；
// seal 返回校验结果。
func TestPublishEventTopicControl(t *testing.T) {
	reg := pftopics.NewRegistry()
	bus, err := mq.New(mq.Options{Prefix: "chonk.", Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	br := New("ins-topic", t.TempDir(), "", func(string) {}, bus)

	regPayload, _ := json.Marshal(map[string]any{
		"topic":       "plugin.test.event",
		"scope":       "instance",
		"instance_id": "ins-topic",
		"schema":      map[string]any{"type": "object"},
	})
	if _, errs := br.PublishEvent(pftopics.TopicRegister, string(regPayload)); len(errs) != 0 {
		t.Fatalf("topic-register errors = %v", errs)
	}
	e, ok := reg.Lookup("plugin.test.event")
	if !ok || e.Owner != "frontend:ins-topic" || e.Scope != pftopics.ScopeInstance {
		t.Fatalf("Lookup = %+v, %v", e, ok)
	}

	// 其他 instance 不能反注册。
	otherPayload, _ := json.Marshal(map[string]any{"topic": "plugin.test.event", "instance_id": "other"})
	if _, errs := br.PublishEvent(pftopics.TopicUnregister, string(otherPayload)); len(errs) == 0 {
		t.Fatal("非 owner 反注册应失败")
	}
	if _, ok := reg.Lookup("plugin.test.event"); !ok {
		t.Fatal("失败的反注册不应删除主题")
	}

	// seal：无订阅 → ok=true。
	res, errs := br.PublishEvent(pftopics.TopicSeal, "{}")
	if len(errs) != 0 {
		t.Fatalf("topic-seal errors = %v", errs)
	}
	m, _ := res.(map[string]any)
	if ok, _ := m["ok"].(bool); !ok {
		t.Fatalf("seal result = %v", res)
	}

	// 正确 owner 反注册成功。
	if _, errs := br.PublishEvent(pftopics.TopicUnregister, string(regPayload)); len(errs) != 0 {
		t.Fatalf("topic-unregister errors = %v", errs)
	}
	if _, ok := reg.Lookup("plugin.test.event"); ok {
		t.Fatal("反注册后仍可查到")
	}
}

// 总线未注入 registry → 明确 error，不 panic。
func TestPublishEventTopicControlWithoutRegistry(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	br := New("ins-topic", t.TempDir(), "", func(string) {}, bus)
	if _, errs := br.PublishEvent(pftopics.TopicRegister, `{"topic":"x","scope":"global"}`); len(errs) == 0 {
		t.Fatal("未装配注册表应返回 error")
	}
}