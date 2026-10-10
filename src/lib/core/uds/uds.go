// Package uds 是「服务端 ↔ 本机薄壳客户端（flash）」的 UDS（Unix domain socket）跨进程消息传输。
//
// 设计见 docs/spec/10-architecture/19-多租户服务端形态.md §8.11 与
// docs/spec/60-reference/62-命令行与参数.md §3A：
//   - 前门 = 本机 UDS socket，路径 = **约定值**（DefaultPath；双方各自推导、**无 CLI 参数**）；
//   - **instance ↔ 连接 1:1**（一条连接 = 一个实例；连接断开 = 该实例退出）；
//   - **免鉴权由前门决定**（UDS = 本机信任 → 免鉴权），与 `form` 无关；
//   - 帧 = **行分隔 JSON**（便于逐帧读写与调试）。
//
// 本包只做**传输**：不认识业务主题（subject → 前端 type 映射、instance 路由规则由调用方决定），
// 也不依赖 llm/gui/plugin —— 供 server（accept 侧）与 flash（dial 侧）共用。
package uds

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// 帧类型（行分隔 JSON 的 `k` 字段）。
const (
	frameRequest  = "req" // 客户端 → 服务端：一次上行（等价 GUI 桥的 publish）
	frameResponse = "res" // 服务端 → 客户端：上行应答信封（61 §9.1）
	frameEvent    = "evt" // 服务端 → 客户端：总线事件（下行）
	frameControl  = "ctl" // 服务端 → 目标实例：**定向控制帧**（如 instance-goto-top；不下发前端）
)

// EnvUDSPath 是 UDS 路径的**环境变量覆盖**（测试 / 多实例隔离；缺省 = DefaultPath）。
// 生产不使用 —— 双方各自推导同一约定值（62 §3A）。
const EnvUDSPath = "CHONK_UDS"

// MaxFrameBytes 是单帧（行分隔 JSON）的字节上限（R7-B-5）：对端灌无换行大流时，
// bufio.ReadBytes 会无界缓冲吃爆内存；超过本上限 → 断连并留痕。
// 取值 = 64 MiB：远大于任何合法业务帧（含编辑器大文件内容），又给内存设硬上限。
const MaxFrameBytes = 64 << 20

// ErrFrameTooLarge 是对端单帧超过 MaxFrameBytes 时的错误（调用方据此断连 + 留痕）。
var ErrFrameTooLarge = errors.New("uds: frame too large")

// readFrame 读一行帧（以 '\n' 结尾），单帧字节数超过 MaxFrameBytes → ErrFrameTooLarge。
// 返回的切片为副本（可安全持有）；半帧（超限）不返回。
func readFrame(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			if len(buf) > MaxFrameBytes {
				return nil, ErrFrameTooLarge
			}
			continue
		}
		if len(buf) > MaxFrameBytes {
			return nil, ErrFrameTooLarge
		}
		return buf, err
	}
}

// DefaultPath 返回 UDS 前门的**约定路径**（62 §3A）：`%LOCALAPPDATA%\chonkpilot\server.sock`。
// 目录 ACL 限当前用户（Windows 下由 %LOCALAPPDATA% 语义保证）。
// 环境变量 `CHONK_UDS` 非空时覆盖（测试 / 多实例隔离）。
func DefaultPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvUDSPath)); p != "" {
		return p
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir() // 非 Windows / 无 LOCALAPPDATA 的兜底（不落系统共享目录）
	}
	return filepath.Join(base, "chonkpilot", "server.sock")
}

// requestFrame 是上行帧。
type requestFrame struct {
	K       string          `json:"k"`
	ID      int64           `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// responseFrame 是应答帧（`{ok,result,errors}` 信封，字段与 61 §9.1 逐字一致）。
type responseFrame struct {
	K      string   `json:"k"`
	ID     int64    `json:"id"`
	OK     bool     `json:"ok"`
	Result any      `json:"result,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

// eventFrame 是下行事件帧（`{type,payload,src}`，与 GUI 桥 envelope 同形）。
type eventFrame struct {
	K       string          `json:"k"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Src     string          `json:"src,omitempty"`
}

// controlFrame 是**定向控制帧**（无 payload 或最小 payload；消费方 = flash 宿主，不下发前端）。
type controlFrame struct {
	K    string          `json:"k"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// payloadBytes 归一载荷为 JSON 原始字节（与 mq.Emit 的序列化口径一致）。
func payloadBytes(payload any) json.RawMessage {
	switch p := payload.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return p
	case []byte:
		return json.RawMessage(p)
	case string:
		return json.RawMessage(p)
	default:
		raw, err := json.Marshal(p)
		if err != nil {
			return nil
		}
		return json.RawMessage(raw)
	}
}

// instanceIDOf 从事件载荷提取 `instance_id`（无字段 / 空串 / 非对象 → "" = 无归属）。
func instanceIDOf(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var probe struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &probe) != nil {
		return ""
	}
	return probe.InstanceID
}

// splitSegments 按 "." 拆主题段。
func splitSegments(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ".")
}

// matchSubject 判定 subject 是否命中 pattern（`>` 匹配任意剩余多段 / `*` 匹配恰好一段）。
// 与 chonkpilot-lib/mq 的匹配语义一致（跨进程侧本地订阅表用）。
func matchSubject(pattern, subject string) bool {
	return matchTokens(splitSegments(pattern), splitSegments(subject))
}

func matchTokens(pattern, subject []string) bool {
	if len(pattern) == 0 {
		return len(subject) == 0
	}
	switch pattern[0] {
	case ">":
		return true
	case "*":
		return len(subject) > 0 && matchTokens(pattern[1:], subject[1:])
	default:
		return len(subject) > 0 && pattern[0] == subject[0] && matchTokens(pattern[1:], subject[1:])
	}
}
