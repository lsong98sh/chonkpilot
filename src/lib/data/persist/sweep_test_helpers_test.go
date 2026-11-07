// 实例绑定/心跳超时清理测试的共用工具（两形态共用；split 用例见 sweep_split_test.go，
// 默认构建用例见 sweep_inprocess_test.go）。
package persist

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// newSweepTestService 建一个实例绑定/超时清理测试服务（进程内总线；UsrPath/AppDir 走临时目录，
// 不污染用户配置；不订阅、不启动扫描——需要时用例自行 Start）。
//
// **返回 helper 创建的 workDir/dataDir**：实例登记的数据根目录须随 Service 一起生命周期管理。
// t.Cleanup 为 LIFO —— 目录清理必须**最后**执行，否则 Windows 下 SQLite/bbolt 仍打开
// `<work_dir>/.chonkpilot/.../chonkpilot.db` 时 RemoveAll 会报「file is being used by
// another process」（split 的两个用例曾因此失败）。故这里先建全部临时目录（注册目录清理），
// 再注册 Service.Stop（停扫描/退订）与 data.Reset（关闭全部缓存连接）→ 执行顺序恒为
// **先关库、再删目录**。用例若在 helper 之后自行 t.TempDir()，其清理会早于关库，务必改用
// 返回的 workDir/dataDir。
func newSweepTestService(t *testing.T) (s *Service, workDir, dataDir string) {
	t.Helper()

	// ① 目录先注册清理（LIFO 最后执行）。
	usrDir := t.TempDir()
	appDir := t.TempDir()
	workDir = t.TempDir()
	dataDir = t.TempDir()

	// ② 清前一用例残留绑定（连接已由上一用例清理关闭）。
	data.Reset()

	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	s = New(bus, Options{UsrPath: filepath.Join(usrDir, "usr.db"), AppDir: appDir})
	// ③ Service 既有 shutdown API（停扫描 + 退订 + 等待在飞 dialogs 对账）→ data.Reset
	//    （关全部缓存连接）。LIFO：本清理先于 bus.Close / 上述 TempDir RemoveAll 执行。
	t.Cleanup(func() {
		s.Stop()
		data.Reset()
	})
	return s, workDir, dataDir
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
