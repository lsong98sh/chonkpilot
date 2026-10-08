// manager.go — filesys_run 工具入口（文件操作 DSL）。
//
// DSL 与 llm_run/desktop_run/browser_run 共用同一解析引擎（chonkpilot-lib/dsl）：
// 动词（RPL/APD/PTC/INS/DEL/MOV/CPY）注册为 Raw 动作，参数原文透传后由
// actions.go 解析执行；引擎免费提供 SET/IF/LOOP/PARALLEL/EXIT 流控与 {{}} 插值。
//
// 语法（定稿 2026-09-04，详细样例见 filesys_run.tool.md）：
// 路径（含 DSL 内 #"path" 句柄）须绝对路径或 ~/ 开头（R-11）。
//
//	### 全局替换
//	RPL #"~/proj/src/main.py" "def old()" "def new():\n    return 42"
//	### 追加
//	APD #"~/proj/src/main.py" "print('done')"
//	### 补丁
//	PTC #"~/proj/src/main.py" "--- a\n+++ b\n@@ -1,3 +1,4 @@..."
//	### 创建 / 删除
//	INS #"~/proj/src/util.py" "def helper(): pass"
//	DEL #"~/proj/src/obsolete.py"
//	DEL #"~/proj/src/main.py" "def dead_code()"        # 删除包含关键字的行
//	### 移动 / 复制
//	MOV #"~/proj/src/app.py" #"~/proj/lib/app.py"
//	CPY #"~/proj/src/config.py" #"~/proj/backup/config.py.bak"
//
// 一致性：修改/删除已有文件前加锁（30s 内重试），成功后解锁；顶层 md5 参数在
// 锁内首触校验，不一致记 fails（不整体失败）。单操作失败不中断其余操作。
//
// 返回：modified（每文件 before→after diff）/ created / deleted / fails。
package fileops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// HandleFileManager 执行文件操作 DSL 脚本。
//
//	filesys_run { script | file [, md5: { "path": "expected_md5" }] }
func HandleFileManager(workDir string, args map[string]interface{}) *cli.Result {
	script, scriptErr := scriptFromArgs(args)
	if scriptErr != "" {
		return cli.Err("filesys_run", scriptErr)
	}
	if script == "" {
		return cli.Err("filesys_run", "script or file is required")
	}
	expectMD5, md5Err := expectMD5FromArgs(args)
	if md5Err != "" {
		return cli.Err("filesys_run", md5Err)
	}
	sess, _ := NewSession(workDir, expectMD5)
	ec := sess.ec
	actions := sess.Actions()

	ast, err := dsl.Parse(script, actions)
	if err != nil {
		return cli.Err("filesys_run", err.Error())
	}
	// 参数级路径违规（R-11）：字面相对路径在执行前即拒绝（整体失败、无副作用）。
	if msgs := prevalidateScriptPaths(ast); len(msgs) > 0 {
		return cli.Err("filesys_run", strings.Join(msgs, "; "))
	}
	// Files 注入校验型 ScriptFS：核心语句 `#"path"` 句柄（LOOP/SET 数据源、访问器、
	// `=> #"file"` 目标）受 R-11 约束（绝对 / ~/ / !/），{{}} 插值路径执行时兜底校验。
	// Vars 注入宿主只读 env（{{env.CHONKPILOT_WORKDIR}} 等），供脚本显式拼绝对路径。
	// 缺 CHONKPILOT_INSTANCE（宿主未注入调用上下文）→ 顶层失败。
	te, envErr := BuildToolEnv()
	if envErr != nil {
		return cli.Err("filesys_run", envErr.Error())
	}
	eng := dsl.NewEngine(dsl.Options{Files: sess.Files(), Actions: actions, StopOnError: false, Vars: te.Vars})
	_ = eng.Execute(ast)

	var scriptErrs []string
	for _, e := range eng.Result().Errors {
		scriptErrs = append(scriptErrs, fmt.Sprintf("第 %d 行：%s", e.Line, e.Msg))
	}
	// agentbox 沙箱越界（仅隔离开启时可能出现）→ 顶层错误（exit 1 + 可诊断消息），
	// 与 R-11 参数级违规同档：不做「单条记 fails、整体成功」的降级。
	if hit, msgs := ec.sandboxViolation(); hit {
		return cli.Err("filesys_run", strings.Join(msgs, "; "))
	}
	// 参数级违规（R-11：DSL 内 #"path" 不合规）→ 顶层错误（exit 1 + 错误消息），
	// 不降级为 fails；与「单条运行时冲突记 fails、整体不失败」语义区分。
	if ec.pathViolation() {
		return cli.Err("filesys_run", strings.Join(scriptErrs, "; "))
	}

	modified := ec.finish()
	createdRefs, deleted, fails := ec.snapshot()
	created := createdEntries(createdRefs)
	summary := fmt.Sprintf("modified: %d, created: %d, deleted: %d", len(modified), len(created), len(deleted))
	payload, err := json.Marshal(map[string]interface{}{
		"status":   "success",
		"output":   summary, // 摘要仅计数；diff 正文见 modified[].diff
		"modified": modified,
		"created":  created,
		"deleted":  deleted,
		"fails":    fails,
	})
	if err != nil {
		return cli.Err("filesys_run", "marshal result: "+err.Error())
	}
	if len(scriptErrs) > 0 {
		failPayload, _ := json.Marshal(map[string]interface{}{
			"status":   "fail",
			"error":    strings.Join(scriptErrs, "; "),
			"output":   summary,
			"modified": modified,
			"created":  created,
			"deleted":  deleted,
			"fails":    fails,
		})
		return &cli.Result{Success: false, Output: string(failPayload), Error: strings.Join(scriptErrs, "; "), Tool: "filesys_run", RawResult: map[string]interface{}{
			"modified": modified, "created": created, "deleted": deleted, "fails": fails,
		}}
	}
	return cli.Ok("filesys_run", string(payload), map[string]interface{}{
		"modified": modified, "created": created, "deleted": deleted, "fails": fails,
	})
}

// expectMD5FromArgs 解析顶层 md5 参数（map[string]string：路径 → 期望 md5）。
// 键即文件路径，须满足 R-11（绝对路径或 ~/ 开头）；违规返回顶层错误消息。
func expectMD5FromArgs(args map[string]interface{}) (map[string]string, string) {
	expectMD5 := map[string]string{}
	rawMD5, ok := args["md5"].(map[string]interface{})
	if !ok {
		return expectMD5, ""
	}
	var bad []string
	for k, v := range rawMD5 {
		if msg := ValidateField(fmt.Sprintf("md5[%q]", k), k); msg != "" {
			bad = append(bad, msg)
			continue
		}
		if s, ok := v.(string); ok {
			expectMD5[k] = s
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, strings.Join(bad, "; ")
	}
	return expectMD5, ""
}

// scriptFromArgs 从 script / file 参数取编辑脚本文本。
// file 参数为脚本文件路径，须满足 R-11；违规返回顶层错误消息。
func scriptFromArgs(args map[string]interface{}) (string, string) {
	if s, _ := args["script"].(string); s != "" {
		return s, ""
	}
	if p, _ := args["file"].(string); p != "" {
		resolved, msg := ValidateFilePath(p)
		if msg != "" {
			return "", "file：" + msg
		}
		// agentbox 沙箱（仅隔离开启时生效）：脚本文件须在允许读目录内
		if err := sandboxErr(resolved, false); err != nil {
			return "", "file：" + err.Error()
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", "读取脚本文件失败：" + err.Error()
		}
		return string(data), ""
	}
	return "", ""
}

// copyFile 文件级复制（from -> to）：文件整份复制；目录递归复制（含子目录）。
func copyFile(from, to, workDir string) (string, string) {
	src, e1 := ResolvePath(from, workDir)
	dst, e2 := ResolvePath(to, workDir)
	if e1 != "" {
		return "", e1
	}
	if e2 != "" {
		return "", e2
	}
	sf, err := os.Stat(src)
	if err != nil {
		return "", fmt.Sprintf("copy %s: %s", from, err)
	}
	if sf.IsDir() {
		if err := copyDirRecursive(src, dst); err != nil {
			return "", fmt.Sprintf("copy %s -> %s: %s", from, to, err)
		}
		return from + " -> " + to, ""
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", fmt.Sprintf("copy %s -> %s: %s", from, to, err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Sprintf("copy %s: %s", from, err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return "", fmt.Sprintf("copy %s -> %s: %s", from, to, err)
	}
	return from + " -> " + to, ""
}

// copyDirRecursive 递归复制目录内容。
func copyDirRecursive(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			if rel == "." {
				return nil
			}
			return os.MkdirAll(target, 0755)
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0644)
	})
}

// unifiedDiff 生成两文本的统一 diff 文本（Actions.finish 使用）。
func unifiedDiff(name, before, after string) string {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(before, after, false)
	dmp.DiffCleanupSemantic(diffs)
	var bd strings.Builder
	fmt.Fprintf(&bd, "--- %s\n+++ %s\n", name, name)
	for _, d := range diffs {
		dl := strings.Split(d.Text, "\n")
		if dl[len(dl)-1] == "" {
			dl = dl[:len(dl)-1]
		}
		for _, ln := range dl {
			switch d.Type {
			case diffmatchpatch.DiffDelete:
				bd.WriteString("-" + ln + "\n")
			case diffmatchpatch.DiffInsert:
				bd.WriteString("+" + ln + "\n")
			default:
				bd.WriteString(" " + ln + "\n")
			}
		}
	}
	return bd.String()
}
