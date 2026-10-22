package pluginclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTP 前门协议常量（与 pluginbridge 的 HTTP 面一致；此处复制字符串，
// 不把服务端类型暴露给插件开发者）。
const (
	httpHelloPath    = "/plugin/v1/hello"
	httpEventsPath   = "/plugin/v1/events"
	httpPrefixPath   = "/plugin/v1/"
	httpHeaderPlugin = "X-Plugin-ID"
	httpMaxBody      = 64 << 20
)

// httpClient 是 HTTP 前门实现：请求-响应 + SSE 事件流（断线自动重连）。
type httpClient struct {
	opts       Options
	base       string
	pluginID   string
	instanceID string
	http       *http.Client

	events    chan Event
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func dialHTTP(ctx context.Context, opts Options) (Client, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.HTTPBase), "/")
	if base == "" {
		return nil, fmt.Errorf("pluginclient: HTTPBase 为空")
	}
	c := &httpClient{
		opts:   opts,
		base:   base,
		http:   &http.Client{},
		events: make(chan Event, 64),
	}
	body, err := json.Marshal(helloPayload(opts))
	if err != nil {
		return nil, err
	}
	callCtx, cancel := withTimeout(ctx, opts.Timeout)
	defer cancel()
	resp, err := c.request(callCtx, http.MethodPost, httpHelloPath, body)
	if err != nil {
		return nil, fmt.Errorf("pluginclient: HTTP hello: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("pluginclient: HTTP 401 unauthorized: %s", strings.TrimSpace(string(raw)))
	}
	var out struct {
		OK         bool     `json:"ok"`
		PluginID   string   `json:"plugin_id"`
		InstanceID string   `json:"instance_id"`
		Errors     []string `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("pluginclient: hello 应答解析失败（HTTP %d）: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || !out.OK {
		return nil, fmt.Errorf("pluginclient: plugin.hello 失败（HTTP %d）: %s", resp.StatusCode, errorText(out.Errors))
	}
	c.pluginID = out.PluginID
	if c.pluginID == "" {
		c.pluginID = opts.PluginID
	}
	c.instanceID = out.InstanceID
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.wg.Add(1)
	go c.sseLoop()
	return c, nil
}

func (c *httpClient) PluginID() string   { return c.pluginID }
func (c *httpClient) InstanceID() string { return c.instanceID }
func (c *httpClient) Events() <-chan Event {
	return c.events
}

func (c *httpClient) Call(ctx context.Context, method string, args any) (json.RawMessage, error) {
	ctx, cancel := withTimeout(ctx, c.opts.Timeout)
	defer cancel()
	var body []byte
	var err error
	if args == nil {
		body = []byte("{}")
	} else {
		body, err = json.Marshal(args)
		if err != nil {
			return nil, err
		}
	}
	resp, err := c.request(ctx, http.MethodPost, httpPrefixPath+method, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("pluginclient: HTTP 401 unauthorized: %s", strings.TrimSpace(string(raw)))
	}
	var out struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Errors []string        `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("pluginclient: 响应解析失败（HTTP %d）: %w", resp.StatusCode, err)
	}
	if !out.OK {
		return nil, fmt.Errorf("pluginclient: %s 失败: %s", method, errorText(out.Errors))
	}
	return nullRaw(out.Result), nil
}

func (c *httpClient) Subscribe(ctx context.Context, subs []Subscription) error {
	_, err := c.Call(ctx, "plugin.subscribe", subscribeArgs(subs))
	return err
}

func (c *httpClient) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		c.http.CloseIdleConnections()
		c.wg.Wait()
	})
	return nil
}

func (c *httpClient) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.opts.Token)
	}
	if c.pluginID != "" {
		req.Header.Set(httpHeaderPlugin, c.pluginID)
	}
	return c.http.Do(req)
}

const (
	sseBackoffMin = time.Second
	sseBackoffMax = 5 * time.Second
)

// sseLoop 持续读取事件流；断开后按 1s→5s 退避重连，Close 后退出。
func (c *httpClient) sseLoop() {
	defer c.wg.Done()
	defer close(c.events)
	backoff := sseBackoffMin
	for {
		connected := c.streamOnce()
		if c.ctx.Err() != nil {
			return
		}
		if connected {
			backoff = sseBackoffMin
		}
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > sseBackoffMax {
			backoff = sseBackoffMax
		}
	}
}

// streamOnce 读一轮 SSE；返回是否成功建立过连接（用于重置退避）。
func (c *httpClient) streamOnce() bool {
	resp, err := c.request(c.ctx, http.MethodGet, httpEventsPath, nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return false
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), httpMaxBody)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var f struct {
			Kind    string          `json:"k"`
			Name    string          `json:"name"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(data), &f) != nil {
			continue
		}
		if f.Kind != "evt" && f.Kind != "ctl" {
			continue
		}
		c.push(Event{Kind: f.Kind, Name: f.Name, Payload: f.Payload})
	}
	return true
}

func (c *httpClient) push(ev Event) {
	select {
	case c.events <- ev:
	default:
	}
}
