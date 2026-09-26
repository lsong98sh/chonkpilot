// `llms` 表 → Router 注册的**增量对账**（LR-10）。
//
// 边界（LR-11 前不接线）：本文件**只提供能力** —— 把「期望注册集」（llm 侧从 usr `llms` 表读出的
// `[]Spec`，含 `defaultLLM` 选出的当前 provider）对账进 Router；**不读表、不选默认、不接触 data**
// （读表 / defaultLLM 归 llm 侧，见 docs/spec/40-roadmap/40-演进计划.md §LR §2 LR-10/LR-11）。
package router

import (
	"errors"
	"fmt"
	"sort"
)

// Report 是一次对账的结果（各类 = provider 名；`Added`/`Updated` 按期望集顺序，`Removed`/
// `Unchanged` 按名称升序，便于白盒断言）。
type Report struct {
	Added     []string // 期望集有、注册表无 → 新注册
	Updated   []string // 双方都有但 Spec 有变 → 覆盖更新
	Removed   []string // 注册表有、期望集无 → 注销
	Unchanged []string // 双方都有且 Spec 逐字段相同 → 不动
	Failed    []string // 期望集内但 Spec 非法（Name / Protocol 为空）→ 未注册
}

// Reconcile 把期望注册集与当前注册表增量对账：
//
//   - 期望集有、注册表无 → `Register`（计入 Added）；
//   - 双方都有且 Spec 不同 → `Register`（幂等覆盖，计入 Updated）；
//   - 注册表有、期望集无 → `Unregister`（计入 Removed）；
//   - 逐字段相同 → 不动（计入 Unchanged，**不产生任何副作用**）。
//
// 语义细节：期望集内**同名重复**取后者（与 `Register` 的幂等覆盖一致）；Spec 非法（Name /
// Protocol 为空）**不注册**并计入 `Failed`（不静默跳过）；返回 error = 各失败项的分类错误
// （`errors.Join`，`*Error{Kind: invalid}`），nil = 全部成功。
//
// **apiKey 只随 Spec 存于内存：本方法不落任何日志**（对齐 llm 侧 maskLLMSecrets 口径）。
func (r *Router) Reconcile(specs []Spec) (Report, error) {
	desired := make(map[string]Spec, len(specs))
	order := make([]string, 0, len(specs))
	var failures []error
	report := Report{}

	for _, spec := range specs {
		norm, err := normalizeSpec(spec)
		if err != nil {
			report.Failed = append(report.Failed, spec.Name)
			failures = append(failures, err)
			continue
		}
		if _, ok := desired[norm.Name]; !ok {
			order = append(order, norm.Name)
		}
		desired[norm.Name] = norm
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, name := range order {
		cur, ok := r.specs[name]
		switch {
		case !ok:
			r.specs[name] = desired[name]
			report.Added = append(report.Added, name)
		case cur == desired[name]:
			report.Unchanged = append(report.Unchanged, name)
		default:
			r.specs[name] = desired[name]
			report.Updated = append(report.Updated, name)
		}
	}
	var removed []string
	for name := range r.specs {
		if _, ok := desired[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	for _, name := range removed {
		delete(r.specs, name)
		report.Removed = append(report.Removed, name)
	}
	sort.Strings(report.Unchanged)

	if len(failures) > 0 {
		return report, fmt.Errorf("llm provider 对账有 %d 项失败: %w", len(failures), errors.Join(failures...))
	}
	return report, nil
}
