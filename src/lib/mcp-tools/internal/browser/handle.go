package browser

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
)

// HandleBrowserRun browser_run 工具入口（cli dispatch 目标）。
func HandleBrowserRun(args map[string]interface{}) *cli.Result {
	var opt Options
	script := jsonString(args["script"])
	file := jsonString(args["file"])
	if script == "" && file != "" {
		// file（脚本文件读取路径）受 R-11 约束：须绝对或以 ~/ 开头
		resolved, msg := fileops.ValidateFilePath(file)
		if msg != "" {
			return cli.Err("browser_run", "file："+msg)
		}
		// 读脚本文件前过 agentbox 沙箱读校验（未启用隔离 = 放行；口径同 fileops/scriptrun）。
		if err := agentbox.Check(resolved, false); err != nil {
			return cli.Err("browser_run", "file："+err.Error())
		}
		b, err := os.ReadFile(resolved)
		if err != nil {
			return cli.Err("browser_run", "读取脚本文件失败: "+err.Error())
		}
		script = string(b)
	}
	if script == "" {
		return cli.Err("browser_run", "需要 script 或 file")
	}
	opt.Script = script
	// 落盘路径参数（R-11）：dom_file / console_file / fail_shot 须绝对或以 ~/ 开头
	domFile, msg := fileops.ValidateFilePath(jsonString(args["dom_file"]))
	if msg != "" {
		return cli.Err("browser_run", "dom_file："+msg)
	}
	consoleFile, msg := fileops.ValidateFilePath(jsonString(args["console_file"]))
	if msg != "" {
		return cli.Err("browser_run", "console_file："+msg)
	}
	failShot, msg := fileops.ValidateFilePath(jsonString(args["fail_shot"]))
	if msg != "" {
		return cli.Err("browser_run", "fail_shot："+msg)
	}
	opt.DomFile = domFile
	opt.ConsoleFile = consoleFile
	opt.FailShot = failShot
	opt.WatTimeoutMs = jsonInt(args["wat_timeout_ms"], 10000)
	opt.Headless = jsonBool(args["headless"], true)
	// chrome_path（Chrome/Edge 可执行文件路径，R-11）：须绝对或以 ~/ 开头（相对路径会报错）
	if p := jsonString(args["chrome_path"]); p != "" {
		resolved, msg := fileops.ValidateFilePath(p)
		if msg != "" {
			return cli.Err("browser_run", "chrome_path："+msg)
		}
		opt.ChromePath = resolved
	}

	// 脚本内字面文件路径（SHT/DOM/DBG/UPF、`=> #"file"` 目标、LOOP/SET 数据源、IF exist 等）
	// 在执行前预校验（R-11）。
	if msgs := prevalidateScript(script); len(msgs) > 0 {
		return cli.Err("browser_run", strings.Join(msgs, "; "))
	}

	// 宿主注入的只读 env（CHONKPILOT_* → {{env.CHONKPILOT_*}}）；缺 instance → 顶层失败。
	te, envErr := fileops.BuildToolEnv()
	if envErr != nil {
		return cli.Err("browser_run", envErr.Error())
	}
	opt.Vars = te.Vars

	out, err := Execute(context.Background(), opt)
	if err != nil {
		return cli.Err("browser_run", err.Error())
	}
	if out == "" {
		out = "执行完成"
	}
	return cli.Ok("browser_run", out, nil)
}

func jsonString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func jsonInt(v interface{}, def int64) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
	}
	return def
}

func jsonBool(v interface{}, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}
