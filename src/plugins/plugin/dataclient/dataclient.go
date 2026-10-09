// Package dataclient 收口内嵌插件共用的 data-* 请求-响应与 prj-config 单键读写。
//
// 背景（2026-10-08）：plugin-history / plugin-codegraph / plugin-vfts 三个独立 module 曾各自
// 持有一份**逐字重复**的 `dataEmit` / `newReqID` / `prjConfigReadKey` / `prjConfigSaveKey` /
// `strval`。三者均依赖 github.com/chonkpilot/chonkpilot-plugin（本 module），故单点收口于此，
// 插件侧改为调用，消除重复（不新增 module、不改依赖图）。
//
// 契约：data-* 请求面把应答 fire-and-forget 发布到**请求同一主题**（persist 侧实现），
// 调用方按 `req_id` 关联收敛；payload/result 字段名见 61-消息一览。
package dataclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// Timeout 是 data-* 请求应答超时（三插件原先各自定义同名常量，值一致）。
const Timeout = 5 * time.Second

// reqSeq 请求序号（进程内自增，消除同 tick 撞号；见 Emit 注释）。
var reqSeq atomic.Uint64

// newReqID 生成一次请求的唯一 id。
// 用 `UnixNano + 进程内自增序号`：单序号在同 tick 并发下仍可能撞号（Windows 时钟刻度粗），
// 叠加自增序号后必唯一；前缀 "plugin-" 仅用于日志辨识，不参与过滤语义。
func newReqID() string {
	return fmt.Sprintf("plugin-%d-%d", time.Now().UnixNano(), reqSeq.Add(1))
}

// Strval 任意值 → 字符串（配置/结果值归一）。
func Strval(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// Emit 向 data-<域>-<动作> 请求面发一次请求并等应答（persist 把应答发布到请求同主题，
// 须按 req_id 关联收敛）。req_id 由本函数写入 req。
func Emit(bus mq.Bus, subject string, req map[string]any) (map[string]any, error) {
	req["req_id"] = newReqID()
	type reply struct {
		result map[string]any
		err    error
	}
	done := make(chan reply, 1)
	sub, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m struct {
			ReqID  string         `json:"req_id"`
			OK     *bool          `json:"ok"`
			Result map[string]any `json:"result"`
			Error  string         `json:"error"`
		}
		if json.Unmarshal(v.Payload, &m) != nil || m.ReqID != req["req_id"] || m.OK == nil {
			return nil // 他人请求/应答忽略
		}
		if !*m.OK {
			msg := m.Error
			if msg == "" {
				msg = "persist error"
			}
			done <- reply{err: errors.New(msg)}
			return nil
		}
		done <- reply{result: m.Result}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	f := bus.Emit(context.Background(), subject, req)
	if err := f.Wait().Err(); err != nil {
		return nil, err
	}
	select {
	case r := <-done:
		return r.result, r.err
	case <-time.After(Timeout):
		return nil, fmt.Errorf("%s via persist 应答超时", subject)
	}
}

// ReadKey 读 prj-config 单键：load 应答 result.data = 值字符串（键不存在 → ""）。
func ReadKey(bus mq.Bus, instanceID, key string) (string, error) {
	res, err := Emit(bus, msgkeys.TopicDataPrjConfigLoad, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"id": key},
	})
	if err != nil {
		return "", err
	}
	val, _ := res["data"].(string)
	return val, nil
}

// SaveKey 写 prj-config 单键（值 = 字符串；save 载荷 {data:{key,value}}）。
func SaveKey(bus mq.Bus, instanceID, key, value string) error {
	_, err := Emit(bus, msgkeys.TopicDataPrjConfigSave, map[string]any{
		"instance_id": instanceID,
		"data":        map[string]any{"key": key, "value": value},
	})
	return err
}
