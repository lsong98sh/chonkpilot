// manage.go：vfts 管理面（UI ↔ 插件，点分相对主题，同主题 promise 写回 v.Result）。
//
//   - vfts.dict.get / vfts.dict.set：**系统级** jieba 词典查看/编辑（直调引擎
//     vfts_dict_get / vfts_dict_set；插件不自行落盘，词典落点唯一由引擎持有）；
//   - vfts.reindex：触发**全量重建**（分词器/词典/自定义词变更后必须重建才生效）。
//
// 分词器固定 jieba（见引擎 workspace.go tokenizer）；词典变更 → 旧索引分词结果失效，
// 故 dict.set 成功后自动对待重建的 workdir 调度一次强制重建（去抖合并）。
package vfts

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// 管理面主题（相对名；chonk. 前缀由总线注入）。前端 type 与总线相对主题同名（点分直通）。
const (
	topicDictGet = msgkeys.TopicVftsDictGet
	topicDictSet = msgkeys.TopicVftsDictSet
	topicReindex = msgkeys.TopicVftsReindex
)

// onDictGet 查看系统级词典：直调引擎 vfts_dict_get，把应答原样写回 v.Result。
func (p *Vfts) onDictGet(_ context.Context, _ string, v *mq.Value) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	text, err := p.callEngine(ctx, "vfts_dict_get", map[string]any{})
	if err != nil {
		v.Result = map[string]any{"ok": false, "error": err.Error()}
		return nil
	}
	var res map[string]any
	if json.Unmarshal([]byte(text), &res) != nil {
		v.Result = map[string]any{"ok": false, "error": "vfts_dict_get 应答解析失败"}
		return nil
	}
	v.Result = res
	return nil
}

// onDictSet 编辑系统级自定义词：直调引擎 vfts_dict_set 落盘；成功后调度强制重建
// （系统级词典 → 对**全部启用中的 workdir** 生效），立即回执（重建在后台）。
func (p *Vfts) onDictSet(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		UserDict string `json:"user_dict"`
	}
	if json.Unmarshal(v.Payload, &req) != nil {
		v.Result = map[string]any{"ok": false, "error": "载荷解析失败（需 {user_dict}）"}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	text, err := p.callEngine(ctx, "vfts_dict_set", map[string]any{"user_dict": req.UserDict})
	if err != nil {
		v.Result = map[string]any{"ok": false, "error": err.Error()}
		return nil
	}
	var res map[string]any
	if json.Unmarshal([]byte(text), &res) != nil {
		res = map[string]any{"ok": true}
	}
	rebuilding := p.scheduleRebuildAll("系统级词典变更")
	p.logf("vfts: 系统级自定义词已更新 → 调度重建 workdir %v", rebuilding)
	v.Result = res
	return nil
}

// onReindex 手动触发全量重建（配置页「重新索引」按钮）：解析调用实例所属 workdir（缺省 =
// 全部启用中的 workdir），后台强制重建 → 立即回执 started。
func (p *Vfts) onReindex(_ context.Context, _ string, v *mq.Value) error {
	wd := p.workdirOfInstance(v.Payload)
	var rebuilding []string
	if wd != "" {
		rebuilding = p.scheduleRebuild([]string{wd}, "手动重新索引")
	} else {
		rebuilding = p.scheduleRebuildAll("手动重新索引")
	}
	p.logf("vfts: 手动重新索引 → 调度重建 workdir %v", rebuilding)
	v.Result = map[string]any{"ok": true, "started": true}
	return nil
}

// scheduleRebuildAll 对全部「引用中且已启用」的 workdir 调度一次强制重建（返回排序后的清单）。
func (p *Vfts) scheduleRebuildAll(reason string) []string {
	p.mu.Lock()
	var wds []string
	for wd, r := range p.works {
		if r.refs > 0 && r.enabled {
			wds = append(wds, wd)
		}
	}
	p.mu.Unlock()
	return p.scheduleRebuild(wds, reason)
}

// scheduleRebuild 对给定 workdir 清单调度强制重建（去抖合并，后台执行）。
func (p *Vfts) scheduleRebuild(wds []string, reason string) []string {
	out := append([]string(nil), wds...)
	sort.Strings(out)
	for _, wd := range out {
		wd := wd
		r := reason
		p.rebuild.schedule(wd, func() {
			p.logf("vfts: %s → workdir %s 强制重建索引", r, wd)
			p.ensureWorkspace(wd, true)
		})
	}
	return out
}

// workdirOfInstance 从载荷（mq 已注入 instance_id）解析该实例所属 workdir；无/未登记 → 空串。
func (p *Vfts) workdirOfInstance(payload []byte) string {
	var req struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &req) != nil || req.InstanceID == "" {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if ir, ok := p.insts[req.InstanceID]; ok {
		return ir.workdir
	}
	return ""
}

// callEngine 调引擎工具：默认走共享子进程 client；测试可注入 engineCallFn 替换。
func (p *Vfts) callEngine(ctx context.Context, name string, args map[string]any) (string, error) {
	if p.engineCallFn != nil {
		return p.engineCallFn(ctx, name, args)
	}
	return p.engineCall(ctx, name, args)
}
