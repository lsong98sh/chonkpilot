// 实例绑定/心跳超时清理测试的共用工具（两形态共用；split 用例见 sweep_split_test.go，
// 默认构建用例见 sweep_inprocess_test.go）。
package persist

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newSweepTestService 建一个实例绑定/超时清理测试服务（进程内总线；UsrPath/AppDir 走临时目录，
// 不污染用户配置；不订阅、不启动扫描——需要时用例自行 Start）。
func newSweepTestService(t *testing.T) *Service {
	t.Helper()
	data.Reset()
	t.Cleanup(data.Reset)
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	return New(bus, Options{UsrPath: t.TempDir() + "/usr.db", AppDir: t.TempDir()})
}

// instancePayload 构造 instance-register/heartbeat/exit 契约载荷（61-消息一览 §4.1；
// Windows 路径经 json.Marshal 转义）。
func instancePayload(id, workDir, dataDir string) []byte {
	m := map[string]string{"instance_id": id}
	if workDir != "" {
		m["work_dir"] = workDir
	}
	if dataDir != "" {
		m["data_dir"] = dataDir
	}
	b, _ := json.Marshal(m)
	return b
}

// backdate 把实例的 LastBeat 拨早 d（模拟心跳停摆；生产路径由真实时间推进）。
// 实例视图本体在 internal/kernel.View（下沉后），经 TouchAt 指定时刻写入。
func backdate(t *testing.T, s *Service, id string, d time.Duration) {
	t.Helper()
	if info, ok := s.lookupInstance(id); !ok {
		t.Fatalf("实例 %s 未登记，无法拨早 LastBeat", id)
	} else if !s.View.TouchAt(id, info.LastBeat.Add(-d)) {
		t.Fatalf("实例 %s 未登记，无法拨早 LastBeat", id)
	}
}
