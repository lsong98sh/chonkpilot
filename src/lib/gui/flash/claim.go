package flash

// flash 的 UDS 认领 / 置顶（🆕 2026-10-10；61 §4.1 ①⑥ · 62 §3A）。
//
// 由 GUI 宿主（`src/lib/gui/remote.go`）与无窗口宿主（`src/flash/cli`）共用：
//   - `DialClaim`：dial 约定 UDS → `instance-claim{user, work_dir}`（前门注入 `__local` 免鉴权、
//     连接绑定实例）；
//   - `BringToTop`：请求置顶「已打开的同项目窗口」（`instance-bring-to-top{data_dir}`，
//     跨用户 → 前门回 `instance-forbidden`）。
//
// **零新增主题/payload**（61-消息一览 为唯一准则）。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/uds"
)

// ClaimInfo 是 instance-claim 成功应答（61 §4.1 ①：{instance_id, work_dir, data_dir}）。
type ClaimInfo struct {
	InstanceID string
	WorkDir    string
	DataDir    string
}

// ClaimOptions 是 DialClaim / 拨号参数。
type ClaimOptions struct {
	// WorkDir 是本实例要打开的工作目录（claim 提议，UDS 前门据此绑定）。
	WorkDir string
	// UDSPath 覆盖 UDS 前门路径；空 = uds.DefaultPath()。
	UDSPath string
	// OnControl 接收定向控制帧（如 instance-goto-top）；nil = 忽略。
	OnControl func(typ string, data []byte)
	// OnClose 在连接断开时回调（恰好一次）——承载「连接断开 = 实例退出」。
	OnClose func()
	// Logf 诊断输出；nil = 不输出。
	Logf func(format string, args ...any)
}

// DialClaim 连接 UDS 前门并**先** `instance-claim`（使连接绑定实例，其后桥的
// `instance-register` 方能经前门注入实例身份）。返回连接（= 远程总线）与认领结果。
func DialClaim(opts ClaimOptions) (*uds.Client, ClaimInfo, error) {
	cli, err := uds.Dial(uds.ClientOptions{
		Path:      opts.UDSPath,
		OnControl: opts.OnControl,
		OnClose:   opts.OnClose,
		Logf:      opts.Logf,
	})
	if err != nil {
		return nil, ClaimInfo{}, fmt.Errorf(
			"无法连接 ChonkPilot 服务端（UDS %s）：请先启动 chonkpilot-server.exe\n%w",
			uds.DefaultPath(), err)
	}
	v := cli.Emit(context.Background(), msgkeys.TopicInstanceClaim, map[string]any{
		msgkeys.InstanceClaimPayloadUser:    CurrentUser(),
		msgkeys.InstanceClaimPayloadWorkDir: opts.WorkDir,
	}).Wait()
	if v.Err() != nil {
		_ = cli.Close()
		return nil, ClaimInfo{}, fmt.Errorf("instance-claim 失败：%w", v.Err())
	}
	m, _ := v.Result.(map[string]any)
	out := ClaimInfo{
		InstanceID: payloadStr(m[msgkeys.InstanceClaimResultInstanceId]),
		WorkDir:    payloadStr(m[msgkeys.InstanceClaimResultWorkDir]),
		DataDir:    payloadStr(m[msgkeys.InstanceClaimResultDataDir]),
	}
	if out.InstanceID == "" {
		_ = cli.Close()
		return nil, ClaimInfo{}, errors.New("instance-claim 未返回 instance_id")
	}
	if out.WorkDir == "" {
		out.WorkDir = opts.WorkDir
	}
	if out.DataDir == "" {
		out.DataDir = DataDirFor(out.WorkDir)
	}
	return cli, out, nil
}

// BringToTop 连接服务端 + claim 自身身份，再发 `instance-bring-to-top{data_dir}` 请求置顶
// 既有窗口（61 §4.1 ⑥）：成功 → nil；跨用户 → 前门回 `instance-forbidden`（`IsForbidden` 判定）。
func BringToTop(workDir, udsPath string, logf func(format string, args ...any)) error {
	cli, err := uds.Dial(uds.ClientOptions{Path: udsPath, Logf: logf})
	if err != nil {
		return fmt.Errorf("该项目已在本用户打开，但无法连接服务端以置顶窗口：%w", err)
	}
	defer func() { _ = cli.Close() }()
	if v := cli.Emit(context.Background(), msgkeys.TopicInstanceClaim, map[string]any{
		msgkeys.InstanceClaimPayloadUser:    CurrentUser(),
		msgkeys.InstanceClaimPayloadWorkDir: workDir,
	}).Wait(); v.Err() != nil {
		return fmt.Errorf("该项目已在本用户打开（置顶请求 claim 失败）：%w", v.Err())
	}
	v := cli.Emit(context.Background(), msgkeys.TopicInstanceBringToTop, map[string]any{
		msgkeys.InstanceBringToTopPayloadDataDir: DataDirFor(workDir),
	}).Wait()
	if v.Err() != nil {
		return v.Err()
	}
	return nil
}

// IsForbidden 判定错误是否为前门 `instance-forbidden`（跨用户 → 提示换目录，不跳登录）。
func IsForbidden(err error) bool {
	return err != nil && strings.Contains(err.Error(), "instance-forbidden")
}

// payloadStr 取载荷字段的字符串值（缺省 ""）。
func payloadStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
