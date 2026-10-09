// 「不显示的目录」配置读取（D-39 层2）。
//
// filesys 严格只依赖 chonkpilot-lib + fsnotify（不依赖 data / plugin 模块，见 10-分层与依赖 §5），
// 故此处按既有 `data-prj-config-load` 消息契约自行做一次请求-响应（语义与 plugins 内
// dataclient.ReadKey 一致，仅作用于本模块，避免引入反向依赖）。配置键经既有 data-prj-config
// 面读写，**零新增消息**。
package filesys

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// hiddenDirsKey 是「不显示的目录」配置键（prj-config 域，团队共享；登记见 64-配置项一览）。
const hiddenDirsKey = "filetree.hide-dirs"

// defaultHiddenDirs 缺省隐藏目录清单：与「点开头恒隐藏」规则重叠（default 行为同改前），
// 价值在于**可配置扩展**（用户可追加 target / node_modules 等非点开头构建目录）。
var defaultHiddenDirs = []string{".git", ".svn", ".chonkpilot"}

// prjConfigLoadTimeout 是异步读取 prj 配置的应答超时：仅用于首次 watch 的一次性补齐，
// 超时即回落缺省，**不阻塞** watch 请求本身。
const prjConfigLoadTimeout = 2 * time.Second

// cfgReqSeq 请求序号（进程内自增，避免同 tick 撞号）。
var cfgReqSeq atomic.Uint64

// parseHiddenDirs 解析「不显示的目录」清单：按逗号 / 分号 / 换行分隔，去空白、去尾分隔符、
// 去重；空串 → 缺省清单（配置为空 = 回落默认）。
func parseHiddenDirs(s string) []string {
	out := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		part = strings.TrimRight(strings.TrimSpace(part), "/\\")
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	if len(out) == 0 {
		return defaultHiddenDirs
	}
	return out
}

// sameStrings 判定两个字符串切片是否逐元素相等（用于配置是否变化的短路）。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// readPrjConfigKey 经既有 `data-prj-config-load` 消息面读取单个 prj 配置键。
// 契约（61-消息一览 §3.1）：payload `{req_id, instance_id, data:{id}}`，
// 应答（发布到同一主题）`{req_id, ok, result:{data}}`；按 req_id 关联收敛，超时返回 error。
func readPrjConfigKey(bus mq.Bus, instanceID, key string) (string, error) {
	reqID := fmt.Sprintf("filesys-%d-%d", time.Now().UnixNano(), cfgReqSeq.Add(1))
	type reply struct {
		val string
		err error
	}
	done := make(chan reply, 1)
	sub, err := bus.On(msgkeys.TopicDataPrjConfigLoad, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string `json:"req_id"`
			OK     *bool  `json:"ok"`
			Result struct {
				Data string `json:"data"`
			} `json:"result"`
			Error string `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != reqID || m.OK == nil {
			return nil // 他人请求 / 应答忽略
		}
		if !*m.OK {
			done <- reply{err: fmt.Errorf("data-prj-config-load: %s", m.Error)}
			return nil
		}
		done <- reply{val: m.Result.Data}
		return nil
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := bus.Emit(context.Background(), msgkeys.TopicDataPrjConfigLoad, map[string]any{
		"req_id":      reqID,
		"instance_id": instanceID,
		"data":        map[string]any{"id": key},
	}).Wait().Err(); err != nil {
		return "", err
	}
	select {
	case r := <-done:
		return r.val, r.err
	case <-time.After(prjConfigLoadTimeout):
		return "", fmt.Errorf("data-prj-config-load 应答超时")
	}
}
