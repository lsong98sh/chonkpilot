// config 表原语 + 订阅面（data-<domain>-refresh）广播 + usr 配置下行事件
// （data-user-config-changed，61 §3.1）——原 persist 的同名 helper 逐字下移。
//
// 记录形态（12-数据层）：config 表一个决策一个 key，记录 {"v": <值字符串>}。
package kernel

import (
	"context"
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// ConfigGet 读 config 表 key 的 v 字段值（不存在 → ""，不报错）。
func ConfigGet(db *data.DB, key string) string {
	var rec data.Record
	ok, err := db.Table("config").Get(key, &rec)
	if err != nil || !ok {
		return ""
	}
	return Sval(rec["v"])
}

// ConfigSet 写 config 表 key：记录整体为 {"v": val}。
func ConfigSet(db *data.DB, key, val string) error {
	return db.Table("config").Upsert(key, data.Record{"v": val})
}

// ConfigDelete 删 config 表 key（不存在视为成功）。
func ConfigDelete(db *data.DB, key string) {
	_ = db.Table("config").Delete(key)
}

// PrjConfigVal 读单值 config 键（active_session_id 等；对齐桥 configSet {v:...} 记录）。
func PrjConfigVal(db *data.DB, key string) (string, error) {
	var rec data.Record
	ok, err := db.Table("config").Get(key, &rec)
	if err != nil || !ok {
		return "", err
	}
	return Sval(rec["v"]), nil
}

// PrjConfigSetVal 写单值 config 键。
func PrjConfigSetVal(db *data.DB, key, val string) error {
	return db.Table("config").Upsert(key, data.Record{"v": val})
}

// Refresh save/delete 后广播 data-<domain>-refresh：{instance_id, id, op} + 最新 {list}
// （所有客户端刷新）。user-config 域额外**兼容广播 config-refresh**（I-42：前端 ChatPanel
// 据此刷新 LLM 列表，此前全仓无发布方）；payload 必带 instance_id（61 §0 硬规则；无归属取空串）。
// scope 供跨进程/独立形态带入实例数据根（MQ 路径留空 = 按本进程实例视图解析）。
func (b *Base) Refresh(domain, instanceID, id, op string) {
	b.RefreshScoped(domain, instanceID, id, op, facade.Scope{})
}

// UserConfigChanged 广播 usr 配置**下行事件** data-user-config-changed（61 §3.1）：由 config
// 域在 data-user-config-save / -delete **成功后**发出（任何绑定下同源，见 config/service.go）。
//
// payload = {"data": <受影响键 → 变更后的有效值>}（与 save 同形，61 §3.1），**不带 instance_id**
// —— 61 §3.1 明载「不带 instance_id → 全局」：每个窗口的桥各自转发 → **所有窗口都收到**（发起
// 窗口重复应用幂等）。用途 = 多窗口下 theme/locale **即时同步**（24 §6.5 · WIN-021）；与
// data-<domain>-refresh 家族的区别正在于此（后者带 instance_id → 只到来源窗口）。
func (b *Base) UserConfigChanged(changed map[string]any) {
	if b == nil || b.Bus == nil || len(changed) == 0 {
		return // 无总线（测试/无订阅场景）或空变更：无接收方/无内容，静默跳过
	}
	raw, _ := json.Marshal(map[string]any{"data": changed})
	_ = b.Bus.Emit(context.Background(), msgkeys.TopicDataUserConfigChanged, raw)
}

// RefreshScoped 同 Refresh，但把调用方数据根（Scope）带给 list 解析（门面 inline 绑定路径）。
func (b *Base) RefreshScoped(domain, instanceID, id, op string, scope facade.Scope) {
	b.RefreshScopedKeys(domain, instanceID, []string{id}, op, scope)
}

// RefreshScopedKeys 同 RefreshScoped，但**一次广播覆盖一组键**（批量写：N 键只发 1 条，
// 61 §3.1）——载荷在既有 `{instance_id, id, op, list?}` 上**新增可选 `ids`（全组键，稳定序）**，
// `id` = 首键（向后兼容既有单键订阅方）。单键（len(ids)==1）**不写 `ids`** → 载荷与改前
// 逐字节等价（零影响既有订阅方）。
//
// 投递范围（G-41-b）：`prj-config` 为**项目共享配置**，需通知**同 work_dir 的全部在册实例**
// （多数 MQ 按 instance 过滤 → 逐个带目标 id 下发，不漏同项目其它实例）；其余域保持来源实例单播。
func (b *Base) RefreshScopedKeys(domain, instanceID string, ids []string, op string, scope facade.Scope) {
	if b.Bus == nil || len(ids) == 0 {
		return // 无总线（测试/无订阅场景）或空键集：变更广播无接收方/无内容，静默跳过
	}
	var list any
	if b.DomainList != nil {
		list = b.DomainList(domain, instanceID, scope) // 同 work_dir 各实例同源 → 计算一次复用
	}
	for _, target := range b.refreshTargets(domain, instanceID) {
		b.emitRefresh(domain, target, ids, op, list)
	}
}

// refreshTargets 计算一次 refresh 广播的投递实例集合（G-41-b）：
//   - `prj-config`（项目共享配置）：按来源实例 work_dir 匹配**同项目全部在册实例**逐个投递；
//   - 其余域（usr 全局 / 会话 / 快照等按实例隔离）：保持来源实例单播（行为不变）。
//
// 来源实例未登记 / work_dir 解析不出 → 回落 `[instanceID]`（与改前逐字节等价）。
func (b *Base) refreshTargets(domain, instanceID string) []string {
	if domain == facade.DomainPrjConfig {
		if wd, _, ok := data.BindOf(instanceID); ok && wd != "" {
			if ids := data.InstancesByWorkDir(wd); len(ids) > 0 {
				return ids
			}
		}
	}
	return []string{instanceID}
}

// emitRefresh 按 **单个目标实例** 发出一次 `data-<domain>-refresh`（载荷形状不变：
// `{instance_id, id, ids?, op, list?}`；`list` 由调用方一次解析、跨目标复用）。
// user-config 域额外兼容广播 `config-refresh`（I-42；单播 → 恒 1 条，行为不变）。
func (b *Base) emitRefresh(domain, instanceID string, ids []string, op string, list any) {
	payload := map[string]any{"id": ids[0], "op": op}
	if len(ids) > 1 {
		payload["ids"] = append([]string(nil), ids...) // 批量才带（单键与改前完全一致）
	}
	if instanceID != "" {
		payload["instance_id"] = instanceID
	}
	if list != nil {
		payload["list"] = list
	}
	b2, _ := json.Marshal(payload)
	_ = b.Bus.Emit(context.Background(), "data-"+domain+"-refresh", b2)
	if domain == "user-config" {
		cf := map[string]any{
			"instance_id": instanceID, // 无归属 → 空串（字段必带，61 §0）
			"id":          ids[0],     // 兼容广播恒单键（user-config 恒单 id，不受批量影响）
			"op":          op,
		}
		if list != nil {
			cf["list"] = list
		}
		cb, _ := json.Marshal(cf)
		_ = b.Bus.Emit(context.Background(), msgkeys.TopicConfigRefresh, cb)
	}
}
