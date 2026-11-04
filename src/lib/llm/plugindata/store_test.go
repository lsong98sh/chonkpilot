package plugindata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

func newStore(t *testing.T, opts Options) *Store {
	t.Helper()
	opts.Root = filepath.Join(t.TempDir(), "plugins")
	if opts.CurrentUser == "" {
		opts.CurrentUser = "alice"
	}
	return New(opts)
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var e *pluginbridge.Error
	if ok := asBridgeError(err, &e); ok {
		return e.Code
	}
	return "other:" + err.Error()
}

func asBridgeError(err error, out **pluginbridge.Error) bool {
	for err != nil {
		if e, ok := err.(*pluginbridge.Error); ok {
			*out = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func readAudit(t *testing.T, s *Store) []auditRecord {
	t.Helper()
	raw, err := os.ReadFile(s.AuditPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("读审计: %v", err)
	}
	var out []auditRecord
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec auditRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("审计行非法: %s: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// TestKVScopeIsolation：键空间按 (插件 id, work_dir, user) 隔离；跨用户默认拒绝并审计。
func TestKVScopeIsolation(t *testing.T) {
	s := newStore(t, Options{})
	wdA, wdB := `E:\proj\a`, `E:\proj\b`

	if err := s.kvSet("p1", wdA, "", "k", json.RawMessage(`{"v":1}`), 0); err != nil {
		t.Fatalf("kvSet: %v", err)
	}
	v, found, err := s.kvGet("p1", wdA, "", "k")
	if err != nil || !found || string(v) != `{"v":1}` {
		t.Fatalf("同域读回不符: found=%v v=%s err=%v", found, v, err)
	}
	if _, found, _ := s.kvGet("p2", wdA, "", "k"); found {
		t.Fatal("跨插件不应可见")
	}
	if _, found, _ := s.kvGet("p1", wdB, "", "k"); found {
		t.Fatal("跨 work_dir 不应可见")
	}

	// 显式跨用户：无授权端口 → 默认拒绝 + 审计。
	if _, _, err := s.kvGet("p1", wdA, "bob", "k"); codeOf(t, err) != pluginbridge.CodeAuthRequired {
		t.Fatalf("跨用户读应回 auth_required: %v", err)
	}
	recs := readAudit(t, s)
	denied := 0
	for _, r := range recs {
		if r.Action == "data.kv.get" && r.TargetUser == "bob" && r.Result == "denied" {
			denied++
		}
	}
	if denied == 0 {
		t.Fatalf("跨用户拒绝未落审计: %+v", recs)
	}

	// 键前缀列举
	for _, k := range []string{"a-1", "a-2", "b-1"} {
		if err := s.kvSet("p1", wdA, "", k, json.RawMessage(`1`), 0); err != nil {
			t.Fatalf("kvSet %s: %v", k, err)
		}
	}
	keys, err := s.kvList("p1", wdA, "", "a-", 100)
	if err != nil || len(keys) != 2 || keys[0] != "a-1" || keys[1] != "a-2" {
		t.Fatalf("kvList = %v err=%v", keys, err)
	}
}

// TestKVValueLimit：值大小超限明确拒绝（不截断），上限内可正常读写。
func TestKVValueLimit(t *testing.T) {
	s := newStore(t, Options{})
	wd := `E:\proj\a`

	exact := json.RawMessage("\"" + strings.Repeat("x", pluginbridge.MaxKVValueBytes-2) + "\"")
	if len(exact) != pluginbridge.MaxKVValueBytes {
		t.Fatalf("夹具长度 %d != %d", len(exact), pluginbridge.MaxKVValueBytes)
	}
	if err := s.kvSet("p1", wd, "", "max", exact, 0); err != nil {
		t.Fatalf("上限内应成功: %v", err)
	}

	over := json.RawMessage("\"" + strings.Repeat("x", pluginbridge.MaxKVValueBytes-1) + "\"")
	if codeOf(t, s.kvSet("p1", wd, "", "max", over, 0)) != pluginbridge.CodeValueTooLarge {
		t.Fatal("超限应回 value_too_large")
	}
	// 未截断：旧值保持不变
	v, found, err := s.kvGet("p1", wd, "", "max")
	if err != nil || !found || len(v) != pluginbridge.MaxKVValueBytes {
		t.Fatalf("超限写不得截断/覆盖: len=%d found=%v err=%v", len(v), found, err)
	}
}

// TestBucketEnsureIdempotent：建桶幂等（第二次 created=false），且**每次新建**都写审计。
func TestBucketEnsureIdempotent(t *testing.T) {
	s := newStore(t, Options{})
	wd := `E:\proj\a`

	b1, created, err := s.bucketEnsure("p1", wd, "", "kb")
	if err != nil || !created || b1.Name != "kb" || b1.CreatedBy != "p1" {
		t.Fatalf("首次建桶: %+v created=%v err=%v", b1, created, err)
	}
	b2, created2, err := s.bucketEnsure("p1", wd, "", "kb")
	if err != nil || created2 {
		t.Fatalf("重复建桶应幂等: %+v created=%v err=%v", b2, created2, err)
	}
	if b1.CreatedAt != b2.CreatedAt {
		t.Fatalf("幂等应回同一元数据: %q vs %q", b1.CreatedAt, b2.CreatedAt)
	}
	list, err := s.bucketList("p1", wd, "")
	if err != nil || len(list) != 1 || list[0].Name != "kb" {
		t.Fatalf("bucketList = %+v err=%v", list, err)
	}
	creates := 0
	for _, r := range readAudit(t, s) {
		if r.Action == "data.bucket.ensure" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("应只审计 1 次建桶（幂等第二次不写），实际 %d", creates)
	}
}

// TestPrjUsersListMetaOnly：只返回用户标识与基础元数据，不读 / 不返回会话内容。
func TestPrjUsersListMetaOnly(t *testing.T) {
	s := newStore(t, Options{})
	wd := t.TempDir()
	usersRoot := filepath.Join(wd, ".chonkpilot", "users")
	mustWrite(t, filepath.Join(usersRoot, "alice", "chonkpilot.db"), "alice-db")
	mustWrite(t, filepath.Join(usersRoot, "alice", "dialogs", "dialog-01.db"), "SECRET-SESSION-CONTENT")
	mustWrite(t, filepath.Join(usersRoot, "alice", "dialogs", "dialog-02.db"), "more")
	if err := os.MkdirAll(filepath.Join(usersRoot, "bob"), 0o755); err != nil {
		t.Fatalf("mkdir bob: %v", err)
	}

	users, err := s.prjUsersList("p1", wd)
	if err != nil {
		t.Fatalf("prjUsersList: %v", err)
	}
	if len(users) != 2 || users[0].User != "alice" || users[1].User != "bob" {
		t.Fatalf("users = %+v", users)
	}
	if !users[0].DBPresent || users[0].DialogCount != 2 || users[0].LastActive == "" {
		t.Fatalf("alice 元数据不符: %+v", users[0])
	}
	if users[1].DBPresent || users[1].DialogCount != 0 || users[1].LastActive != "" {
		t.Fatalf("bob 元数据不符: %+v", users[1])
	}
	raw, _ := json.Marshal(users)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("用户列表泄露会话内容: %s", raw)
	}

	recs := readAudit(t, s)
	hit := false
	for _, r := range recs {
		if r.Action == "prj.users.list" && r.Count == 2 && r.Plugin == "p1" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("prj.users.list 未落审计: %+v", recs)
	}
}

// TestCrossUserAuthorized：注入授权端口后放行，并写「允许」审计（不放开任何会话内容读）。
func TestCrossUserAuthorized(t *testing.T) {
	var calls int
	s := newStore(t, Options{Authorize: func(action, pluginID, actor, workDir, target string) (bool, error) {
		calls++
		return action == "data.kv.get" && target == "bob", nil
	}})
	if _, found, err := s.kvGet("p1", `E:\proj\a`, "bob", "k"); err != nil || found {
		t.Fatalf("授权后读取（键不存在）: found=%v err=%v", found, err)
	}
	if calls != 1 {
		t.Fatalf("授权端口调用次数 = %d", calls)
	}
	recs := readAudit(t, s)
	if len(recs) != 1 || recs[0].Result != "ok" || recs[0].TargetUser != "bob" {
		t.Fatalf("放行未写审计: %+v", recs)
	}
}

// TestDataDirSharedRootUnsupported：共享数据根形态（data_dir 非空）不支持跨用户枚举。
func TestDataDirSharedRootUnsupported(t *testing.T) {
	s := newStore(t, Options{DataDir: `E:\proj\.chonkpilot`})
	if _, err := s.prjUsersList("p1", `E:\proj\a`); codeOf(t, err) != pluginbridge.CodeUnsupported {
		t.Fatalf("应回 unsupported: %v", err)
	}
}

// TestEmptyRootDisablesCapabilities：数据根未配置 → 能力整体不可用（不落到相对路径 / CWD）。
func TestEmptyRootDisablesCapabilities(t *testing.T) {
	h := New(Options{CurrentUser: "alice"}).Hooks()
	if h.KVGet != nil || h.KVSet != nil || h.PrjUsersList != nil || h.SessionExport != nil {
		t.Fatal("Root 为空时必须返回零值 hooks（全部 method_not_found）")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ── G-104：KV TTL / 前缀批量删除 / 桶查询 ─────────────────────────────

// TestKVTTLExpiry：设置 → 未过期可读 → 过期后 get 回 found=false（按不存在，不是错误）、
// list 不返回，且过期项被惰性清理；无 TTL 的键不受影响。
func TestKVTTLExpiry(t *testing.T) {
	s := newStore(t, Options{})
	wd := `E:\proj\a`

	if err := s.kvSet("p1", wd, "", "temp", json.RawMessage(`{"v":1}`), 1); err != nil {
		t.Fatalf("kvSet(ttl=1): %v", err)
	}
	if err := s.kvSet("p1", wd, "", "temp2", json.RawMessage(`2`), 1); err != nil {
		t.Fatalf("kvSet(temp2): %v", err)
	}
	if err := s.kvSet("p1", wd, "", "keep", json.RawMessage(`3`), 0); err != nil {
		t.Fatalf("kvSet(keep): %v", err)
	}

	// 未过期：可读、列表可见。
	if v, found, err := s.kvGet("p1", wd, "", "temp"); err != nil || !found || string(v) != `{"v":1}` {
		t.Fatalf("未过期应可读: found=%v v=%s err=%v", found, v, err)
	}
	if keys, _ := s.kvList("p1", wd, "", "", 100); len(keys) != 3 {
		t.Fatalf("未过期列表应含 3 个键: %v", keys)
	}

	time.Sleep(1100 * time.Millisecond)

	// 过期：get 按不存在返回（不报错），并惰性删除文件。
	if _, found, err := s.kvGet("p1", wd, "", "temp"); err != nil || found {
		t.Fatalf("过期后应 found=false 且无错误: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(filepath.Join(s.kvDir("p1", wd, "alice"), "temp.json")); !os.IsNotExist(err) {
		t.Fatalf("过期项应被惰性清理: err=%v", err)
	}

	// list 不返回过期项（并清理 temp2）；无 TTL 的 keep 仍在。
	keys, err := s.kvList("p1", wd, "", "", 100)
	if err != nil {
		t.Fatalf("kvList: %v", err)
	}
	if len(keys) != 1 || keys[0] != "keep" {
		t.Fatalf("过期项不应出现在列表: %v", keys)
	}
	if _, err := os.Stat(filepath.Join(s.kvDir("p1", wd, "alice"), "temp2.json")); !os.IsNotExist(err) {
		t.Fatalf("list 应惰性清理过期项: err=%v", err)
	}
}

// TestKVLegacyNoExpiry：旧数据（无 TTL 信封，裸 JSON 值）= 永不过期（向后兼容）。
func TestKVLegacyNoExpiry(t *testing.T) {
	s := newStore(t, Options{})
	wd := `E:\proj\a`
	dir := s.kvDir("p1", wd, "alice")
	mustWrite(t, filepath.Join(dir, "legacy.json"), `{"old":true}`)

	v, found, err := s.kvGet("p1", wd, "", "legacy")
	if err != nil || !found || string(v) != `{"old":true}` {
		t.Fatalf("旧数据应可读且无到期: found=%v v=%s err=%v", found, v, err)
	}
	keys, err := s.kvList("p1", wd, "", "", 100)
	if err != nil || len(keys) != 1 || keys[0] != "legacy" {
		t.Fatalf("旧数据应出现在列表: %v err=%v", keys, err)
	}
}

// TestKVDeletePrefix：只删当前 (插件 id, work_dir, user) 命名空间内匹配前缀的键；
// 计数正确；空前缀明确拒绝（防误删全量）。
func TestKVDeletePrefix(t *testing.T) {
	s := newStore(t, Options{})
	wdA, wdB := `E:\proj\a`, `E:\proj\b`
	for _, k := range []string{"a-1", "a-2", "b-1"} {
		if err := s.kvSet("p1", wdA, "", k, json.RawMessage(`1`), 0); err != nil {
			t.Fatalf("kvSet %s: %v", k, err)
		}
	}
	if err := s.kvSet("p2", wdA, "", "a-9", json.RawMessage(`1`), 0); err != nil {
		t.Fatalf("kvSet p2: %v", err)
	}
	if err := s.kvSet("p1", wdB, "", "a-9", json.RawMessage(`1`), 0); err != nil {
		t.Fatalf("kvSet wdB: %v", err)
	}

	n, err := s.kvDeletePrefix("p1", wdA, "", "a-")
	if err != nil || n != 2 {
		t.Fatalf("删除条数 = %d err=%v（应 2）", n, err)
	}
	if keys, _ := s.kvList("p1", wdA, "", "", 100); len(keys) != 1 || keys[0] != "b-1" {
		t.Fatalf("本命名空间剩余键不符: %v", keys)
	}
	// 其它插件 / work_dir 命名空间不受影响。
	if keys, _ := s.kvList("p2", wdA, "", "", 100); len(keys) != 1 || keys[0] != "a-9" {
		t.Fatalf("跨插件不应被删: %v", keys)
	}
	if keys, _ := s.kvList("p1", wdB, "", "", 100); len(keys) != 1 || keys[0] != "a-9" {
		t.Fatalf("跨 work_dir 不应被删: %v", keys)
	}
	if n, err := s.kvDeletePrefix("p1", wdA, "", "zzz-"); err != nil || n != 0 {
		t.Fatalf("无命中应回 0: n=%d err=%v", n, err)
	}
	if _, err := s.kvDeletePrefix("p1", wdA, "", ""); codeOf(t, err) != pluginbridge.CodeArgsInvalid {
		t.Fatalf("空前缀应回 args_invalid: %v", err)
	}
}

// writeBucketRecords 直接按形状（一行一条 JSON，追加写）造桶记录（本轮只提供只读查询）。
func writeBucketRecords(t *testing.T, s *Store, pluginID, workDir, user, bucket string, lines ...string) {
	t.Helper()
	dir := filepath.Join(s.bucketRoot(pluginID, workDir, user), bucket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir bucket: %v", err)
	}
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, bucketRecordsFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write records: %v", err)
	}
}

// TestBucketQueryPaginationAndPrefix：分页（offset/limit）、前缀过滤、坏行跳过、空桶、
// 桶不存在 → not_found。
func TestBucketQueryPaginationAndPrefix(t *testing.T) {
	s := newStore(t, Options{})
	wd := `E:\proj\a`
	if _, _, err := s.bucketEnsure("p1", wd, "", "kb"); err != nil {
		t.Fatalf("bucketEnsure: %v", err)
	}
	writeBucketRecords(t, s, "p1", wd, "alice", "kb",
		`{"id":"r1","key":"alpha-1","value":{"n":1},"created_at":"2026-11-03T00:00:00Z"}`,
		`{"id":"r2","key":"alpha-2","value":{"n":2}}`,
		`not-json`,
		`{"id":"r3","key":"beta-1","value":{"n":3}}`,
		`{"id":"r4","value":{"n":4}}`,
		`{"id":"r5","key":"beta-2","value":{"n":5}}`,
		`{"id":"r6","key":"beta-3","value":{"n":6}}`,
	)

	// 缺省 limit（0 → 50）：全部 6 条（坏行跳过），按追加顺序。
	res, err := s.bucketQuery(pluginbridge.BucketQueryRequest{PluginID: "p1", WorkDir: wd, Bucket: "kb"})
	if err != nil || res.Total != 6 || len(res.Items) != 6 {
		t.Fatalf("全量查询 = total=%d items=%d err=%v", res.Total, len(res.Items), err)
	}
	if res.Items[0].ID != "r1" || res.Items[5].ID != "r6" || string(res.Items[0].Value) != `{"n":1}` {
		t.Fatalf("顺序 / 值不符: %+v", res.Items)
	}

	// 分页：offset=2 limit=2 → r3、r4；total 恒为分页前命中数。
	res, err = s.bucketQuery(pluginbridge.BucketQueryRequest{
		PluginID: "p1", WorkDir: wd, Bucket: "kb", Limit: 2, Offset: 2,
	})
	if err != nil || res.Total != 6 || len(res.Items) != 2 || res.Items[0].ID != "r3" || res.Items[1].ID != "r4" {
		t.Fatalf("分页不符: %+v err=%v", res, err)
	}

	// 前缀过滤（作用于 key）：beta- 命中 3 条。
	res, err = s.bucketQuery(pluginbridge.BucketQueryRequest{
		PluginID: "p1", WorkDir: wd, Bucket: "kb", Prefix: "beta-",
	})
	if err != nil || res.Total != 3 || len(res.Items) != 3 {
		t.Fatalf("前缀过滤不符: total=%d items=%d err=%v", res.Total, len(res.Items), err)
	}
	// key 缺省的记录按 id 过滤。
	res, err = s.bucketQuery(pluginbridge.BucketQueryRequest{
		PluginID: "p1", WorkDir: wd, Bucket: "kb", Prefix: "r4",
	})
	if err != nil || res.Total != 1 || len(res.Items) != 1 || res.Items[0].ID != "r4" {
		t.Fatalf("id 回退过滤不符: %+v err=%v", res, err)
	}

	// offset 越界 → 空列表（非 nil）+ total 不变。
	res, err = s.bucketQuery(pluginbridge.BucketQueryRequest{
		PluginID: "p1", WorkDir: wd, Bucket: "kb", Limit: 3, Offset: 99,
	})
	if err != nil || res.Total != 6 || res.Items == nil || len(res.Items) != 0 {
		t.Fatalf("越界分页不符: %+v err=%v", res, err)
	}

	// 空桶（无 records.jsonl）→ 空列表 + total=0。
	if _, _, err := s.bucketEnsure("p1", wd, "", "empty"); err != nil {
		t.Fatalf("bucketEnsure(empty): %v", err)
	}
	res, err = s.bucketQuery(pluginbridge.BucketQueryRequest{PluginID: "p1", WorkDir: wd, Bucket: "empty"})
	if err != nil || res.Total != 0 || res.Items == nil || len(res.Items) != 0 {
		t.Fatalf("空桶不符: %+v err=%v", res, err)
	}

	// 桶不存在 → not_found（不静默回空）。
	if _, err := s.bucketQuery(pluginbridge.BucketQueryRequest{
		PluginID: "p1", WorkDir: wd, Bucket: "nope",
	}); codeOf(t, err) != pluginbridge.CodeNotFound {
		t.Fatalf("桶不存在应回 not_found: %v", err)
	}
}

// TestBucketQueryLimitCap：limit 上限 500（实现侧兜底，桥侧同口径）。
func TestBucketQueryLimitCap(t *testing.T) {
	s := newStore(t, Options{})
	wd := `E:\proj\a`
	if _, _, err := s.bucketEnsure("p1", wd, "", "big"); err != nil {
		t.Fatalf("bucketEnsure: %v", err)
	}
	lines := make([]string, 600)
	for i := range lines {
		lines[i] = `{"id":"r` + strconv.Itoa(i) + `","key":"k","value":` + strconv.Itoa(i) + `}`
	}
	writeBucketRecords(t, s, "p1", wd, "alice", "big", lines...)

	res, err := s.bucketQuery(pluginbridge.BucketQueryRequest{
		PluginID: "p1", WorkDir: wd, Bucket: "big", Limit: 100000,
	})
	if err != nil || res.Total != 600 || len(res.Items) != pluginbridge.MaxBucketQueryLimit {
		t.Fatalf("limit 应封顶 %d: total=%d items=%d err=%v",
			pluginbridge.MaxBucketQueryLimit, res.Total, len(res.Items), err)
	}
}

// TestAuditRotation：超限触发轮转、最多保留 3 个归档、重复调用幂等（未超限不动作）。
func TestAuditRotation(t *testing.T) {
	s := newStore(t, Options{})
	path := s.AuditPath()
	fillOverLimit := func() {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir audit: %v", err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat("x", auditMaxBytes+1)), 0o644); err != nil {
			t.Fatalf("fill audit: %v", err)
		}
	}
	archiveExists := func(i int) bool {
		_, err := os.Stat(path + "." + strconv.Itoa(i))
		return err == nil
	}

	// 首次：超限 → 轮转为 .1；当前文件只含新记录。
	fillOverLimit()
	s.audit(auditRecord{Action: "rotate-1", Result: "ok"})
	if !archiveExists(1) {
		t.Fatal("超限应轮转为 audit.jsonl.1")
	}
	recs := readAudit(t, s)
	if len(recs) != 1 || recs[0].Action != "rotate-1" {
		t.Fatalf("轮转后当前文件应只含新记录: %+v", recs)
	}

	// 幂等：未超限时重复写不产生新归档。
	s.audit(auditRecord{Action: "rotate-2", Result: "ok"})
	if archiveExists(2) {
		t.Fatal("未超限不应再次轮转")
	}
	if recs := readAudit(t, s); len(recs) != 2 {
		t.Fatalf("当前文件应有 2 条: %+v", recs)
	}

	// 连续轮转：最多保留 3 个归档（.1/.2/.3），.4 不出现。
	for i := 0; i < 4; i++ {
		fillOverLimit()
		s.audit(auditRecord{Action: "rotate-more", Result: "ok"})
	}
	for i := 1; i <= auditMaxArchives; i++ {
		if !archiveExists(i) {
			t.Fatalf("应保留归档 audit.jsonl.%d", i)
		}
	}
	if archiveExists(auditMaxArchives + 1) {
		t.Fatalf("归档数超过 %d", auditMaxArchives)
	}
}
