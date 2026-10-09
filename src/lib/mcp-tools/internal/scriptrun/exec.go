// Package scriptrun 实现 script_run 工具：多 runtime 脚本执行器。
//
// 契约（25-mcp-server）:
//
//	script_run(runtime, script|file, workdir?, requires?, env?, args?, interpreter?)
//
// runtime 白名单：shell / cmd / bash / python / js / powershell / vbs / java。
// 解释器来源（配置驱动，不探测 PATH）：
//  1. interpreter 参数（显式覆盖）
//  2. CHONKPILOT_INTERPRETERS 环境变量（宿主 spawn executor 时注入，{runtime: 解释器路径}）
//  3. 系统命令型（shell/cmd/bash）内置默认（cmd/bash 由 OS 提供）
//  4. 其余 runtime 未配置 → 直接报错（提示 LLM 安装/配置 runtime）
//
// 执行层（mcp-tools）无沙箱/无 datadir：不做路径白名单（agentbox 管控）；
// `file`（脚本文件）、`workdir`（运行目录）、`interpreter`（解释器/可执行文件路径）
// 均受 R-11 约束（须绝对路径 / `~/` 开头 / `!/` 开头，见 fileops.ValidateFilePath），相对路径拒绝。
// 宿主注入的 CHONKPILOT_*（fileops.HostEnv）注入子进程环境（用户 env 同名优先）。
// 脚本临时文件写系统 temp，输出截断，无内置取消（上层 gateway 负责）。
package scriptrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-lib/winproc"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
)

// runtimes 支持的 runtime 白名单。
var runtimes = map[string]struct{}{
	"shell": {}, "cmd": {}, "bash": {},
	"python": {}, "js": {}, "powershell": {}, "vbs": {}, "java": {},
}

// scriptExt 代码文件型 runtime 的临时脚本扩展名。
// java 必须为 .java：JDK 11+ 单文件源码模式（JEP 330）要求 `java <file>.java`，
// 其它扩展名会被当作类名 → ClassNotFoundException（I-80）；public 类名与文件名不匹配不受限
// （JEP 330 已放宽 JLS 7.6 的同名约束），故临时文件名可保持随机。
var scriptExt = map[string]string{
	"python": ".py", "js": ".js", "powershell": ".ps1", "vbs": ".vbs", "bash": ".sh", "java": ".java",
}

// scriptOutputLimit 是脚本 stdout+stderr 累计读取上限（8MB）：超出即封顶丢弃尾部，
// 防脚本产出无界输出全量入内存（C-55）。结果超长仍由 executor 统一层再处理。
const scriptOutputLimit = 8 << 20

// cappedBuffer 是带累计上限的字节缓冲（并发安全：stdout/stderr 可能共写）：
// 写满上限后丢弃其余内容并标记截断。
type cappedBuffer struct {
	mu      sync.Mutex
	buf     []byte
	limit   int
	dropped bool
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if remain := w.limit - len(w.buf); remain > 0 {
		if len(p) <= remain {
			w.buf = append(w.buf, p...)
		} else {
			w.buf = append(w.buf, p[:remain]...)
			w.dropped = true
		}
	} else {
		w.dropped = true
	}
	return len(p), nil
}

// string 返回累计输出；发生截断时追加一行提示。
func (w *cappedBuffer) string() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := string(w.buf)
	if w.dropped {
		s += fmt.Sprintf("\n...(输出超过 %dMB，已截断)", w.limit>>20)
	}
	return s
}

// HandleScriptRun 执行 script_run 工具。
func HandleScriptRun(workDir string, args map[string]interface{}) *cli.Result {
	rt, _ := args["runtime"].(string)
	rt = strings.ToLower(strings.TrimSpace(rt))
	if rt == "" {
		return cli.Err("script_run", "runtime 必填（shell/cmd/bash/python/js/powershell/vbs/java）")
	}
	if _, ok := runtimes[rt]; !ok {
		return cli.Err("script_run", fmt.Sprintf("不支持的 runtime: %s", rt))
	}

	script, _ := args["script"].(string)
	filePath, _ := args["file"].(string)
	if (script == "") == (filePath == "") {
		return cli.Err("script_run", "script 与 file 必须且仅有一个有值")
	}

	workDir, wdMsg := resolveWorkDir(workDir, args["workdir"])
	if wdMsg != "" {
		return cli.Err("script_run", "workdir："+wdMsg)
	}
	if workDir == "" {
		workDir = "."
	}
	// file 参数强校验（R-11）：必须绝对路径或 ~/ 开头，否则顶层错误。
	// agentbox 沙箱（仅隔离开启时生效）：脚本文件须在允许读目录内。
	// 说明：脚本**内部**的文件读写无法拦截（任意子进程）——本工具在隔离开启下仍属
	// 「仅参数面约束、非强制」通道，见 14-安全域-agentbox §5 登记。
	if filePath != "" {
		resolved, msg := fileops.ValidateFilePath(filePath)
		if msg != "" {
			return cli.Err("script_run", "file："+msg)
		}
		if err := agentbox.Check(resolved, false); err != nil {
			return cli.Err("script_run", "file："+err.Error())
		}
		filePath = resolved
	}

	// 解释器解析（配置驱动）
	interp, msg := resolveInterpreter(rt, args)
	if interp == "" {
		return cli.Err("script_run", msg)
	}

	// 宿主注入环境（R-11 二次升级）：本进程 CHONKPILOT_* → 子进程环境。
	// 用户显式 env 优先（同名覆盖）。
	childEnv := mergeChildEnv(os.Environ(), fileops.HostEnv(), childUserEnv(args))

	// requires 依赖安装
	if reqs, ok := args["requires"].([]interface{}); ok && len(reqs) > 0 {
		var list []string
		for _, r := range reqs {
			if s, ok := r.(string); ok && s != "" {
				list = append(list, s)
			}
		}
		if len(list) > 0 {
			if r := installRequires(rt, interp, workDir, list, childEnv); r != nil {
				return r
			}
		}
	}

	// 构造命令
	cmdArgs, cleanup, err := buildCommand(rt, interp, script, filePath)
	if err != nil {
		return cli.Err("script_run", err.Error())
	}
	if cleanup != nil {
		defer cleanup()
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.SysProcAttr = winproc.SysProcAttr()
	cmd.Dir = workDir
	cmd.Env = childEnv
	if raw, ok := args["args"].([]interface{}); ok {
		for _, a := range raw {
			if s, ok := a.(string); ok {
				cmd.Args = append(cmd.Args, s)
			}
		}
	}

	start := time.Now()
	// C-55：限流 Writer 接管 stdout/stderr，累计超上限即封顶丢弃尾部，防脚本无界输出全量入内存
	//（统一层的超长截断发生在整串已读入之后，故必须在此先封顶）。
	ob := &cappedBuffer{limit: scriptOutputLimit}
	cmd.Stdout = ob
	cmd.Stderr = ob
	runErr := cmd.Run()
	elapsed := int64(time.Since(start).Seconds())

	outStr := ob.string()

	// filter（可选，regex 行过滤，等价 <cmd> | findstr/egrep；过滤后由统一层封装）
	if filterExpr, _ := args["filter"].(string); filterExpr != "" {
		re, err := regexp.Compile(filterExpr)
		if err != nil {
			return cli.Err("script_run", fmt.Sprintf("invalid filter regex: %s", err))
		}
		var kept []string
		for _, line := range strings.Split(outStr, "\n") {
			if re.MatchString(line) {
				kept = append(kept, line)
			}
		}
		outStr = strings.Join(kept, "\n")
	}
	outStr = strings.TrimSpace(outStr)

	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}

	raw := map[string]interface{}{
		"runtime":   rt,
		"exit_code": exitCode,
		"elapsed":   elapsed,
	}
	if exitCode != 0 {
		return &cli.Result{
			Success:   false,
			Error:     fmt.Sprintf("脚本执行失败（exit %d）：%s", exitCode, runErr),
			Output:    outStr,
			Tool:      "script_run",
			RawResult: raw,
		}
	}
	return cli.Ok("script_run", outStr, raw)
}

// resolveWorkDir 解析 workdir 参数（R-11：运行目录须绝对路径或 `~/` 开头，相对路径拒绝）。
// 返回 (解析结果, 违规消息)；未提供 workdir 时沿用 base（缺省由调用方回落 "."）。
func resolveWorkDir(base string, raw interface{}) (string, string) {
	if s, ok := raw.(string); ok && s != "" {
		resolved, msg := fileops.ValidateFilePath(s)
		if msg != "" {
			return "", msg
		}
		return resolved, ""
	}
	return base, ""
}

// resolveInterpreter 解析解释器：显式 interpreter > CHONKPILOT_INTERPRETERS > 系统命令型默认。
// 显式 interpreter 为可执行文件路径，须绝对或以 ~/ 开头（R-11）；系统命令型默认
// （cmd/bash/sh）走 PATH 解析，不受约束。CHONKPILOT_INTERPRETERS 为宿主注入的内部配置，不校验。
func resolveInterpreter(rt string, args map[string]interface{}) (string, string) {
	if i, _ := args["interpreter"].(string); i != "" {
		if _, msg := fileops.ValidateFilePath(i); msg != "" {
			return "", "interpreter：" + msg
		}
		return i, ""
	}
	if m := interpretersFromEnv(); m != nil {
		if v, ok := m[rt]; ok && v != "" {
			return v, ""
		}
	}
	switch rt {
	case "cmd":
		return "cmd", ""
	case "bash":
		return "bash", ""
	case "shell":
		if runtime.GOOS == "windows" {
			return "cmd", ""
		}
		return "sh", ""
	}
	return "", fmt.Sprintf("❌ runtime %q 未配置（未安装解释器路径）；请安装后在项目配置中指定 interpreter.%s，或由宿主注入 CHONKPILOT_INTERPRETERS", rt, rt)
}

// interpretersFromEnv 解析 CHONKPILOT_INTERPRETERS（{runtime: 解释器绝对路径} JSON 对象）；
// 缺失/非法 → nil。仅内部 executor 进程可见（宿主 spawn 时注入）。
func interpretersFromEnv() map[string]string {
	raw := strings.TrimSpace(os.Getenv(fileops.EnvInterpreters))
	if raw == "" {
		return nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

// buildCommand 构造执行命令。命令串型（shell/cmd/bash）：解释器 + 串；
// 代码文件型：临时脚本文件（或用户 file） + 解释器。返回 argv 与清理函数。
func buildCommand(rt, interp, script, filePath string) ([]string, func(), error) {
	// 命令串型
	if rt == "shell" || rt == "cmd" || rt == "bash" {
		switch rt {
		case "cmd":
			if filePath != "" {
				return []string{interp, "/c", filePath}, nil, nil
			}
			return []string{interp, "/c", script}, nil, nil
		case "bash":
			if filePath != "" {
				return []string{interp, filePath}, nil, nil
			}
			return []string{interp, "-c", script}, nil, nil
		default: // shell
			if runtime.GOOS == "windows" {
				if filePath != "" {
					return []string{interp, "/c", filePath}, nil, nil
				}
				return []string{interp, "/c", script}, nil, nil
			}
			if filePath != "" {
				return []string{interp, filePath}, nil, nil
			}
			return []string{interp, "-c", script}, nil, nil
		}
	}

	// 代码文件型
	if filePath != "" {
		if _, err := os.Stat(filePath); err != nil {
			return nil, nil, fmt.Errorf("脚本文件不存在: %s", filePath)
		}
		return []string{interp, filePath}, nil, nil
	}

	ext := scriptExt[rt]
	if ext == "" {
		ext = ".txt"
	}
	f, err := os.CreateTemp("", "ck_script_*"+ext)
	if err != nil {
		return nil, nil, fmt.Errorf("创建临时脚本失败: %s", err)
	}
	path := f.Name()
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		os.Remove(path)
		return nil, nil, fmt.Errorf("写入临时脚本失败: %s", err)
	}
	f.Close()

	// powershell 需绕过执行策略
	if rt == "powershell" {
		return []string{interp, "-ExecutionPolicy", "Bypass", "-File", path}, func() { os.Remove(path) }, nil
	}
	return []string{interp, path}, func() { os.Remove(path) }, nil
}

// childUserEnv 解析用户显式 env 参数（字符串化）。
func childUserEnv(args map[string]interface{}) map[string]string {
	out := map[string]string{}
	if envRaw, ok := args["env"].(map[string]interface{}); ok {
		for k, v := range envRaw {
			out[k] = fmt.Sprint(v)
		}
	}
	return out
}

// mergeChildEnv 合并子进程环境：base（os.Environ）为底，groups 依序覆盖（后组同名优先），
// 按变量名去重（保留最后一次取值）。
func mergeChildEnv(base []string, groups ...map[string]string) []string {
	idx := map[string]int{}
	out := make([]string, 0, len(base))
	for _, kv := range base {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if _, dup := idx[k]; dup {
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	for _, g := range groups {
		for k, v := range g {
			kv := k + "=" + v
			if i, ok := idx[k]; ok {
				out[i] = kv
				continue
			}
			idx[k] = len(out)
			out = append(out, kv)
		}
	}
	return out
}

// installTimeout 是依赖安装（pip/npm）的独立上限：安装可能因网络/交互而挂起，
// 不能无限阻塞调用方（与主脚本执行的「上层 gateway 取消」口径分离）。
const installTimeout = 5 * time.Minute

// installRequires 安装依赖（executor 静默执行，交互式安装不支持）。
// python → pip install；js → npm install --no-save（无 package.json 先 init -y）；其余暂不支持。
func installRequires(rt, interp, workDir string, requires []string, env []string) *cli.Result {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	switch rt {
	case "python":
		pkgs := append([]string{"-m", "pip", "install", "--disable-pip-version-check", "-q"}, requires...)
		cmd := exec.CommandContext(ctx, interp, pkgs...)
		cmd.SysProcAttr = winproc.SysProcAttr()
		cmd.Dir = workDir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return cli.Err("script_run", fmt.Sprintf("pip install 失败：%s\n%s", err, strings.TrimSpace(string(out))))
		}
		return nil
	case "js":
		if _, err := os.Stat(filepath.Join(workDir, "package.json")); err != nil {
			initCmd := exec.CommandContext(ctx, "npm", "init", "-y")
			initCmd.SysProcAttr = winproc.SysProcAttr()
			initCmd.Dir = workDir
			initCmd.Env = env
			if out, err := initCmd.CombinedOutput(); err != nil {
				return cli.Err("script_run", fmt.Sprintf("npm init 失败：%s\n%s", err, strings.TrimSpace(string(out))))
			}
		}
		pkgs := append([]string{"install", "--no-save"}, requires...)
		cmd := exec.CommandContext(ctx, "npm", pkgs...)
		cmd.SysProcAttr = winproc.SysProcAttr()
		cmd.Dir = workDir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return cli.Err("script_run", fmt.Sprintf("npm install 失败：%s\n%s", err, strings.TrimSpace(string(out))))
		}
		return nil
	default:
		return cli.Err("script_run", fmt.Sprintf("runtime %s 暂不支持 requires 依赖安装", rt))
	}
}
