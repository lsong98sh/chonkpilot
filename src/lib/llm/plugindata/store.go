// Package plugindata 是插件桥**数据能力端点**的宿主实现（SANDBOX-WRITE-1 P6 / 方案 §9.1，2026-11-03）。
//
// 契约（C8 / C9 / C18）：插件与任务脚本**不得直接打开 bbolt 库**（`<work_dir>/.chonkpilot/**/*.db`）；
// 键值、自定义桶、项目用户枚举与会话导出一律经本包，由宿主完成读 / 写 / 建桶 / 审计。
//
// 键空间隔离 = **(插件 id, work_dir, user)**：
//   - 插件 id 取自插件桥会话身份（`plugin.hello` 自报，调用方不可伪造）；
//   - `work_dir` 必填（不依赖「当前进程恰好属于谁」）；
//   - `user` 缺省 = 当前 OS 用户；**显式跨用户 = 仅「历史记录读取」类放行**（G-102，2026-11-03
//     用户拍板：`session.get` / `session.history` / `session.export`，判定落点 = `resolveUser`）；
//     KV / 桶的读、写跨用户一律 `auth_required`。宿主注入 `Options.Authorize` 返回 true 亦可
//     作为宿主级显式追加授权（两种放行都写审计）。
//
// 落盘位置（**不污染会话库**）：
//
//	<Root>/<plugin_id>/kv/<hash(work_dir)>/<user>/<key>.json        // 键值（值 = 任意 JSON，≤64 KiB；含可选到期时间）
//	<Root>/<plugin_id>/buckets/<hash(work_dir)>/<user>/<name>/      // 自定义桶（含 _bucket.json 元数据 + records.jsonl 记录）
//	<Root>/_audit/audit.jsonl                                       // 审计（JSONL，追加写；超 8 MiB 轮转为 .1/.2/.3）
//
// `Root` = 插件平台数据根（`<DataDir>/plugins`，与插件私有 storage 同级）。
package plugindata

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	wslayout "github.com/chonkpilot/chonkpilot-workspace/layout"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// auditDir / auditFile 是审计落盘位置（相对 Options.Root）。
const (
	auditDir  = "_audit"
	auditFile = "audit.jsonl"

	kvDirName     = "kv"
	bucketDirName = "buckets"
	scopeFileName = "_scope.json"
	bucketMeta    = "_bucket.json"
	// bucketRecordsFile 是桶内**有序记录**文件（G-104：一行一条 JSON，追加写）。
	bucketRecordsFile = "records.jsonl"

	// kvEnvelopeMarker 是 TTL 落盘信封的标记字段（`{"__ck_kv":1,"value":...,"expires_at":...}`）。
	// 旧数据（无该标记）= 裸 JSON 值 = 永不过期（向后兼容）。
	kvEnvelopeMarker = "__ck_kv"

	// auditMaxBytes / auditMaxArchives 是审计轮转口径（G-102 技术部分）：
	// 写入前检查大小，超过 8 MiB → 轮转为 `audit.jsonl.1`，最多保留 3 个归档（.1/.2/.3），
	// 超出删除最旧的（.3 → 删除后重新编号）。纯本地文件操作、幂等（未超限不动作）。
	auditMaxBytes    = 8 << 20
	auditMaxArchives = 3
)

// Options 是 Store 的构造参数。
type Options struct {
	// Root 是插件平台数据根（= `<DataDir>/plugins`）；必填。
	Root string
	// DataDir 是数据层分支判据（instance-register 的 data_dir）：desktop **恒空**。
	// 非空 = 共享数据根形态（prjusr 与 prj 同根，无 `users/` 分支）→ 跨用户枚举不支持。
	DataDir string
	// CurrentUser 是「当前 OS 用户」覆盖（测试隔离）；空 = `layout.UserDirName()`。
	CurrentUser string
	// UsersRoot 覆盖「某 work_dir 的 users 根」解析（测试隔离 / 非默认部署）；nil = 用
	// `layout.Of(workDir, "")` 的 `<root>/users`（唯一算法，含 SetUsrRootOverride）。
	UsersRoot func(workDir string) (string, error)
	// Authorize 是跨用户访问的**宿主级显式追加授权口**（G-102 内置矩阵之外，nil = 只按内置矩阵）。
	// 入参 = (action, plugin_id, actor_user, work_dir, target_user)；返回 true 才放行，调用方写审计。
	Authorize func(action, pluginID, actorUser, workDir, targetUser string) (bool, error)
	// Logf 诊断输出（可选）。
	Logf func(format string, args ...any)
}

// Store 实现 `pluginbridge.PluginDataHooks`。
type Store struct {
	opts Options

	auditMu sync.Mutex
}

// New 构造 Store（不做 IO）。
func New(o Options) *Store {
	o.Root = strings.TrimSpace(o.Root)
	o.DataDir = strings.TrimSpace(o.DataDir)
	o.CurrentUser = strings.TrimSpace(o.CurrentUser)
	return &Store{opts: o}
}

// CurrentUser 返回本机当前 OS 用户名（与 `users/<name>` 目录名同口径）。
func CurrentUser() string { return wslayout.UserDirName() }

// Hooks 返回可注入插件桥的数据能力实现。
//
// 未配置数据根（`Options.Root` 为空）→ 返回零值（全部 `method_not_found`）：
// 宁可「能力不可用」，也不把数据落到相对路径 / 当前工作目录。
func (s *Store) Hooks() pluginbridge.PluginDataHooks {
	if s.opts.Root == "" {
		return pluginbridge.PluginDataHooks{}
	}
	return pluginbridge.PluginDataHooks{
		KVGet:          s.kvGet,
		KVSet:          s.kvSet,
		KVDelete:       s.kvDelete,
		KVList:         s.kvList,
		KVDeletePrefix: s.kvDeletePrefix,
		BucketEnsure:   s.bucketEnsure,
		BucketList:     s.bucketList,
		BucketQuery:    s.bucketQuery,
		PrjUsersList:   s.prjUsersList,
		SessionExport:  s.sessionExport,
	}
}

// AuditPath 返回审计文件路径（测试 / 运维定位用）。
func (s *Store) AuditPath() string {
	return filepath.Join(s.opts.Root, auditDir, auditFile)
}

func (s *Store) currentUser() string {
	if s.opts.CurrentUser != "" {
		return s.opts.CurrentUser
	}
	return CurrentUser()
}

func (s *Store) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}

// ── 键空间 ───────────────────────────────

// scopeHash 把 work_dir 归一到稳定的目录名（大小写不敏感：Windows 路径同盘同目录）。
func scopeHash(workDir string) string {
	norm := strings.ToLower(filepath.Clean(workDir))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:8])
}

func (s *Store) kvDir(pluginID, workDir, user string) string {
	return filepath.Join(s.opts.Root, pluginID, kvDirName, scopeHash(workDir), user)
}

func (s *Store) bucketRoot(pluginID, workDir, user string) string {
	return filepath.Join(s.opts.Root, pluginID, bucketDirName, scopeHash(workDir), user)
}

// ── 授权（G-102：跨用户仅历史记录读取放行）──────────────

// isHistoryReadAction 判定动作是否属于「历史记录读取」类（G-102，2026-11-03 用户拍板的
// 跨用户唯一放行面）：`session.get` / `session.history`（经 `plugin/v1` 转发数据面，无 user
// 参数）与 `session.export`（本包实现）。KV / 桶的读、写**不属于**历史记录，跨用户一律拒绝。
func isHistoryReadAction(action string) bool {
	switch action {
	case "session.get", "session.history", "session.export":
		return true
	}
	return false
}

// resolveUser 是**跨用户授权矩阵的唯一判定落点**：缺省（显式 user 为空或等于当前用户）=
// 当前 OS 用户；显式跨用户 → 放行条件 =「历史记录读取类」（G-102）**或**宿主授权端口
// （`Options.Authorize`，nil = 无追加口）返回 true；无论放行 / 拒绝 / 端口报错都写审计。
func (s *Store) resolveUser(action, pluginID, workDir, explicit string) (string, error) {
	cur := s.currentUser()
	explicit = strings.TrimSpace(explicit)
	if explicit == "" || explicit == cur {
		return cur, nil
	}
	allowed := isHistoryReadAction(action)
	if !allowed && s.opts.Authorize != nil {
		ok, err := s.opts.Authorize(action, pluginID, cur, workDir, explicit)
		if err != nil {
			s.audit(auditRecord{
				Action: action, ActorUser: cur, Plugin: pluginID, WorkDir: workDir,
				TargetUser: explicit, Result: "error", Reason: err.Error(),
			})
			return "", &pluginbridge.Error{Code: pluginbridge.CodeInternal, Msg: err.Error()}
		}
		allowed = ok
	}
	if !allowed {
		s.audit(auditRecord{
			Action: action, ActorUser: cur, Plugin: pluginID, WorkDir: workDir,
			TargetUser: explicit, Result: "denied", Reason: "cross_user_not_history_read",
		})
		return "", &pluginbridge.Error{
			Code: pluginbridge.CodeAuthRequired,
			Msg:  "跨用户访问仅放行历史记录读取（G-102，2026-11-03）",
		}
	}
	s.audit(auditRecord{
		Action: action, ActorUser: cur, Plugin: pluginID, WorkDir: workDir,
		TargetUser: explicit, Result: "ok",
	})
	return explicit, nil
}

// ── data.kv.* ───────────────────────────

// kvRecord 是带 TTL 的落盘信封。旧数据（无 `__ck_kv` 标记）= 裸 JSON 值 = 永不过期。
type kvRecord struct {
	Marker    int             `json:"__ck_kv"`
	Value     json.RawMessage `json:"value"`
	ExpiresAt int64           `json:"expires_at,omitempty"` // Unix 秒；0 = 永不过期
}

// decodeKV 拆信封：旧格式（裸值）→ (raw, 0)；新格式 → (value, expires_at)。
func decodeKV(raw []byte) (json.RawMessage, int64) {
	var probe struct {
		Marker    *int            `json:"__ck_kv"`
		Value     json.RawMessage `json:"value"`
		ExpiresAt int64           `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.Marker != nil &&
		*probe.Marker == 1 && probe.Value != nil {
		return probe.Value, probe.ExpiresAt
	}
	return json.RawMessage(raw), 0
}

// expired 判断记录是否已过期（expires_at = 0 表示永不过期）。
func expired(expiresAt int64, now time.Time) bool {
	return expiresAt > 0 && now.Unix() >= expiresAt
}

func (s *Store) kvGet(pluginID, workDir, user, key string) (json.RawMessage, bool, error) {
	u, err := s.resolveUser("data.kv.get", pluginID, workDir, user)
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(s.kvDir(pluginID, workDir, u), key+".json")
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, false, nil
		}
		return nil, false, rerr
	}
	value, expiresAt := decodeKV(raw)
	if expired(expiresAt, time.Now()) {
		// 惰性清理：过期项按「不存在」返回（不是错误）。
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			s.logf("plugindata: 清理过期键失败 %s: %v", path, rmErr)
		}
		return nil, false, nil
	}
	return value, true, nil
}

func (s *Store) kvSet(pluginID, workDir, user, key string, value json.RawMessage, ttlSeconds int64) error {
	// 先做授权（跨用户尝试无论后续是否超限都要留审计），再判大小。
	u, err := s.resolveUser("data.kv.set", pluginID, workDir, user)
	if err != nil {
		return err
	}
	if len(value) > pluginbridge.MaxKVValueBytes {
		// 桥已预检；此处为直连实现（测试 / 未来内部调用）兜底 —— 明确拒绝，不截断。
		return &pluginbridge.Error{
			Code: pluginbridge.CodeValueTooLarge,
			Msg:  "value 超过上限 " + strconv.Itoa(pluginbridge.MaxKVValueBytes) + " 字节",
		}
	}
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	var expiresAt int64
	if ttlSeconds > 0 {
		now := time.Now().Unix()
		if ttlSeconds > math.MaxInt64-now {
			return &pluginbridge.Error{
				Code: pluginbridge.CodeArgsInvalid,
				Msg:  "ttl_seconds 过大（超出可表示时间范围）",
			}
		}
		expiresAt = now + ttlSeconds
	}
	raw, merr := json.Marshal(kvRecord{Marker: 1, Value: value, ExpiresAt: expiresAt})
	if merr != nil {
		return merr
	}
	dir := s.kvDir(pluginID, workDir, u)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeScope(dir, pluginID, workDir, u); err != nil {
		s.logf("plugindata: 写 scope 失败 %s: %v", dir, err)
	}
	return writeFileAtomic(filepath.Join(dir, key+".json"), raw)
}

func (s *Store) kvDelete(pluginID, workDir, user, key string) (bool, error) {
	u, err := s.resolveUser("data.kv.del", pluginID, workDir, user)
	if err != nil {
		return false, err
	}
	err = os.Remove(filepath.Join(s.kvDir(pluginID, workDir, u), key+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// kvDeletePrefix 删除**当前 (插件 id, work_dir, user) 命名空间内**匹配前缀的键（G-104）。
// 空前缀 = 明确拒绝（防误删全量）；返回删除条数。
func (s *Store) kvDeletePrefix(pluginID, workDir, user, prefix string) (int, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return 0, &pluginbridge.Error{
			Code: pluginbridge.CodeArgsInvalid,
			Msg:  "prefix 不能为空（防误删全量）",
		}
	}
	u, err := s.resolveUser("data.kv.del-prefix", pluginID, workDir, user)
	if err != nil {
		return 0, err
	}
	dir := s.kvDir(pluginID, workDir, u)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	deleted := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		k := strings.TrimSuffix(e.Name(), ".json")
		if k == strings.TrimSuffix(scopeFileName, ".json") {
			continue // 内部元数据不算用户键
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if rmErr := os.Remove(filepath.Join(dir, e.Name())); rmErr != nil {
			if os.IsNotExist(rmErr) {
				continue
			}
			return deleted, rmErr
		}
		deleted++
	}
	return deleted, nil
}

func (s *Store) kvList(pluginID, workDir, user, prefix string, limit int) ([]string, error) {
	u, err := s.resolveUser("data.kv.list", pluginID, workDir, user)
	if err != nil {
		return nil, err
	}
	dir := s.kvDir(pluginID, workDir, u)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	now := time.Now()
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		k := strings.TrimSuffix(e.Name(), ".json")
		if k == strings.TrimSuffix(scopeFileName, ".json") {
			continue // 内部元数据不算用户键
		}
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		// TTL：过期项不返回（并惰性清理）。
		if raw, rerr := os.ReadFile(filepath.Join(dir, e.Name())); rerr == nil {
			if _, expiresAt := decodeKV(raw); expired(expiresAt, now) {
				if rmErr := os.Remove(filepath.Join(dir, e.Name())); rmErr != nil && !os.IsNotExist(rmErr) {
					s.logf("plugindata: 清理过期键失败 %s: %v", e.Name(), rmErr)
				}
				continue
			}
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	return keys, nil
}

// ── data.bucket.* ───────────────────────

type bucketMetaFile struct {
	Name      string `json:"name"`
	WorkDir   string `json:"work_dir"`
	User      string `json:"user"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) bucketEnsure(pluginID, workDir, user, name string) (pluginbridge.BucketInfo, bool, error) {
	u, err := s.resolveUser("data.bucket.ensure", pluginID, workDir, user)
	if err != nil {
		return pluginbridge.BucketInfo{}, false, err
	}
	dir := filepath.Join(s.bucketRoot(pluginID, workDir, u), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return pluginbridge.BucketInfo{}, false, err
	}
	metaPath := filepath.Join(dir, bucketMeta)
	if raw, rerr := os.ReadFile(metaPath); rerr == nil {
		var m bucketMetaFile
		if json.Unmarshal(raw, &m) == nil {
			return pluginbridge.BucketInfo{Name: m.Name, CreatedAt: m.CreatedAt, CreatedBy: m.CreatedBy}, false, nil
		}
	}
	meta := bucketMetaFile{
		Name: name, WorkDir: filepath.Clean(workDir), User: u, CreatedBy: pluginID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeFileAtomic(metaPath, raw); err != nil {
		return pluginbridge.BucketInfo{}, false, err
	}
	// 建桶是**写入**动作（C18）→ 一律审计。
	s.audit(auditRecord{
		Action: "data.bucket.ensure", ActorUser: s.currentUser(), Plugin: pluginID,
		WorkDir: workDir, TargetUser: u, Bucket: name, Result: "ok",
	})
	return pluginbridge.BucketInfo{Name: meta.Name, CreatedAt: meta.CreatedAt, CreatedBy: meta.CreatedBy}, true, nil
}

func (s *Store) bucketList(pluginID, workDir, user string) ([]pluginbridge.BucketInfo, error) {
	u, err := s.resolveUser("data.bucket.list", pluginID, workDir, user)
	if err != nil {
		return nil, err
	}
	root := s.bucketRoot(pluginID, workDir, u)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []pluginbridge.BucketInfo{}, nil
		}
		return nil, err
	}
	out := make([]pluginbridge.BucketInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := pluginbridge.BucketInfo{Name: e.Name()}
		if raw, rerr := os.ReadFile(filepath.Join(root, e.Name(), bucketMeta)); rerr == nil {
			var m bucketMetaFile
			if json.Unmarshal(raw, &m) == nil {
				info.CreatedAt, info.CreatedBy = m.CreatedAt, m.CreatedBy
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// bucketQuery 桶内查询（G-104）：只读，按 `records.jsonl` 追加顺序返回，分页 + 可选前缀过滤。
// 桶不存在 → `not_found`（不静默回空结果）；空桶（无记录文件）→ 空列表 + total=0。
// 不写审计（除非跨用户，由 resolveUser 负责）。
func (s *Store) bucketQuery(req pluginbridge.BucketQueryRequest) (pluginbridge.BucketQueryResult, error) {
	u, err := s.resolveUser("data.bucket.query", req.PluginID, req.WorkDir, req.User)
	if err != nil {
		return pluginbridge.BucketQueryResult{}, err
	}
	dir := filepath.Join(s.bucketRoot(req.PluginID, req.WorkDir, u), req.Bucket)
	if st, serr := os.Stat(dir); serr != nil || !st.IsDir() {
		return pluginbridge.BucketQueryResult{}, &pluginbridge.Error{
			Code: pluginbridge.CodeNotFound,
			Msg:  "桶不存在: " + req.Bucket,
		}
	}
	records, err := readBucketRecords(filepath.Join(dir, bucketRecordsFile), s.logf)
	if err != nil {
		return pluginbridge.BucketQueryResult{}, err
	}
	matched := make([]pluginbridge.BucketRecord, 0, len(records))
	for _, rec := range records {
		if req.Prefix != "" && !strings.HasPrefix(bucketRecordKey(rec), req.Prefix) {
			continue
		}
		matched = append(matched, rec)
	}
	total := len(matched)

	limit := req.Limit
	if limit <= 0 {
		limit = pluginbridge.DefaultBucketQueryLimit
	}
	if limit > pluginbridge.MaxBucketQueryLimit {
		limit = pluginbridge.MaxBucketQueryLimit
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return pluginbridge.BucketQueryResult{Items: []pluginbridge.BucketRecord{}, Total: total}, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := make([]pluginbridge.BucketRecord, end-offset)
	copy(items, matched[offset:end])
	return pluginbridge.BucketQueryResult{Items: items, Total: total}, nil
}

// bucketRecordKey 是前缀过滤的作用对象：`key` 优先，缺省退回 `id`。
func bucketRecordKey(rec pluginbridge.BucketRecord) string {
	if rec.Key != "" {
		return rec.Key
	}
	return rec.ID
}

// readBucketRecords 读取 `records.jsonl`（一行一条 JSON；空行与坏行跳过并留诊断）。
// 文件不存在 = 空桶（返回空切片，不是错误）。
func readBucketRecords(path string, logf func(string, ...any)) ([]pluginbridge.BucketRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []pluginbridge.BucketRecord{}, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// 单条记录可含 64 KiB 量级的值 → 放宽 token 上限（默认 64 KiB 不够）。
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	out := make([]pluginbridge.BucketRecord, 0, 64)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec pluginbridge.BucketRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			if logf != nil {
				logf("plugindata: 桶记录行非法（跳过）%s: %v", path, err)
			}
			continue
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ── prj.users.list（C16；只读元数据）────────

// usersRoot 返回 `<work_dir>/.chonkpilot/users`（唯一算法 = `layout.Of`，含 SetUsrRootOverride）。
// 共享数据根形态（DataDir 非空）没有 users/ 分支 → 不支持跨用户枚举。
func (s *Store) usersRoot(workDir string) (string, error) {
	if s.opts.UsersRoot != nil {
		return s.opts.UsersRoot(workDir)
	}
	if s.opts.DataDir != "" {
		return "", &pluginbridge.Error{
			Code: pluginbridge.CodeUnsupported,
			Msg:  "共享数据根形态（data_dir 非空）无 users/ 分支，不支持跨用户枚举",
		}
	}
	return filepath.Dir(wslayout.Of(workDir, "").PrjUsrDir), nil
}

func (s *Store) prjUsersList(pluginID, workDir string) ([]pluginbridge.PrjUser, error) {
	root, err := s.usersRoot(workDir)
	if err != nil {
		return nil, err
	}
	entries, rerr := os.ReadDir(root)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			s.audit(auditRecord{
				Action: "prj.users.list", ActorUser: s.currentUser(), Plugin: pluginID,
				WorkDir: workDir, Result: "ok", Count: 0,
			})
			return []pluginbridge.PrjUser{}, nil
		}
		return nil, rerr
	}
	users := make([]pluginbridge.PrjUser, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		u := pluginbridge.PrjUser{User: e.Name()}
		var lastMod time.Time
		if st, serr := os.Stat(filepath.Join(dir, "chonkpilot.db")); serr == nil {
			u.DBPresent = true
			lastMod = st.ModTime()
		}
		// 只数对话库文件（不打开、不读内容）。
		if ds, derr := os.ReadDir(filepath.Join(dir, "dialogs")); derr == nil {
			for _, de := range ds {
				if de.IsDir() || !strings.HasSuffix(de.Name(), ".db") {
					continue
				}
				u.DialogCount++
				if info, ierr := de.Info(); ierr == nil && info.ModTime().After(lastMod) {
					lastMod = info.ModTime()
				}
			}
		}
		if !lastMod.IsZero() {
			u.LastActive = lastMod.UTC().Format(time.RFC3339)
		}
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].User < users[j].User })
	s.audit(auditRecord{
		Action: "prj.users.list", ActorUser: s.currentUser(), Plugin: pluginID,
		WorkDir: workDir, Result: "ok", Count: len(users),
	})
	return users, nil
}

// ── session.export（G-103，2026-11-03 已实施）──────

// sessionExport 会话只读导出：授权经 `resolveUser`（跨用户属历史记录读取 → G-102 放行 + 审计），
// 读取经数据层既有公开 API（见 export.go），导出体**内联返回**（不落文件、不建导出目录）。
// 成功落一条 `ok` 审计（谁导出了哪个 work_dir / 用户的会话）。
func (s *Store) sessionExport(req pluginbridge.SessionExportRequest) (any, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, &pluginbridge.Error{
			Code: pluginbridge.CodeArgsInvalid,
			Msg:  "session.export: session_id 必填",
		}
	}
	u, err := s.resolveUser("session.export", req.PluginID, req.WorkDir, req.User)
	if err != nil {
		return nil, err
	}
	body, err := s.readSessionExport(req.WorkDir, u, req.SessionID)
	if err != nil {
		return nil, err
	}
	s.audit(auditRecord{
		Action: "session.export", ActorUser: s.currentUser(), Plugin: req.PluginID,
		WorkDir: req.WorkDir, TargetUser: u, Result: "ok",
	})
	return json.RawMessage(body), nil
}

// ── 审计 ───────────────────────────────

type auditRecord struct {
	At         string `json:"at"`
	Action     string `json:"action"`
	ActorUser  string `json:"actor_user"`
	Plugin     string `json:"plugin,omitempty"`
	WorkDir    string `json:"work_dir,omitempty"`
	TargetUser string `json:"target_user,omitempty"`
	Bucket     string `json:"bucket,omitempty"`
	Result     string `json:"result"`
	Reason     string `json:"reason,omitempty"`
	Count      int    `json:"count,omitempty"`
}

// audit 追加一条审计记录（JSONL）。失败只留诊断，不影响主流程（审计不阻断能力）。
// 写入前检查大小：超过 `auditMaxBytes` → 先轮转（见 rotateAuditLocked）。
func (s *Store) audit(rec auditRecord) {
	if s.opts.Root == "" {
		return
	}
	rec.At = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	dir := filepath.Join(s.opts.Root, auditDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.logf("plugindata: 审计目录创建失败: %v", err)
		return
	}
	path := filepath.Join(dir, auditFile)
	s.rotateAuditLocked(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		s.logf("plugindata: 审计文件打开失败: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		s.logf("plugindata: 审计写入失败: %v", err)
	}
}

// rotateAuditLocked 是审计轮转（G-102 技术部分；调用方须持 auditMu）：
//
//   - 未超 `auditMaxBytes` → 什么都不做（幂等；重复调用无副作用）；
//   - 超过 → `audit.jsonl` → `audit.jsonl.1`，`.1` → `.2`，`.2` → `.3`，`.3` 删除（最多 3 个归档）；
//   - 纯本地文件操作（stat / rename / remove），失败只留诊断并继续写当前文件（不阻断能力）。
//
// **留存策略（是否可关闭 / 项目级授权）仍属 G-102 待拍板**，此处只做技术轮转。
func (s *Store) rotateAuditLocked(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= auditMaxBytes {
		return
	}
	archive := func(i int) string { return path + "." + strconv.Itoa(i) }
	if err := os.Remove(archive(auditMaxArchives)); err != nil && !os.IsNotExist(err) {
		s.logf("plugindata: 删除最旧审计归档失败: %v", err)
	}
	for i := auditMaxArchives - 1; i >= 1; i-- {
		if err := os.Rename(archive(i), archive(i+1)); err != nil && !os.IsNotExist(err) {
			s.logf("plugindata: 审计归档重命名失败 %s → %s: %v", archive(i), archive(i+1), err)
		}
	}
	if err := os.Rename(path, archive(1)); err != nil {
		s.logf("plugindata: 审计轮转失败: %v", err)
	}
}

// ── 文件工具 ────────────────────────────

func writeScope(dir, pluginID, workDir, user string) error {
	raw, _ := json.MarshalIndent(map[string]string{
		"plugin": pluginID, "work_dir": filepath.Clean(workDir), "user": user,
	}, "", "  ")
	return writeFileAtomic(filepath.Join(dir, scopeFileName), raw)
}

func writeFileAtomic(path string, raw []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}
