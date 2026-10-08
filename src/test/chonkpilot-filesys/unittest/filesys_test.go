// filesys.* 服务单测（61-消息一览 §2）：总线直驱 Filesys 组件，t.TempDir() 作 work_dir。
// 黑盒形态（外部测试模块）：经公开 API filesys.New/Start/Stop + chonk. 前缀内存总线驱动，
// 错误 code 经 reflect 从私有 mapErr{code,message} 的底层 map 提取。
// 覆盖 list/content 查询、create/mkdir/remove/rename/copy（含 dest_dir/new_name 改名复制）写操作、
// 错误形态与 watch → filesys.changed 广播（真实 fsnotify 事件）。
package filesys_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"

	"github.com/chonkpilot/chonkpilot-filesys"
)

// maxTextBytes 对齐 filesys.go content 截断阈值（黑盒不可见，本地常量同步）。
const maxTextBytes = 512 * 1024

// testInstanceID 是测试实例标识：filesys 的基目录只认「instance → work_dir」绑定
// （instance-register 登记，G-21），故请求必须先注册实例、且载荷携带该 instance_id。
const testInstanceID = "ins-filesys-test"

// newTestFilesys 建独立环境：chonk. 前缀内存总线 + 已启动的 Filesys（t.Cleanup 收尾：先 Stop 后 Close）。
// 启动后立即注册实例（instance-register{instance_id, work_dir}）——filesys 自持该绑定解析 work_dir。
func newTestFilesys(t *testing.T) (mq.Bus, *filesys.Filesys, string) {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	svc := filesys.New(bus)
	if err := svc.Start(); err != nil {
		t.Fatalf("filesys.Start: %v", err)
	}
	t.Cleanup(svc.Stop)
	wd := t.TempDir()
	registerInstance(t, bus, testInstanceID, wd)
	return bus, svc, wd
}

// registerInstance 发布 instance-register（61-消息一览 §4.1）绑定 instance → work_dir。
func registerInstance(t *testing.T, bus mq.Bus, instanceID, workDir string) {
	t.Helper()
	bus.Emit(context.Background(), "instance-register", map[string]any{
		"instance_id": instanceID,
		"work_dir":    workDir,
	}).Wait()
}

// call 发 filesys.* 请求（请求-响应：Wait 收集 handler 写回的 Result/Errors）。
// instance_id 缺省补测试实例（filesys 只按绑定解析 work_dir；载荷 work_dir 不再被采信）。
func call(bus mq.Bus, subject string, payload map[string]any) *mq.Value {
	if _, ok := payload["instance_id"]; !ok {
		payload["instance_id"] = testInstanceID
	}
	return bus.Emit(context.Background(), subject, payload).Wait()
}

// mustOK 断言无错误并返回 Result map。
func mustOK(t *testing.T, subject string, v *mq.Value) map[string]any {
	t.Helper()
	if len(v.Errors) != 0 {
		t.Fatalf("%s 应成功但 errors=%v", subject, v.Errors)
	}
	res, ok := v.Result.(map[string]any)
	if !ok {
		t.Fatalf("%s result 非 map: %#v", subject, v.Result)
	}
	return res
}

// errCode 提取 filesys 错误 code（黑盒：错误动态类型为私有 map[string]string{code,message}，
// 经 reflect 遍历底层 map 取 code 键）。
func errCode(err error) string {
	rv := reflect.ValueOf(err)
	if rv.IsValid() && rv.Kind() == reflect.Map {
		for _, k := range rv.MapKeys() {
			if k.Kind() == reflect.String && k.String() == "code" {
				if v := rv.MapIndex(k); v.IsValid() && v.Kind() == reflect.String {
					return v.String()
				}
			}
		}
	}
	return ""
}

// wantErrCode 断言 v.Errors 首个错误 code。
func wantErrCode(t *testing.T, v *mq.Value, code string) {
	t.Helper()
	if len(v.Errors) == 0 {
		t.Fatalf("期望错误 code=%s 但无错误", code)
	}
	if got := errCode(v.Errors[0]); got != code {
		t.Fatalf("错误 code=%s（期望 %s），err=%v", got, code, v.Errors[0])
	}
}

// TestListEmptyAndPopulated：空目录 children=空数组；文件/子目录出现、隐藏点文件被跳过。
func TestListEmptyAndPopulated(t *testing.T) {
	bus, _, wd := newTestFilesys(t)

	res := mustOK(t, "filesys.list", call(bus, "filesys.list", map[string]any{"work_dir": wd}))
	if res["is_dir"] != true || res["path"] != wd {
		t.Fatalf("list root=%#v", res)
	}
	if kids, ok := res["children"].([]map[string]any); !ok || len(kids) != 0 {
		t.Fatalf("空目录 children 应为空数组: %#v", res["children"])
	}

	// 建 2 文件 + 1 子目录 + 1 隐藏点文件（list 应跳过隐藏）
	for _, f := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(wd, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(wd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	res = mustOK(t, "filesys.list", call(bus, "filesys.list", map[string]any{"work_dir": wd, "path": "."}))
	kids, _ := res["children"].([]map[string]any)
	if len(kids) != 3 {
		t.Fatalf("children 应 3 项（隐藏跳过）: %#v", res["children"])
	}
	byName := map[string]map[string]any{}
	for _, n := range kids {
		nm, _ := n["name"].(string)
		p, _ := n["path"].(string)
		mt, _ := n["mtime"].(string)
		if nm == "" || p == "" || mt == "" {
			t.Fatalf("节点缺 name/path/mtime: %#v", n)
		}
		if _, ok := n["is_dir"]; !ok {
			t.Fatalf("节点缺 is_dir: %#v", n)
		}
		if _, ok := n["size"]; !ok {
			t.Fatalf("节点缺 size: %#v", n)
		}
		byName[nm] = n
	}
	if _, hit := byName[".hidden"]; hit {
		t.Fatal("隐藏点文件不应出现在 children")
	}
	for _, want := range []string{"a.txt", "b.txt", "sub"} {
		n, hit := byName[want]
		if !hit {
			t.Fatalf("children 缺 %s: %#v", want, byName)
		}
		if want == "sub" && n["is_dir"] != true {
			t.Fatalf("sub 应为目录: %#v", n)
		}
	}
	if p, _ := byName["a.txt"]["path"].(string); !strings.HasPrefix(p, filepath.ToSlash(wd)) {
		t.Fatalf("node path=%s 应在 work_dir 内", p)
	}
}

// TestContentReadBackAndTruncated：原文读回 truncated=false；>512KB（maxTextBytes）截断 truncated=true。
func TestContentReadBackAndTruncated(t *testing.T) {
	bus, _, wd := newTestFilesys(t)

	text := "你好 filesys ✓\nline2"
	if err := os.WriteFile(filepath.Join(wd, "hello.txt"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	res := mustOK(t, "filesys.content", call(bus, "filesys.content", map[string]any{"work_dir": wd, "path": "hello.txt"}))
	if res["kind"] != "file" || res["content"] != text || res["truncated"] != false {
		t.Fatalf("content=%#v", res)
	}

	big := strings.Repeat("x", maxTextBytes+4096)
	if err := os.WriteFile(filepath.Join(wd, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	res = mustOK(t, "filesys.content", call(bus, "filesys.content", map[string]any{"work_dir": wd, "path": "big.txt"}))
	content, _ := res["content"].(string)
	if res["truncated"] != true || len(content) != maxTextBytes {
		t.Fatalf("大文件应截断到 %d 字节: len=%d truncated=%v", maxTextBytes, len(content), res["truncated"])
	}
}

// TestWriteOps：create/mkdir/remove/rename/copy/duplicate 正常路径（{ok:true,path} + 落盘断言）。
func TestWriteOps(t *testing.T) {
	bus, _, wd := newTestFilesys(t)

	// create 新文件
	r := mustOK(t, "filesys.create", call(bus, "filesys.create", map[string]any{
		"work_dir": wd, "dir": ".", "name": "hello.txt", "content": "hello filesys",
	}))
	if r["ok"] != true || r["path"] != filepath.Join(wd, "hello.txt") {
		t.Fatalf("create result=%#v", r)
	}
	if b, err := os.ReadFile(filepath.Join(wd, "hello.txt")); err != nil || string(b) != "hello filesys" {
		t.Fatalf("create 落盘不符: %v %q", err, b)
	}

	// mkdir
	mustOK(t, "filesys.mkdir", call(bus, "filesys.mkdir", map[string]any{"work_dir": wd, "name": "sub"}))
	if fi, err := os.Stat(filepath.Join(wd, "sub")); err != nil || !fi.IsDir() {
		t.Fatalf("mkdir 未生效: %v", err)
	}

	// remove（相对 path）
	mustOK(t, "filesys.remove", call(bus, "filesys.remove", map[string]any{"work_dir": wd, "path": "hello.txt"}))
	if _, err := os.Stat(filepath.Join(wd, "hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("remove 后文件仍在: %v", err)
	}

	// rename sub → sub2（相对 new_name）
	r = mustOK(t, "filesys.rename", call(bus, "filesys.rename", map[string]any{"work_dir": wd, "path": "sub", "new_name": "sub2"}))
	if r["path"] != filepath.Join(wd, "sub2") {
		t.Fatalf("rename path=%v", r["path"])
	}
	if _, err := os.Stat(filepath.Join(wd, "sub")); !os.IsNotExist(err) {
		t.Fatalf("rename 后旧路径仍在: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(wd, "sub2")); err != nil || !fi.IsDir() {
		t.Fatalf("rename 新路径不存在: %v", err)
	}

	// 重建文件供 copy/duplicate
	mustOK(t, "filesys.create", call(bus, "filesys.create", map[string]any{
		"work_dir": wd, "name": "hello.txt", "content": "copy me",
	}))
	mustOK(t, "filesys.mkdir", call(bus, "filesys.mkdir", map[string]any{"work_dir": wd, "name": "dest"}))

	// copy → dest/hello.txt（dest_dir 相对 work_dir）
	mustOK(t, "filesys.copy", call(bus, "filesys.copy", map[string]any{
		"work_dir": wd, "path": "hello.txt", "dest_dir": "dest",
	}))
	if b, err := os.ReadFile(filepath.Join(wd, "dest", "hello.txt")); err != nil || string(b) != "copy me" {
		t.Fatalf("copy 内容不符: %v %q", err, b)
	}

	// 同目录改名复制（前端传 new_name，替代原 filesys.duplicate）→ hello-copy.txt
	r = mustOK(t, "filesys.copy", call(bus, "filesys.copy", map[string]any{
		"work_dir": wd, "path": "hello.txt", "new_name": "hello-copy.txt",
	}))
	if r["path"] != filepath.Join(wd, "hello-copy.txt") {
		t.Fatalf("copy(new_name) path=%v", r["path"])
	}
	if b, err := os.ReadFile(filepath.Join(wd, "hello-copy.txt")); err != nil || string(b) != "copy me" {
		t.Fatalf("copy(new_name) 内容不符: %v %q", err, b)
	}

	// 目录改名复制（dest_dir 缺省 = 源目录）→ sub2-copy
	mustOK(t, "filesys.copy", call(bus, "filesys.copy", map[string]any{
		"work_dir": wd, "path": "sub2", "new_name": "sub2-copy",
	}))
	if fi, err := os.Stat(filepath.Join(wd, "sub2-copy")); err != nil || !fi.IsDir() {
		t.Fatalf("copy(new_name) 目录未生效: %v", err)
	}

	// 复制到其他目录 + 改名（dest_dir 与 new_name 组合）
	mustOK(t, "filesys.copy", call(bus, "filesys.copy", map[string]any{
		"work_dir": wd, "path": "hello.txt", "dest_dir": "dest", "new_name": "renamed.txt",
	}))
	if b, err := os.ReadFile(filepath.Join(wd, "dest", "renamed.txt")); err != nil || string(b) != "copy me" {
		t.Fatalf("copy(dest_dir+new_name) 内容不符: %v %q", err, b)
	}
}

// TestWriteErrorsExistsForbidden：exists/forbidden/参数类错误（mapErr code 断言 + message 含码词）。
func TestWriteErrorsExistsForbidden(t *testing.T) {
	bus, _, wd := newTestFilesys(t)

	// create 同名 → exists
	mustOK(t, "filesys.create", call(bus, "filesys.create", map[string]any{"work_dir": wd, "name": "dup.txt"}))
	wantErrCode(t, call(bus, "filesys.create", map[string]any{"work_dir": wd, "name": "dup.txt", "content": "again"}), "exists")

	// mkdir 同名 → exists
	mustOK(t, "filesys.mkdir", call(bus, "filesys.mkdir", map[string]any{"work_dir": wd, "name": "dir1"}))
	wantErrCode(t, call(bus, "filesys.mkdir", map[string]any{"work_dir": wd, "name": "dir1"}), "exists")

	// list 越界：相对 ../ 与 wd 外绝对路径 → forbidden
	wantErrCode(t, call(bus, "filesys.list", map[string]any{"work_dir": wd, "path": ".."}), "forbidden")
	outside := filepath.Join(os.TempDir(), "chonkpilot-filesys-outside")
	wantErrCode(t, call(bus, "filesys.list", map[string]any{"work_dir": wd, "path": outside}), "forbidden")

	// create 越界 dir → forbidden；空 name → create_failed
	wantErrCode(t, call(bus, "filesys.create", map[string]any{"work_dir": wd, "dir": "..", "name": "x.txt"}), "forbidden")
	wantErrCode(t, call(bus, "filesys.create", map[string]any{"work_dir": wd, "name": ""}), "create_failed")

	// rename 到已存在目标（未 overwrite）→ exists
	mustOK(t, "filesys.create", call(bus, "filesys.create", map[string]any{"work_dir": wd, "name": "src.txt", "content": "s"}))
	mustOK(t, "filesys.create", call(bus, "filesys.create", map[string]any{"work_dir": wd, "name": "dst.txt", "content": "d"}))
	wantErrCode(t, call(bus, "filesys.rename", map[string]any{"work_dir": wd, "path": "src.txt", "new_name": "dst.txt"}), "exists")

	// copy 到已存在目标（未 overwrite）→ exists；new_name 含分隔符 → copy_failed
	wantErrCode(t, call(bus, "filesys.copy", map[string]any{"work_dir": wd, "path": "src.txt", "new_name": "dst.txt"}), "exists")
	wantErrCode(t, call(bus, "filesys.copy", map[string]any{"work_dir": wd, "path": "src.txt", "new_name": "sub/dst.txt"}), "copy_failed")

	// Error() 返回 message，且内容含错误码词 exists
	v := call(bus, "filesys.create", map[string]any{"work_dir": wd, "name": "dup.txt"})
	if len(v.Errors) == 0 || !strings.Contains(v.Errors[0].Error(), "exists") {
		t.Fatalf("错误 message 应含 exists: %v", v.Errors)
	}
}

// TestWorkDirBoundToInstanceOnly（G-21 验收）：payload 的 work_dir **一律不被采信** ——
// 基目录只取 instance-register 登记的绑定；自报绑外目录不能把读写落到绑外。
func TestWorkDirBoundToInstanceOnly(t *testing.T) {
	bus, _, wd := newTestFilesys(t)

	// 他目录（模拟客户端自报 work_dir / 越界目标）
	other := t.TempDir()
	secret := filepath.Join(other, "SECRET.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ① 自报 work_dir=other + 绑外绝对 path → forbidden（拒绝执行，而非落到他目录）
	wantErrCode(t, call(bus, "filesys.list", map[string]any{"work_dir": other, "path": other}), "forbidden")
	// 读他目录文件同样拒绝（绝对 path 落在绑定的 wd 之外）
	wantErrCode(t, call(bus, "filesys.content", map[string]any{"work_dir": other, "path": secret}), "forbidden")
	// 删除他目录文件 → forbidden 且文件仍在
	wantErrCode(t, call(bus, "filesys.remove", map[string]any{"work_dir": other, "path": secret}), "forbidden")
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("绑外文件被删除：%v", err)
	}

	// ② 自报 work_dir=other + 相对 path → 作用于**实例绑定目录**（不影响他目录）
	mustOK(t, "filesys.create", call(bus, "filesys.create", map[string]any{
		"work_dir": other, "dir": ".", "name": "probe.txt", "content": "x",
	}))
	if _, err := os.Stat(filepath.Join(wd, "probe.txt")); err != nil {
		t.Fatalf("写入未落在实例绑定目录：%v", err)
	}
	if _, err := os.Stat(filepath.Join(other, "probe.txt")); err == nil {
		t.Fatal("写入落到了自报目录（work_dir 未被忽略）")
	}
	// list 自报 work_dir=other 时列的是实例绑定目录
	res := mustOK(t, "filesys.list", call(bus, "filesys.list", map[string]any{"work_dir": other, "path": ""}))
	if res["path"] != wd {
		t.Fatalf("list 基目录应为实例绑定 wd=%v，实得 %v", wd, res["path"])
	}

	// ③ 未登记 instance_id → forbidden（不回落载荷 work_dir，即便载荷填的是绑定值）
	wantErrCode(t, call(bus, "filesys.list", map[string]any{
		"work_dir": wd, "path": "", "instance_id": "ins-unregistered",
	}), "forbidden")
	// 无 instance_id → forbidden（mq 侧不注入时同样拒绝）
	unregistered := bus.Emit(context.Background(), "filesys.list", map[string]any{"work_dir": wd, "path": ""}).Wait()
	wantErrCode(t, unregistered, "forbidden")

	// ④ instance-exit 释放绑定 → 后续请求 forbidden（绑定不残留）
	// G-29 验收：超时回收侧（llm / persist 的 sweep）如今也发**同一** instance-exit
	// （主题与 payload 契约不变，61-消息一览 §4.1 ③）→ 这里按"收到 instance-exit 前可通 /
	// 收到后被拒"逐项验证，并验证重复到达的幂等（同一 id 发两次不报错、绑定不复活）。
	live := map[string]any{"work_dir": wd, "path": ""}
	mustOK(t, "filesys.list", call(bus, "filesys.list", live)) // 绑定在 → 同一 payload 可通

	exited := bus.Emit(context.Background(), "instance-exit",
		map[string]any{"instance_id": testInstanceID}).Wait()
	if len(exited.Errors) != 0 {
		t.Fatalf("instance-exit 处理不应报错：%v", exited.Errors)
	}
	wantErrCode(t, call(bus, "filesys.list", live), "forbidden") // 解绑 → 同一 payload 被拒

	// 幂等：同一 instance_id 的 instance-exit 重复到达（如 llm 与 persist 两处 sweep 同时
	// 判定、或显式 exit 与超时回收叠加）→ 不报错、绑定不复活。
	again := bus.Emit(context.Background(), "instance-exit",
		map[string]any{"instance_id": testInstanceID}).Wait()
	if len(again.Errors) != 0 {
		t.Fatalf("重复 instance-exit 不应报错：%v", again.Errors)
	}
	wantErrCode(t, call(bus, "filesys.list", live), "forbidden")
}

// changedMatches 判断广播载荷是否命中目标路径（单文件事件 path 或目录事件 children 中的节点 path）。
func changedMatches(m map[string]any, target string) bool {
	if p, _ := m["path"].(string); p == target {
		return true
	}
	if kids, ok := m["children"].([]any); ok {
		for _, k := range kids {
			if km, ok := k.(map[string]any); ok {
				if p, _ := km["path"].(string); p == target {
					return true
				}
			}
		}
	}
	return false
}

// waitChanged 在 timeout 内轮询 ch，命中 target 即返回；超时才 fail（最多等 timeout）。
func waitChanged(t *testing.T, ch chan map[string]any, target string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case m := <-ch:
			if changedMatches(m, target) {
				return
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("超时 %v 未收到 %s 的 filesys.changed", timeout, target)
}

// drainIdle 排空 ch，直到 idle 时长内无新消息（丢弃与断言无关的残留广播）。
func drainIdle(ch chan map[string]any, idle time.Duration) {
	for {
		select {
		case <-ch:
		case <-time.After(idle):
			return
		}
	}
}

// TestWatchChangedAndUnwatch：watch work_dir → 真实建文件触发 filesys.changed（1s 窗口）；
// unwatch 后再建文件不再收到该路径广播（600ms 观察窗）。
func TestWatchChangedAndUnwatch(t *testing.T) {
	bus, _, wd := newTestFilesys(t)

	ch := make(chan map[string]any, 128)
	sub, err := bus.On("filesys.changed", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			select {
			case ch <- m:
			default:
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	// watch 声明（fire-and-forget）：Wait 返回后 watcher 已建立、无错误
	if v := call(bus, "filesys.watch", map[string]any{"work_dir": wd, "path": wd}); len(v.Errors) != 0 {
		t.Fatalf("watch errors=%v", v.Errors)
	}

	// 真实创建文件 → 1s 内必须收到 filesys.changed（fsnotify + 60ms 去抖）
	nf := filepath.Join(wd, "watch-new.txt")
	if err := os.WriteFile(nf, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitChanged(t, ch, filepath.ToSlash(nf), time.Second)

	// unwatch → 排空残留事件 → 再建文件 → 观察窗内不得再收到该路径广播
	if v := call(bus, "filesys.unwatch", map[string]any{"work_dir": wd, "path": wd}); len(v.Errors) != 0 {
		t.Fatalf("unwatch errors=%v", v.Errors)
	}
	drainIdle(ch, 300*time.Millisecond)
	nf2 := filepath.Join(wd, "watch-unwatched.txt")
	if err := os.WriteFile(nf2, []byte("hi2"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case m := <-ch:
			if changedMatches(m, filepath.ToSlash(nf2)) {
				t.Fatalf("unwatch 后仍收到 %s 的 changed: %#v", nf2, m)
			}
		case <-time.After(30 * time.Millisecond):
		}
	}
}

// TestRemoveRefusesWorkDir：remove 空 path / 等价于 work_dir 的 path 一律拒绝（forbidden）——
// absPath 对空 path 回落 work_dir，若不拦将 `os.RemoveAll(work_dir)` 删掉整个工作目录。
func TestRemoveRefusesWorkDir(t *testing.T) {
	bus, _, wd := newTestFilesys(t)
	keep := filepath.Join(wd, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 空 path → 拒绝
	wantErrCode(t, call(bus, "filesys.remove", map[string]any{"work_dir": wd, "path": ""}), "forbidden")
	// "."（等价 work_dir）→ 拒绝
	wantErrCode(t, call(bus, "filesys.remove", map[string]any{"work_dir": wd, "path": "."}), "forbidden")
	// 绝对 work_dir 本身 → 拒绝
	wantErrCode(t, call(bus, "filesys.remove", map[string]any{"work_dir": wd, "path": wd}), "forbidden")

	if fi, err := os.Stat(wd); err != nil || !fi.IsDir() {
		t.Fatalf("work_dir 不应被删除: %v", err)
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "x" {
		t.Fatalf("work_dir 内文件不应被删除: %v %q", err, b)
	}
}

// TestCopyNewNameTraversalRejected：copy 的 new_name 只接受裸文件名——"." / ".." / 含分隔符者一律
// 拒绝（copy_failed），防 `Join(dest_dir,"..")` 越权写出 work_dir 外。
func TestCopyNewNameTraversalRejected(t *testing.T) {
	bus, _, wd := newTestFilesys(t)
	sub := filepath.Join(wd, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := "escape-marker-9f3a.txt"
	if err := os.WriteFile(filepath.Join(sub, marker), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(wd) // work_dir 的父目录（越权目标）

	// 裸 ".."：旧实现仅拦分隔符 → `Join(wd,"..")` 落到 work_dir 外的父目录并覆盖式合并写出
	wantErrCode(t, call(bus, "filesys.copy", map[string]any{
		"work_dir": wd, "path": "sub", "new_name": "..", "overwrite": 1,
	}), "copy_failed")
	// "." 同样拒绝
	wantErrCode(t, call(bus, "filesys.copy", map[string]any{
		"work_dir": wd, "path": "sub", "new_name": ".",
	}), "copy_failed")
	// 含分隔符的相对多段 → 拒绝（裸文件名约束）
	wantErrCode(t, call(bus, "filesys.copy", map[string]any{
		"work_dir": wd, "path": "sub", "new_name": "../evil",
	}), "copy_failed")

	if _, err := os.Stat(filepath.Join(parent, marker)); err == nil {
		t.Fatalf("copy new_name=\"..\" 越权写出 work_dir 外文件 %s", filepath.Join(parent, marker))
	}
}

// TestRenameRelativeMultiSegment：rename 相对多段 new_name（含分隔符，相对 work_dir 解析）应成功
// （旧实现 Clean 后仍相对 → withinWorkDir 恒 false，合法相对多段名被一律拒绝）；越界仍拒绝。
func TestRenameRelativeMultiSegment(t *testing.T) {
	bus, _, wd := newTestFilesys(t)
	if err := os.Mkdir(filepath.Join(wd, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wd, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "a", "f.txt"), []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := mustOK(t, "filesys.rename", call(bus, "filesys.rename", map[string]any{
		"work_dir": wd, "path": "a/f.txt", "new_name": "b/f2.txt",
	}))
	if r["path"] != filepath.Join(wd, "b", "f2.txt") {
		t.Fatalf("rename(相对多段) path=%v want %v", r["path"], filepath.Join(wd, "b", "f2.txt"))
	}
	if b, err := os.ReadFile(filepath.Join(wd, "b", "f2.txt")); err != nil || string(b) != "v" {
		t.Fatalf("rename 未生效: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(wd, "a", "f.txt")); !os.IsNotExist(err) {
		t.Fatalf("rename 后旧路径仍在: %v", err)
	}

	// 相对多段越权（../escaped.txt）→ forbidden，目标不得写出 work_dir 外
	wantErrCode(t, call(bus, "filesys.rename", map[string]any{
		"work_dir": wd, "path": "b/f2.txt", "new_name": "../escaped.txt",
	}), "forbidden")
	if _, err := os.Stat(filepath.Join(filepath.Dir(wd), "escaped.txt")); err == nil {
		t.Fatal("rename 越权写出了 work_dir 外文件")
	}
}

// makeDirLink 在 link 处创建指向 target 的目录链接：优先符号链接；无权限（Windows 未开开发者模式）
// 回退目录联接（mklink /J，无需特权）；均失败则 skip（并说明测试条件缺失）。
func makeDirLink(t *testing.T, link, target string) {
	t.Helper()
	symErr := os.Symlink(target, link)
	if symErr == nil {
		return
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("环境无法创建符号链接 / 目录联接（os.Symlink: %v；mklink: %v %s）——跳过符号链接越权用例",
			symErr, err, out)
	}
}

// TestSymlinkEscapeForbidden（#3）：work_dir 内的符号链接 / 目录联接指向 work_dir 外时，
// 经其访问的 read/list/copy 一律 forbidden（解析符号链接后的真实路径在 work_dir 外）。
func TestSymlinkEscapeForbidden(t *testing.T) {
	bus, _, wd := newTestFilesys(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(wd, "link")
	makeDirLink(t, link, outside)

	// 经符号链接读外部文件 → forbidden（不得泄露内容）
	wantErrCode(t, call(bus, "filesys.content", map[string]any{"work_dir": wd, "path": "link/secret.txt"}), "forbidden")
	// list 外部目录 → forbidden
	wantErrCode(t, call(bus, "filesys.list", map[string]any{"work_dir": wd, "path": "link"}), "forbidden")
	// copy 外部文件到 work_dir → forbidden
	wantErrCode(t, call(bus, "filesys.copy", map[string]any{
		"work_dir": wd, "path": "link/secret.txt", "dest_dir": ".",
	}), "forbidden")
	// 外部文件原样保留
	if b, err := os.ReadFile(secret); err != nil || string(b) != "SECRET" {
		t.Fatalf("外部文件被改动: %v %q", err, b)
	}
}
