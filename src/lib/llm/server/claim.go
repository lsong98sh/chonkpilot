// instance-claim：实例认领（61-消息一览 §4.1 ①，2026-09-20；阶段 2a 实施，2b-2 接权限校验）。
//
// 语义（前端在登录 + 选目录之后发起；desktop 直接在启动时发起）：
//   - **请求-响应**：应答经 publish promise 回**发起者**（v.Result，按各自 publish 往返 →
//     多客户端不串号），无 `.reply` 事件、payload 无 req_id；
//   - 请求 `{user?, work_dir?}`（**均为提议**；desktop 省略 → 用服务端启动参数）；
//   - 应答 `{instance_id, work_dir, data_dir}`；
//   - 失败 `{ok:false, error:"instance-unauthorized"|"instance-forbidden"}`（复用既有
//     {ok,error} 信封、不新增字段，61 §4.6 错误码；「未认证」不是 HTTP 401 —— GUI 走 MQ）；
//   - **claim 成功后照发既有 `instance-register`**（主题与 payload **一字不改**：
//     {instance_id, work_dir, data_dir?, client_type?}）→ 供 chonkpilot-data / chonkpilot-filesys
//     / 本 server 登记 `instance → work_dir`（G-21 依赖它）。
//
// 内存实例表 = 既有共通 instance 视图（`instance.Manager`，本包字段 s.im）：字段恰为
// {instance_id, work_dir, data_dir, lastBeat, client_type}（`instance.Info`）；仅内存、
// 进程重启即清（与 22 §4「服务端重启 → 实例内存表清空」一致）。
//
// 鉴权与权限校验（阶段 2b-2，替代 2a 的粗判定；61 §4.6 · 22 §6.7）：
//   - **身份一律由入口从连接层取令牌**（browser = cookie `chonkpilot-token`；GUI = 桥持有），
//     经内部字段 `token` 注入 —— **不采信 payload 的身份字段**（22 §1「客户端不自称」）；
//   - **形态由服务端判定**（`Options.Form`，非 UA、非客户端自报）：`desktop` 免鉴权
//     （本机单用户，work_dir 取服务端启动参数/入口绑定）；`gui` / `browser` 要求令牌；
//   - 失败码 = `instance-unauthorized`（未登录 / 令牌失效 → 前端跳登录）/ `instance-forbidden`
//     （已登录但**无权访问该 work_dir** → 提示换目录，**不跳登录**）；
//   - `user` 提议与连接层身份**交叉校验**（不一致 → unauthorized）；`work_dir` 提议按
//     `allowWorkDir` 校验归属（见下）；
//   - desktop / browser 的 `data_dir` = **该用户的数据根** `<用户数据根>/<uid>/`
//     （61 §4.6：业务数据每用户一套库 → 天然隔离）。
//
// 2b-2 保留的 2a 口径（**未改**）：
//   - `desktop` 下 `work_dir` **提议**仍只做「不得静默改写入口绑定」的粗判定
//     （与绑定不一致 → `instance-forbidden`）—— 该分支只做免鉴权回落，不做用户/项目判定；
//   - **实例标识 =「入口绑定优先、缺省服务端生成 uuid v4」**（22 §3.2 的 2a 折衷；转为
//     「恒服务端生成」是认证域成为唯一绑定方之后的独立决定，仍挂 41 G-28 待拍板）。
package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data/auth"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// SubjectInstanceClaim 是实例认领主题（相对主题；chonk. 前缀由总线注入，61 §4.1）。
// 归属客户端方法面（前端 type `instance-claim` 经桥/httpapi 上行，见 frontMethodSubjects）。
const SubjectInstanceClaim = msgkeys.TopicInstanceClaim

// 错误码（61 §4.6；复用既有 {ok,error} 信封，不新增字段）。
const (
	claimErrUnauthorized = "instance-unauthorized" // 未登录 / 令牌失效 → 前端跳登录
	claimErrForbidden    = "instance-forbidden"    // 已登录但无权访问该 work_dir → 提示换目录（不跳登录）
)

// claimReq 是 instance-claim 请求载荷（61 §4.1：{user?, work_dir?}）。
//
// instance_id 非消息面字段，而是**入口注入的连接绑定**（bridge injectInstance / httpapi
// injectInstance 对单字方法面统一注入）；user / work_dir 均为**提议**，服务端以绑定为准。
//
// `token` 亦是**入口注入的连接层凭据**（非消息面字段；同 instance_id 既有口径）—— 服务端
// 只按它解析身份，**不采信 payload 里的 `user` 作身份**（22 §1）。
type claimReq struct {
	InstanceID string `json:"instance_id"`
	User       string `json:"user"`
	WorkDir    string `json:"work_dir"`
	Token      string `json:"token"`
}

// onInstanceClaim 处理一次 instance-claim（方法面：结果写 v.Result、失败码进 v.Errors）。
func (s *Server) onInstanceClaim(_ context.Context, _ string, v *mq.Value) error {
	var req claimReq
	_ = json.Unmarshal(v.Payload, &req)

	// ① 实例标识：入口绑定优先（见文件头 2b-2 保留口径）；无绑定 → 服务端生成 uuid v4。
	id := req.InstanceID
	if id == "" {
		id = newUUID()
	}

	// ② 入口绑定解析 work_dir/data_dir：本实例既有登记（入口启动期 instance-register 已登记）
	//    优先，缺省回落**服务端启动参数**（--work-dir，缺省 cwd；--data-dir，缺省
	//    <work_dir>/.chonkpilot）—— desktop 省略 payload 时即走后者（61 §4.1 ①）。
	rec, _ := s.im.Lookup(id)
	boundWorkDir := rec.WorkDir
	if boundWorkDir == "" {
		boundWorkDir = s.opts.WorkDir
	}
	boundDataDir := rec.DataDir
	if boundDataDir == "" {
		boundDataDir = s.opts.DataDir
	}
	clientType := rec.ClientType

	// ③ 鉴权 + work_dir 归属校验（按**服务端判定的形态**分派；61 §4.6）。
	workDir, dataDir, code := s.resolveClaimTarget(&req, boundWorkDir, boundDataDir)
	if code != "" {
		logf("[chonkpilot-server] instance-claim %s: work_dir=%q → %s（form=%s）\n",
			id, req.WorkDir, code, s.Form())
		return s.claimFail(v, code)
	}

	// ④ 写服务端内存实例表（{instance_id, work_dir, data_dir, lastBeat, client_type}）——
	//    复用共通 instanceManager（唯一内存表，LastBeat = now，进程重启即清）。
	reg := map[string]any{
		"instance_id": id,
		"work_dir":    workDir,
		"data_dir":    dataDir,
	}
	if clientType != "" {
		reg["client_type"] = clientType // 既有登记值（gui/browser/cli）；未登记 → 字段缺省（不臆造）
	}
	if raw, err := json.Marshal(reg); err == nil {
		if err := s.im.HandleRegister(raw); err != nil {
			// 实例数上限（DefaultMaxInstances，缺口 7）→ 明确失败（不静默成功）。
			// 61 §4.6 只定义两个错误码，此处取「不跳登录」的 instance-forbidden。
			logf("[chonkpilot-server] instance-claim %s: 登记被拒: %v\n", id, err)
			return s.claimFail(v, claimErrForbidden)
		}
	}

	// ⑤ 照发既有 instance-register（**主题与 payload 一字不改**）→ data/filesys/server 登记
	//    `instance → work_dir`（G-21 依赖）。重复登记幂等（各订阅方按 instance_id 覆盖）。
	s.publish(instance.SubjectRegister, reg)

	// ⑥ 应答发起者（publish promise：本请求的 v.Result → 桥/入口 HTTP 响应 → 前端）。
	v.Result = map[string]any{"instance_id": id, "work_dir": workDir, "data_dir": dataDir}
	return nil
}

// resolveClaimTarget 按形态解析本次认领的 `work_dir` / `data_dir`（61 §4.1 ① · §4.6）。
//
//	desktop（免鉴权）：work_dir/data_dir = 入口绑定或服务端启动参数（本机单用户）；
//	                       未带绑定又无启动参数 → instance-forbidden（不静默绑定空目录）；
//	                       提议与绑定不一致 → instance-forbidden（**不静默改写入口绑定**，2a 口径）；
//	gui / browser（要求认证）：
//	                       无有效令牌 → instance-unauthorized；
//	                       `user` 提议与连接层身份不一致 → instance-unauthorized；
//	                       `work_dir` 提议为空 / 无权访问 → instance-forbidden；
//	                       通过 → work_dir = 规范化提议；data_dir = `<用户数据根>/<uid>/`。
//
// 返回 (work_dir, data_dir, 错误码)；错误码空 = 通过。
func (s *Server) resolveClaimTarget(req *claimReq, boundWorkDir, boundDataDir string) (string, string, string) {
	if s.Form() == FormDesktop {
		workDir, dataDir := boundWorkDir, boundDataDir
		if strings.TrimSpace(workDir) == "" {
			// 既无实例登记、又无启动参数 → 不静默绑定空目录（明确失败，且不引导登录）。
			return "", "", claimErrForbidden
		}
		if p := strings.TrimSpace(req.WorkDir); p != "" && !sameDir(p, workDir) {
			return "", "", claimErrForbidden
		}
		return workDir, dataDir, ""
	}

	// desktop / browser：身份以**连接层令牌**为准（前端自报的 user 只作提议交叉校验）。
	u, err := s.userByToken(req.Token, "")
	if err != nil {
		return "", "", claimErrUnauthorized
	}
	if p := strings.TrimSpace(req.User); p != "" && !sameUser(p, u) {
		logf("[chonkpilot-server] instance-claim: user 提议 %q 与连接层身份 %q 不一致\n", p, u.Username)
		return "", "", claimErrUnauthorized
	}
	dir := strings.TrimSpace(req.WorkDir)
	if dir == "" {
		// desktop / browser 的 work_dir **必填**（61 §4.1 ①）；未提供 → 不臆造，提示换目录。
		return "", "", claimErrForbidden
	}
	// `auth.projectRoots` 允许根（缺省 = 用户数据根，见 authform 部分）。
	ok, err := s.allowWorkDir(u.UID, dir)
	if err != nil {
		// auth 库不可用 / 项目登记读写失败 → 无法确认归属（**不放行**）：按「需重新登录」处理。
		logf("[chonkpilot-server] instance-claim: work_dir 归属校验失败: %v\n", err)
		return "", "", claimErrUnauthorized
	}
	if !ok {
		return "", "", claimErrForbidden
	}
	dataDir := s.userDataDir(u.UID)
	if dataDir == "" {
		dataDir = boundDataDir // 未配置用户数据根（异常装配）→ 回落既有绑定，不阻断认领
	}
	return auth.NormalizeDir(dir), dataDir, ""
}

// allowWorkDir 判定用户对 work_dir 的访问权（61 §4.6「项目选择白名单」· 22 §6.7）：
//
//	① work_dir（规范化后）命中**既有项目**（`projects_by_path`）→ 要求该 user 在 `access`
//	   中有该项目关联（有 → 通过；无 → 拒绝）；
//	② 未登记为项目、但**落在某个允许根之下**（`auth.projectRoots`；缺省 = 用户数据根）→
//	   **用户自建项目**：建 `projects` 记录（name = 目录名、created_by = uid）+ `access`
//	   关联 → 通过（方案 A：自建项目已够本阶段闭环，不做管理员授权界面）；
//	③ 其余（不在允许根下 / 项目存在但无关联）→ 拒绝 → `instance-forbidden`。
//
// 返回 (是否放行, 内部错误)；内部错误由调用方按「无法确认归属」处理（不放行）。
func (s *Server) allowWorkDir(uid, dir string) (bool, error) {
	dir = auth.NormalizeDir(dir)
	if strings.TrimSpace(uid) == "" || dir == "" {
		return false, nil
	}
	if err := s.ensureAuth(); err != nil {
		return false, err
	}
	if proj, ok, err := s.authz.ProjectByPath(dir); err != nil {
		return false, err
	} else if ok {
		return s.authz.HasAccess(uid, proj.ProjectID)
	}
	if !withinAnyRoot(s.projectRoots(), dir) {
		return false, nil
	}
	_, err := s.authz.GrantProject(uid, dir)
	return err == nil, err
}

// projectRoots 读配置 `auth.projectRoots`（**允许根列表**）：空 = 缺省 `<用户数据根>`。
func (s *Server) projectRoots() []string {
	if roots := s.opts.ProjectRoots; len(roots) > 0 {
		return roots
	}
	if root := s.userDataRootPath(); strings.TrimSpace(root) != "" {
		return []string{root}
	}
	return nil
}

// withinAnyRoot 判定 dir（规范化绝对路径）是否落在某个允许根之下（**含自身**）。
// 比较用 `filepath.Rel`：Windows 下大小写不敏感、跨盘符返回错误 → 该根视为不匹配。
func withinAnyRoot(roots []string, dir string) bool {
	for _, r := range roots {
		root := auth.NormalizeDir(r)
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		return true
	}
	return false
}

// sameUser 交叉校验 `user` 提议与连接层身份（61 §4.1 ①：身份**以连接层为准**）：
// 命中用户名（Windows/大小写不敏感）或 uid → 一致。
func sameUser(proposed string, u auth.User) bool {
	proposed = strings.TrimSpace(proposed)
	return strings.EqualFold(proposed, u.Username) || proposed == u.UID
}

// claimFail 写回认领失败（61 §4.6：复用既有 {ok,error} 信封，不新增字段）：
// Result = {ok:false, error:<code>}（前端据 error 分派）+ 同名错误进 v.Errors（HTTP 侧 ok=false）。
func (s *Server) claimFail(v *mq.Value, code string) error {
	v.Result = map[string]any{"ok": false, "error": code}
	return errors.New(code)
}

// sameDir 比较两个目录是否同一（Windows 大小写不敏感；产品形态为 Windows）。
// desktop 分支的「提议不得改写入口绑定」判定用（详略：只比字面清理后的路径，
// 不做符号链接/8.3 短名归一）。
func sameDir(a, b string) bool {
	ca := filepath.Clean(filepath.FromSlash(strings.TrimSpace(a)))
	cb := filepath.Clean(filepath.FromSlash(strings.TrimSpace(b)))
	return strings.EqualFold(ca, cb)
}

// newUUID 生成 RFC 4122 v4 风格 UUID（不引入额外依赖；同 GUI 桥 / httpapi 实现）。
func newUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("ins-%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
