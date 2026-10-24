// engineclient 是「manifest 插件 → 既有常驻引擎（legacy pluginhost）」的通用桥接客户端。
//
// 用途：browser / desktop 等插件把业务留在引擎 exe 里，插件只做 MCP/UI 桥接。
// 协议（src/lib/mcp-tools/internal/pluginproto）：一行一 JSON
//
//	请求 {"id","endpoint","params"}；应答 {"id","ok","result"|"error"}
//	endpoint ∈ start / stop / operation / ping / shutdown
//
// 约定：**每次调用新建连接**（引擎按连接串行处理），避免多路复用错帧。
package engineclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/winproc"
)

// Options 是引擎客户端参数。
type Options struct {
	// Root 是引擎 exe 所在目录（缺省 = 插件目录，由调用方传 CHONKPILOT_PLUGIN_ROOT）。
	Root string
	// Exe 是引擎可执行文件名（如 chonkpilot-browser-plugin.exe）。
	Exe string
	// SocketName 是缺省 socket 文件名（如 browser.sock）；与 Socket 二选一。
	SocketName string
	// Socket 是显式 socket 路径（优先于 SocketName）。
	Socket string
	// Args 是拉起引擎时的额外参数（-socket 由本包自动追加）。
	Args []string
	// Timeout 是单次调用超时（缺省 120s）。
	Timeout time.Duration
	// StartTimeout 是拉起后等待就绪的上限（缺省 25s）。
	StartTimeout time.Duration
	// Logf 用于诊断输出（可为 nil）。
	Logf func(format string, args ...any)
}

// Client 是引擎客户端（并发安全）。
type Client struct {
	opts Options
	seq  int64
	mu   sync.Mutex // 保护 spawn（避免并发重复拉起）
}

// New 构造引擎客户端。
func New(o Options) *Client {
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = 25 * time.Second
	}
	if o.Socket == "" && o.SocketName != "" {
		o.Socket = DefaultSocket(o.SocketName)
	}
	return &Client{opts: o}
}

// Socket 返回当前使用的 socket 路径。
func (c *Client) Socket() string { return c.opts.Socket }

// DefaultSocket 与既有引擎的缺省路径一致：%LOCALAPPDATA%\chonkpilot\plugins\<name>。
func DefaultSocket(name string) string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "chonkpilot", "plugins", name)
}

// Ensure 保证引擎在监听：先 ping，失败则拉起同目录 exe 并轮询。
func (c *Client) Ensure(ctx context.Context) error {
	if _, err := c.Call(ctx, "ping", nil); err == nil {
		return nil
	}
	c.mu.Lock()
	// 二次检查：并发调用时可能已被另一个 goroutine 拉起。
	if _, err := c.Call(ctx, "ping", nil); err == nil {
		c.mu.Unlock()
		return nil
	}
	serr := c.spawn()
	c.mu.Unlock()
	if serr != nil {
		return serr
	}
	deadline := time.Now().Add(c.opts.StartTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if _, err := c.Call(ctx, "ping", nil); err == nil {
			return nil
		}
	}
	return fmt.Errorf("引擎未就绪（exe=%s socket=%s）", c.opts.Exe, c.opts.Socket)
}

func (c *Client) spawn() error {
	exe := c.opts.Exe
	if !filepath.IsAbs(exe) {
		exe = filepath.Join(c.opts.Root, exe)
	}
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("引擎缺失: %s", exe)
	}
	args := append(append([]string{}, c.opts.Args...), "-socket", c.opts.Socket)
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	// 引擎 exe 为 console 子系统：隐藏控制台窗口（不影响 UDS 通信）。
	cmd.SysProcAttr = winproc.SysProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动引擎失败: %w", err)
	}
	if c.opts.Logf != nil {
		c.opts.Logf("engine spawned: %s (pid=%d, socket=%s)", exe, cmd.Process.Pid, c.opts.Socket)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

type frame struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
	Params   any    `json:"params,omitempty"`
}

type reply struct {
	ID     string         `json:"id"`
	OK     bool           `json:"ok"`
	Result map[string]any `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Call 执行一次引擎调用（start/stop/operation/ping）。
func (c *Client) Call(ctx context.Context, endpoint string, params any) (map[string]any, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "unix", c.opts.Socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	deadline := time.Now().Add(c.opts.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	id := strconv.FormatInt(atomic.AddInt64(&c.seq, 1), 10)
	if err := json.NewEncoder(conn).Encode(frame{ID: id, Endpoint: endpoint, Params: params}); err != nil {
		return nil, err
	}
	var rep reply
	if err := json.NewDecoder(conn).Decode(&rep); err != nil {
		return nil, err
	}
	if !rep.OK {
		if rep.Error != nil {
			return nil, fmt.Errorf("%s: %s", rep.Error.Code, rep.Error.Message)
		}
		return nil, fmt.Errorf("%s 执行失败", endpoint)
	}
	if rep.Result == nil {
		rep.Result = map[string]any{}
	}
	return rep.Result, nil
}
