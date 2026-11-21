// graph_test.go —— 近似图召回回归（近重复语料下的离群点可达性）。
//
// 背景（2026-11-21，e2e S-SESS-02 满载复现）：mock embedding 只有 3 种取值，全量索引语料
// 235 条消息中绝大多数向量完全相同；旧剪枝按「离当前节点最远」淘汰，少数**离群但真实命中**
// 的向量被重复簇反复挤出，且反向边同步删除 → 图中不可达 → 无过滤 semantic 检索（approx=true）
// 的 top-10 永远不含目标会话。剪枝改为 diversity（淘汰与其它邻居最近的冗余节点）后恢复可达。
package hnswlite

import (
	"fmt"
	"testing"
)

func TestSearchFindsOutlierClusterInDuplicateCorpus(t *testing.T) {
	g := NewGraph[string]()
	g.EfSearch = 64
	crowd := []float32{0, 0, 1, 0}  // 查询与目标正交（距离 1）
	target := []float32{1, 0, 0, 0} // 目标簇（距离 0，必须进 top-k）
	for i := 0; i < 233; i++ {
		g.Add(MakeNode(fmt.Sprintf("c%03d", i), crowd))
	}
	// 目标在语料末尾加入（真实全量索引按对话/消息顺序扫描，新会话的最后写入）。
	g.Add(MakeNode("t1", target))
	g.Add(MakeNode("t2", target))

	res := g.Search(target, 10)
	found := map[string]bool{}
	keys := make([]string, 0, len(res))
	for _, r := range res {
		found[r.Key] = true
		keys = append(keys, r.Key)
	}
	if !found["t1"] || !found["t2"] {
		t.Fatalf("离群目标不可达（剪枝/搜索终止过早）: top10=%v", keys)
	}
}
