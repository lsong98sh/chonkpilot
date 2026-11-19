// 数据面请求-响应客户端（A3 总线化：server 对 chonkpilot-data 的一切访问经总线 persist，
// 不再直连 prj/usr 库）。persist 应答发布到请求**同一相对主题**（A2 正名，§3 头），载荷
// {req_id, ok, result} / {req_id, ok:false, error}；请求带 req_id 并按 req_id 匹配应答
// （忽略他人请求/应答与失败回复）。超时统一 dataTimeout。
//
// 2026-11-19（总线 promise 退场）：请求-应答机制收敛到 `host/core/buscall`（每客户端
// 一条长驻 ">" 订阅按 req_id 路由）；本文件保留 dataRequest 的既有签名与超时口径，
// 内部改走共享 Client。
package server

import (
	"context"

	"github.com/lsong98sh/chonkpilot-framework/host/core/buscall"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// dataTimeout 数据面请求超时（persist 应答同步：同进程合并形态微秒级；给分离形态留裕量）。
// buscall.DefaultTimeout 同值（3s）；此处保留常量名供既有引用。
const dataTimeout = buscall.DefaultTimeout

// dataClient 是 server 侧的数据面调用端（长驻订阅；Server 生命周期内复用）。
// nil 时按调用方传入的 bus 即席构造（测试路径；订阅开销可忽略）。
var dataClientFactory = func(bus mq.Bus) *buscall.Client {
	return buscall.New(bus)
}

// dataRequest 发 data-<domain>-<op> 请求并等 result（失败/超时 → error）。
func dataRequest(bus mq.Bus, subject string, req map[string]any) (map[string]any, error) {
	c := dataClientFactory(bus)
	defer c.Close()
	res, err := c.CallWithTimeout(context.Background(), dataTimeout, subject, req)
	if err != nil {
		if de, ok := err.(*dataError); ok {
			return nil, de
		}
		return nil, &dataError{msg: err.Error()}
	}
	if m, ok := res.(map[string]any); ok || res == nil {
		return m, nil
	}
	// result 非 map（理论上游 persist 各域 result 恒为对象/数组信封；非 map 时原样包回）。
	return map[string]any{"result": res}, nil
}

// dataError 是数据面失败（含 persist 错误 / 超时）。
type dataError struct{ msg string }

func (e *dataError) Error() string { return e.msg }
