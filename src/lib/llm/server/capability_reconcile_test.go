// capability 热重扫「去窗口」集成回归（P3）：新增一个根时，未变化节点的工具**全程不消失**。
//
// 旧实现先注销全部节点再重建 → 未变化节点也离开工具面（core-mcp spawn/握手窗口）；
// 新实现只注册新增节点 → 未变化工具面零窗口。
package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

func TestCapabilityReconcileKeepsUnchangedToolFace(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)

	userRoot := persist.CapUserRoot(s.opts.UsrPath)
	writeHotToolContract(t, userRoot, "user_keep")
	registerTestInstance(t, s) // 经总线注册（s.im / persist 实例视图同源；reconcile 从 s.im 取实例）
	userTool := "ins-test-user_user_keep"
	if _, ok := waitTool(t, s, userTool, true, 5*time.Second); !ok {
		t.Fatalf("用户级工具未接入: %s", userTool)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	var missing atomic.Int32
	present := make(chan struct{}, 1)
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			defs, err := s.gc.ListTools(context.Background())
			if err == nil {
				if countTool(defs, userTool) == 0 {
					missing.Add(1)
				}
				if countTool(defs, "ins-test-project_prj_new") > 0 {
					select {
					case present <- struct{}{}:
					default:
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// 新增 project 根（user 节点未变化）→ 重扫只应注册新节点。
	writeHotToolContract(t, persist.CapProjectRoot(testWorkDir), "prj_new")
	s.reconcileCapabilityNodes()

	select {
	case <-present:
	case <-time.After(10 * time.Second):
		close(stop)
		<-done
		t.Fatal("新增 project 节点未接入工具面")
	}
	close(stop)
	<-done
	if n := missing.Load(); n != 0 {
		t.Fatalf("热重扫期间未变化工具消失 %d 次（存在工具面窗口）", n)
	}
}
