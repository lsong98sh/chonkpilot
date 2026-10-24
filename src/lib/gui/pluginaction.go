//go:build windows

package gui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// 插件 UI 动作面（PLUGIN-UI-1 第二段，28-plugins §3A.2）。
//
// `POST /plugins/<id>/api/<action>`（同源、仅本机形态、**不加令牌**）：
//   - 宿主只做四件事：解析 `<id>/<action>` → 校验 `X-CK-Plugin-Id == <id>` → 透传给插件
//     （`plugin.ui.rpc`，即通用 RPC）→ 原样回吐（200 + 插件结果 JSON）；**不解析业务**。
//   - 超时 / 上游错误 / 插件未就绪统一用 `{ok:false, errors:[...]}` 信封
//     （超时 = 504，插件未实现 = 501，插件未就绪 = 404，其余上游失败 = 502）。
const (
	pluginAPISegment = "/api/"
	// pluginIDHeader 是前端注入的来源视图 id（与路径 `<id>` 必须一致，防跨插件串用）。
	pluginIDHeader = "X-CK-Plugin-Id"
	// pluginActionMaxBody 是动作请求体上限（超出 → 413，不进插件）。
	pluginActionMaxBody = 1 << 20
)

// errPluginActionTooLarge 表示动作请求体超过上限（413，不进插件）。
var errPluginActionTooLarge = errors.New("请求体过大")

// pluginActionFunc 向插件透传一条 UI 动作；err 非空 = 上游失败（宿主不解读业务）。
type pluginActionFunc func(ctx context.Context, pluginID, action string, payload map[string]any) (any, error)

// pluginActionHandler 是 `/plugins/<id>/api/<action>` 的处理器。
type pluginActionHandler struct {
	call pluginActionFunc
}

func newPluginActionHandler(call pluginActionFunc) *pluginActionHandler {
	return &pluginActionHandler{call: call}
}

// matchesPluginAPIPath 判定路径是否为动作面（`/plugins/<id>/api/<action>`）。
func matchesPluginAPIPath(p string) bool {
	_, action, ok := parsePluginActionPath(p)
	return ok && action != ""
}

// parsePluginActionPath 拆分 `/plugins/<id>/api/<action>`；action 内允许 `.`（如 `status.get`），
// 不允许 `/`、`\`、`..`（越界 / 非动作形态 → ok=false，由调用方回 404）。
func parsePluginActionPath(p string) (id, action string, ok bool) {
	if !strings.HasPrefix(p, pluginsPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(p, pluginsPrefix)
	id, sub, found := strings.Cut(rest, pluginAPISegment)
	if !found || id == "" || sub == "" {
		return "", "", false
	}
	if strings.ContainsAny(id+sub, `/\`) || strings.Contains(id, "..") || strings.Contains(sub, "..") {
		return "", "", false
	}
	return id, sub, true
}

func (h *pluginActionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.call == nil {
		http.NotFound(w, r)
		return
	}
	id, action, ok := parsePluginActionPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writePluginActionErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Header.Get(pluginIDHeader) != id {
		writePluginActionErr(w, http.StatusForbidden, "plugin id mismatch")
		return
	}
	payload, err := decodePluginActionBody(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errPluginActionTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writePluginActionErr(w, status, err.Error())
		return
	}
	res, err := h.call(r.Context(), id, action, payload)
	if err != nil {
		// 未就绪 / 未登记 → 404；超时 → 504；其余（插件返回错误、通道故障）→ 502。
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			writePluginActionErr(w, http.StatusGatewayTimeout, err.Error())
		case strings.Contains(err.Error(), "未就绪"), strings.Contains(err.Error(), "未登记"),
			strings.Contains(err.Error(), "未启动"):
			writePluginActionErr(w, http.StatusNotFound, err.Error())
		case strings.Contains(err.Error(), "method_not_found"):
			// 插件未实现该动作 → 明确 501（区别于上游故障）。
			writePluginActionErr(w, http.StatusNotImplemented, err.Error())
		default:
			writePluginActionErr(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	writePluginActionJSON(w, http.StatusOK, res)
}

// decodePluginActionBody 读取动作请求体：空 body = `{}`；只接受 JSON 对象。
func decodePluginActionBody(body io.Reader) (map[string]any, error) {
	if body == nil {
		return map[string]any{}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(body, pluginActionMaxBody+1))
	if err != nil {
		return nil, errors.New("读取请求体失败")
	}
	if len(raw) > pluginActionMaxBody {
		return nil, errPluginActionTooLarge
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("请求体必须是 JSON 对象")
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// writePluginActionErr 是统一错误信封（`{ok:false, errors:[msg]}`）。
func writePluginActionErr(w http.ResponseWriter, status int, msg string) {
	writePluginActionJSON(w, status, map[string]any{"ok": false, "errors": []string{msg}})
}

func writePluginActionJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
