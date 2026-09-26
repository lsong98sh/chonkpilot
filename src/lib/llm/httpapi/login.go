// login 域**入口承载**（61-消息一览 §4.6；阶段 2b-1）。
//
// 本入口（browser 形态）是**会话令牌**的承载方：
//   - 上行：从**连接层**取当前令牌（cookie `chonkpilot-token`）注入内部字段 `token`，并注入
//     `token_kind=session` —— **不采信前端 payload 里的身份字段**；
//   - 下行：服务端应答里的内部 `token` 字段在此**取走并剥除** → **令牌不进前端 payload**
//     （前端只看到 61 §4.6 的 `{ok, user}`）；令牌只经 `Set-Cookie` 下发；
//   - `login-out` → 清 cookie（服务端清令牌行）。
//
// cookie 属性（61 §4.6）：`HttpOnly`、**不设 `Max-Age`**（= 会话 cookie，关浏览器即失效）。
//
// 注：认证域三个主题**不经** `frontMethodSubjects` 分派（需 Set-Cookie，故走专用分支）。
package httpapi

import (
	"encoding/json"
	"net/http"
)

const (
	// authCookieName 是会话令牌的 cookie 名（61 §4.6：`Set-Cookie: chonkpilot-token=<v>`）。
	authCookieName = "chonkpilot-token"
	// tokenKindSession 是本入口注入的令牌类别（browser = 会话令牌）。
	tokenKindSession = "session"
)

// loginSubjects 认证域上行 type → 相对主题（同名，61 §4.6）。
var loginSubjects = map[string]string{
	"login-register": "login-register",
	"login-in":       "login-in",
	"login-out":      "login-out",
}

// isLoginTopic 判定是否认证域上行。
func isLoginTopic(typ string) bool {
	_, ok := loginSubjects[typ]
	return ok
}

// cookieToken 取本连接当前会话令牌（cookie；缺失 → ""）。身份一律以连接层为准。
func cookieToken(r *http.Request) string {
	c, err := r.Cookie(authCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// handleLogin 处理认证域上行（信封与 handlePublish 逐字段一致：HTTP 恒 200，错误收进 errors）。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, typ, payloadJSON string) {
	var m map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	// 入口注入（非消息面字段）：令牌类别 + 连接层令牌（同 instance_id 由入口注入的既有口径）。
	m["token_kind"] = tokenKindSession
	m["token"] = cookieToken(r)
	raw, err := json.Marshal(m)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "result": nil, "errors": []string{"login: " + err.Error()},
		})
		return
	}
	result, errs := s.publishV(loginSubjects[typ], raw)
	issued := takeInternalToken(result) // 取走内部 token（返回前端前必须剥除）
	switch {
	case typ == "login-out":
		s.clearAuthCookie(w) // 登出：清 cookie（令牌行由服务端清）
	case issued != "":
		s.setAuthCookie(w, issued)
	}
	emsg := make([]string, 0, len(errs))
	for _, e := range errs {
		emsg = append(emsg, e.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(emsg) == 0, "result": result, "errors": emsg})
}

// takeInternalToken 从服务端应答里**取走**内部令牌字段（`token` / `token_kind`）——
// 返回前前端永远看不到令牌（61 §4.6：令牌由入口承载、不下发前端）。
func takeInternalToken(result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	token, _ := m["token"].(string)
	delete(m, "token")
	delete(m, "token_kind")
	return token
}

// setAuthCookie 下发会话令牌 cookie（61 §4.6：HttpOnly、**不设 Max-Age**）。
func (s *Server) setAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearAuthCookie 清除会话令牌 cookie（登出即时失效：Max-Age<0）。
func (s *Server) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
