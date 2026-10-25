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
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/idgen"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-lib/osuser"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
)

// LiveWorkDirs 返回当前已登记实例的**去重 work_dir**（非空；供 resident indexer
// 单实例多 workdir 发现，见 pluginbridge.Options.WorkDirs / plugin.index.workdirs）。
// 实例表仅内存（进程重启即清）→ indexer 每轮轮询即可看到新开 / 已关项目。
func (s *Server) LiveWorkDirs() []string {
	if s == nil || s.im == nil {
		return nil
	}
	list := s.im.List()
	seen := make(map[string]struct{}, len(list))
	out := make([]string, 0, len(list))
	for _, in := range list {
		wd := strings.TrimSpace(in.WorkDir)
		if wd == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(wd))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, filepath.Clean(wd))
	}
	sort.Strings(out)
	return out
}

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
	// Local 是**入口注入的连接层信任标记**（🆕 2026-10-09；非消息面字段，同 instance_id/token 口径）：
	// 本机 UDS 前门注入 true → work_dir 取客户端提议、免鉴权（61 §4.6「免鉴权由前门决定」）。
	Local bool `json:"__local"`
}

// onInstanceClaim 处理一次 instance-claim（方法面：结果写 v.Result、失败码进 v.Errors）。
func (s *Server) onInstanceClaim(_ context.Context, _ string, v *mq.Value) error {
	var req claimReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		// B-12：不再静默吞错——非法/空载荷按空请求继续（保持既有宽容语义），但留痕。
		logf("[chonkpilot-server] instance-claim: 载荷解析失败（按空请求处理）: %v\n", err)
	}

	// ① 实例标识：入口绑定优先（见文件头 2b-2 保留口径）；无绑定 → 服务端生成 uuid v4。
	id := req.InstanceID
	if id == "" {
		id = idgen.NewUUID()
	}

	// ② 入口绑定解析 work_dir/data_dir：本实例既有登记（入口启动期 instance-register 已登记）优先。
	//    **PRODSIM-1：work_dir 不再回落服务端启动参数**（单进程多项目后 opts.WorkDir 只是首个项目）；
	//    data_dir 同样只认**实例既有登记**（不回落启动参数）：desktop 缺省登记为空 → prjusr 走
	//    project-id 分支；显式 `--data-dir` 场景登记非空 → 认领必须原样保留（否则会把 bridge 写入
	//    的数据根覆盖为空，prjusr 在 <dataDir> 与 ~/.chonkpilot/data/<prj-id> 之间漂移）。
	rec, _ := s.im.Lookup(id)
	boundWorkDir := rec.WorkDir
	boundDataDir := rec.DataDir
	clientType := rec.ClientType

	// ③ 鉴权 + work_dir 归属校验（按**服务端判定的形态**分派；61 §4.6）。
	workDir, dataDir, code := s.resolveClaimTarget(&req, boundWorkDir, boundDataDir, rec.WorkDir)
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
		"user":        registerUserName(req.User), // 🆕 2026-10-09：instance-register 的 `user` 必填
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
//	本地前门（`req.Local`，UDS 本机信任 / 免鉴权）：
//	                       work_dir = 客户端提议；提议为空 → 回落**实例绑定**的 work_dir
//	                       （`instWorkDir`）；两者皆空 → instance-forbidden（不静默绑定空目录）。
//	desktop（内嵌免鉴权，`s.Form()==FormDesktop`）：work_dir/data_dir = 实例绑定或服务端启动
//	                       参数；未带绑定又无启动参数 → instance-forbidden；
//	                       提议与绑定不一致 → instance-forbidden（**不静默改写入口绑定**，2a 口径）；
//	gui / browser（要求认证）：
//	                       无有效令牌 → instance-unauthorized；
//	                       `user` 提议与连接层身份不一致 → instance-unauthorized；
//	                       `work_dir` 提议为空 → 默认 `<用户数据根>/workspace`（首次自动创建）；
//	                       默认目录/提议无权访问 → instance-forbidden；
//	                       通过 → work_dir = 规范化提议；data_dir = `<用户数据根>/<uid>/`。
//
// 返回 (work_dir, data_dir, 错误码)；错误码空 = 通过。
func (s *Server) resolveClaimTarget(req *claimReq, boundWorkDir, boundDataDir, instWorkDir string) (string, string, string) {
	// 本地前门（🆕 2026-10-09：UDS = 本机信任）—— work_dir 取**客户端提议**；
	// 提议为空则回落**连接已绑定实例的 work_dir**（`instWorkDir` = 实例登记的 work_dir）
	// ——flash 薄壳的 SPA 按 desktop 语义认领时**不带 work_dir**（其 work_dir 已由 flash 自身
	// `DialClaim` 绑定并经 instance-register 登记），缺失回落会让「免鉴权的本地前端认领」被
	// 误判 forbidden（= 黑屏 + “无权访问该工作目录”）。
	// **只认实例绑定**（不落 `boundWorkDir`/启动参数）：server 的启动 cwd（= `s.opts.WorkDir`，
	// 独立 server 下即安装目录）**不是项目目录**，回落它会在实例未绑定时**静默绑定安装目录**
	// → 使本分支的 forbidden 守卫失效（fail-open）。实例未绑定且无提议 → 明确失败。
	// data_dir 随 work_dir 派生为 `<work_dir>/.chonkpilot`（与 desktop 单体口径一致）。
	if req.Local {
		wd := strings.TrimSpace(req.WorkDir)
		if wd == "" {
			wd = strings.TrimSpace(instWorkDir)
		}
		if wd == "" {
			return "", "", claimErrForbidden // 无提议且无实例绑定 → 明确失败（不静默绑定空目录）
		}
		wd = filepath.Clean(wd)
		return wd, filepath.Join(wd, ".chonkpilot"), ""
	}
	if f := s.Form(); f == FormDesktop {
		// PRODSIM-1（多项目主窗口）：work_dir 取**客户端提议**，缺省回落**实例登记**
		// （`instWorkDir`），**不再回落服务端启动参数** —— 单进程多项目后 opts.WorkDir 只是
		// 首个项目的路径，回落它会让其它项目静默绑到首个项目（或安装目录）。
		//
		// data_dir：**保留实例既有绑定**（`boundDataDir` 已含 opts.DataDir 回落）。
		// 2026-10-23 修正：此前恒返回空串，会把 bridge 已登记的显式 `--data-dir` 覆盖为空 →
		// 同一实例的 prjusr 在「<dataDir>（prj 与 prjusr 同根，12-数据层 §3）」与
		// 「~/.chonkpilot/data/<prj-id>」之间来回漂移（先写入一处、后读取另一处 →
		// 会话列表 / 历史恒为空）。缺省（未给 --data-dir）仍为空 → 走 project-id 分支。
		wd := strings.TrimSpace(req.WorkDir)
		if wd == "" {
			wd = strings.TrimSpace(instWorkDir)
		}
		if wd == "" {
			// 既无提议、又无实例登记 → 不静默绑定空目录（明确失败）。
			return "", "", claimErrForbidden
		}
		return filepath.Clean(wd), strings.TrimSpace(boundDataDir), ""
	}

	// PRODSIM-1：本地认证面已整体移除（认证将来统一到 registry）—— 仅保留
	// Local（UDS 本机信任）与 desktop 两个分支；其余形态不存在。
	return "", "", claimErrForbidden
}

// registerUserName 归一 `instance-register` 的 `user` 字段（🆕 2026-10-09；61 §4.1 ⑤ 该字段必填）：
// 取客户端提议（本机形态自报 OS 用户名），空则回落**本机 OS 用户名**（服务端进程同一用户）。
func registerUserName(proposed string) string {
	if u := strings.TrimSpace(proposed); u != "" {
		return u
	}
	return osuser.CurrentUser()
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
