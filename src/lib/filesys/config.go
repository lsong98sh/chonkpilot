// 「不显示的目录」配置读取（D-39 层2）。
//
// filesys 严格只依赖 chonkpilot-lib + fswatcher（不依赖 data / plugin 模块，见 10-分层与依赖 §5），
// 故此处按既有 `data-prj-config-load` 消息契约自行做一次请求-响应（语义与 plugins 内
// dataclient.ReadKey 一致，仅作用于本模块，避免引入反向依赖）。配置键经既有 data-prj-config
// 面读写，**零新增消息**。
package filesys

import (
	"context"
	"strings"
	"time"

	"github.com/lsong98sh/chonkpilot-framework/host/core/buscall"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
	"github.com/lsong98sh/chonkpilot-util/strlist"
)

// hiddenDirsKey 是「不显示的目录」配置键（prj-config 域，团队共享；登记见 64-配置项一览）。
const hiddenDirsKey = "filetree.hide-dirs"

// defaultHiddenDirs 缺省隐藏目录清单：与「点开头恒隐藏」规则重叠（default 行为同改前），
// 价值在于**可配置扩展**（用户可追加 target / node_modules 等非点开头构建目录）。
var defaultHiddenDirs = []string{".git", ".svn", ".chonkpilot"}

// prjConfigLoadTimeout 是异步读取 prj 配置的应答超时：仅用于首次 watch 的一次性补齐，
// 超时即回落缺省，**不阻塞** watch 请求本身。
const prjConfigLoadTimeout = 2 * time.Second

// parseHiddenDirs 解析「不显示的目录」清单：按逗号 / 分号 / 换行分隔，去空白、去尾分隔符、
// 去重；空串 → 缺省清单（配置为空 = 回落默认）。
func parseHiddenDirs(s string) []string {
	out := make([]string, 0, 4)
	for _, part := range strlist.SplitDedup(s) {
		part = strings.TrimRight(part, "/\\")
		if part != "" {
			out = append(out, part)
		}
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
// 2026-11-19（总线 promise 退场）：改走共享 buscall（长驻订阅按 req_id 路由）；
// 本包不依赖 data 模块的约束不变（buscall 在 framework host/core）。
func readPrjConfigKey(bus mq.Bus, instanceID, key string) (string, error) {
	c := buscall.New(bus)
	defer c.Close()
	res, err := c.CallWithTimeout(context.Background(), prjConfigLoadTimeout, msgkeys.TopicDataPrjConfigLoad, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"id": key},
	})
	if err != nil {
		return "", err
	}
	m, _ := res.(map[string]any)
	if m == nil {
		return "", nil
	}
	s, _ := m["data"].(string)
	return s, nil
}
