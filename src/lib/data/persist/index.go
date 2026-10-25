// data-index-<op> 信封层：索引排除判定（只读；2026-09-27 新增）。
//
// 判定一组 workdir 相对路径是否被 codegraph / vfts 的「索引排除规则」排除（供前端文件树灰显）。
// 逻辑在 internal/indexignored（复用 chonkpilot-ignore 单一实现）；本 handler 只做信封：
// 解析 paths → 解析实例 workdir + 单批读 prj 配置 → 判定 → 回载荷（61 §3.6）。
// **零副作用**：不写配置/状态、不触发索引；workdir 无效 / 配置不可读 → 降级（不抛错）。
package persist

import (
	"encoding/json"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/indexignored"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// handleIndex 分发 data-index-<op>（当前仅 ignored；未知动作忽略不回复）。
func (s *Service) handleIndex(op string, payload []byte) {
	if op != "ignored" {
		return
	}
	method := "data-index-" + op
	var p IndexPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.failID(method, p.ReqID, err)
		return
	}
	f := p.fields()
	workdir, _, _ := s.InstBindingFor(p.InstanceID, facade.Scope{})
	values := s.indexConfigValues(p.InstanceID)
	get := func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
	res := indexignored.Query(workdir, []string(f.Paths), get, s.Warnf)
	result := map[string]any{
		msgkeys.FieldCodegraph: engineIgnoredResult(res.Codegraph),
		msgkeys.FieldVfts:      engineIgnoredResult(res.Vfts),
	}
	if res.Truncated {
		result[msgkeys.FieldTruncated] = true // 超限才带（缺省不出现；见 61 §3.6）
	}
	s.replyID(method, p.ReqID, result)
}

// indexConfigValues 单批读取本判定需要的 prj 配置键（一次 ConfigKVGet）；
// 失败 → nil（get 全部未命中 = 各键按缺省），并写日志。
func (s *Service) indexConfigValues(instanceID string) map[string]string {
	resp, err := s.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Keys: indexignored.ConfigKeys(),
	})
	if err != nil {
		if s.Warnf != nil {
			s.Warnf("data-index-ignored: 读 prj 配置失败（instance=%q）：%v", instanceID, err)
		}
		return nil
	}
	return resp.Values
}

// engineIgnoredResult 组装单引擎结果载荷（ignored 恒为数组，不用 null）。
func engineIgnoredResult(e indexignored.EngineResult) map[string]any {
	ignored := make([]any, 0, len(e.Ignored))
	for _, p := range e.Ignored {
		ignored = append(ignored, p)
	}
	// "enabled"/"ignored" 为嵌套数组键（非契约顶层字段，保留字面量）。
	return map[string]any{"enabled": e.Enabled, "ignored": ignored}
}
