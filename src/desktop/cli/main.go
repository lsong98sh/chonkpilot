// chonkpilot-cli：CLI 单体（批处理/脚本模式，工具调用强制同步，无 WebView2）。
//
// 单进程内嵌：filesys + data/persist + LLM server + gateway + mcp-server + plugins，
// 与 GUI 单体共享同一 lib 集，通过内存 MQ 通讯。
//
// 本文件只留 **flag 解析 + 装配**（I-158）：数据根三态准备 / work-dir 占用校验 /
// prjusr 试开 / 配置复制编排都在 `chonkpilot-cli` 库（src/lib/cli，L2 可测）。
//
// 用法：
//
//	chonkpilot-cli.exe --prompt "帮我查一下" --work-dir .
//	echo "帮我查一下" | chonkpilot-cli.exe --work-dir .
//	chonkpilot-cli.exe --prompt "总结" --output final --work-dir .
//	chonkpilot-cli.exe --prompt "全文" --output verbose --work-dir . > dump.txt
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/chonkpilot/chonkpilot-cli"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-codegraph"
	"github.com/chonkpilot/chonkpilot-plugin-history"
	"github.com/chonkpilot/chonkpilot-plugin-vfts"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

// cliOpts 是 CLI 解析后的参数集合（`parseFlags` 产物）。
type cliOpts struct {
	prompt     string
	promptFile string
	scenario   string
	workDir    string
	dataDir    string
	output     string
	llmModel   string
	think      string
	effort     string
	// dataDirSet 区分「--data-dir 未传」与「--data-dir=（显式留空）」
	// （三态数据根语义，见 chonkpilot-cli 包 PrepareDataDir）。
	dataDirSet bool
}

// parseFlags 注册 CLI 参数并解析 args（不含程序名），返回解析结果。
// 仅「输出模式非法」在此返回错误；flag 语法错误由 fs 的 ErrorHandling 决定
// （生产用 flag.ExitOnError → 进程退出；测试用 flag.ContinueOnError → 返回错误）。
func parseFlags(fs *flag.FlagSet, args []string) (*cliOpts, error) {
	o := &cliOpts{}
	fs.StringVar(&o.prompt, "prompt", "", "提示词（与 --prompt-file 二选一）")
	fs.StringVar(&o.promptFile, "prompt-file", "", "提示词文件路径（与 --prompt 二选一）")
	fs.StringVar(&o.scenario, "scenario", "", "场景目录名（空=默认场景）")
	fs.StringVar(&o.workDir, "work-dir", "", "工作目录（默认 cwd）")
	fs.StringVar(&o.dataDir, "data-dir", "", "数据根（不传=临时隔离；留空=真实根；给路径=该路径作数据根）")
	fs.StringVar(&o.output, "output", "sse", "输出模式：sse（流式）/ final（仅最终结果）/ verbose（全报文 dump）")
	fs.StringVar(&o.llmModel, "llm", "", "LLM 模型名（默认 server 配置）")
	fs.StringVar(&o.think, "think", "", "思考模式（high / medium / low）")
	fs.StringVar(&o.effort, "effort", "", "思考力度（high / medium / low）")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "data-dir" {
			o.dataDirSet = true
		}
	})
	switch o.output {
	case "sse", "final", "verbose":
	default:
		return nil, fmt.Errorf("无效输出模式 %q，可选：sse / final / verbose", o.output)
	}
	return o, nil
}

func main() {
	opts, err := parseFlags(flag.CommandLine, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	prompt, promptFile, scenario := opts.prompt, opts.promptFile, opts.scenario
	workDir, dataDir, output := opts.workDir, opts.dataDir, opts.output
	llmModel, think, effort := opts.llmModel, opts.think, opts.effort
	dataDirSet := opts.dataDirSet

	if prompt == "" && promptFile == "" {
		// 尝试从 stdin 读取
		stat, statErr := os.Stdin.Stat()
		// stat 失败按「非管道」处理（视作无可读 stdin）——不可在 stat 为 nil 时解引用
		// （原 `stat, _ := ...` 吞错后在 nil 上取 Mode() 会 panic）。
		if statErr != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
			fmt.Fprintln(os.Stderr, "请提供 --prompt 或 --prompt-file，或通过 stdin 传入提示词")
			os.Exit(1)
		}
		scanner := bufio.NewScanner(os.Stdin)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		// 循环结束须检查 scanner.Err()（读 stdin 出错时不得静默继续，如 I/O 错误）。
		if err := scanner.Err(); err != nil {
			fmt.Fprintln(os.Stderr, "读取 stdin 失败:", err)
			os.Exit(1)
		}
		prompt = strings.Join(lines, "\n")
	} else if promptFile != "" {
		b, err := os.ReadFile(promptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取提示词文件 %s: %v\n", promptFile, err)
			os.Exit(1)
		}
		prompt = string(b)
	}

	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cwd:", err)
			os.Exit(1)
		}
	}
	workDir = resolveDir(workDir)

	// -- 数据根（三态语义：未传=临时隔离 / 留空=真实根 / 路径=该路径作数据根，D-45） --
	prepared, err := cli.PrepareDataDir(workDir, dataDir, dataDirSet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[cli] prepare data dir:", err)
		os.Exit(1)
	}
	defer prepared.Cleanup()
	dataDir = prepared.DataDir

	instanceID := newUUID()

	// -- 消息总线（命名空间前缀 chonk. 在此注入） --
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		log.Fatalf("[cli] mq.New: %v", err)
	}
	defer bus.Close()

	// -- chonkpilot-filesys 文件服务 --
	fsys := filesys.New(bus)
	if err := fsys.Start(); err != nil {
		log.Fatalf("[cli] filesys start: %v", err)
	}
	defer fsys.Stop()

	// -- inprocess 会话服务（内嵌 persist + gateway + mcp-server + plugins） --
	srv := server.New(bus, server.Options{
		LLMBase:   "http://127.0.0.1:8901/v1",
		LLMModel:  llmModel,
		AsyncMode: "never", // 强制同步，不允许转异步
		// 服务端启动参数下发（唯一消费点 = instance-claim 的 desktop 回落，见 61 §4.1 ①）
		WorkDir: workDir,
		DataDir: dataDir,
		Plugins: []plugin.Hook{
			// DSL-4（42 §2 (253) ④）：CLI 宿主**硬编码禁用记忆与压缩**——只禁用**自动沉淀 / 自动压缩**
			// （写路径），故此处**不挂载** compress / memory 插件（配置即便开启也不生效；CLI 无设置页与
			// 「立即沉淀记忆」入口，手动路径本就不存在）。**指引块（读路径）不受影响**：记忆清单/资产/
			// 用户偏好指引由 llm server 侧 memory_guide 注入，只随 prj `memory.enabled` 门控，与宿主形态无关。
			// 其余插件照常（history 快照 / codegraph / vfts 索引工具面，默认关闭）。
			history.New(),
			codegraph.New(codegraph.Options{}),
			vfts.New(vfts.Options{}),
		},
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("[cli] server start: %v", err)
	}
	defer srv.Stop()

	// -- 注册实例 --
	instancePayload := map[string]any{
		"instance_id": instanceID,
		"work_dir":    workDir,
		"data_dir":    dataDir,
		"client_type": "cli",
	}
	instanceJSON, _ := json.Marshal(instancePayload)
	bus.Emit(context.Background(), msgkeys.TopicInstanceRegister, instanceJSON)

	// 形态开关（编译期，42 §2 (69)(72)）：分离形态（`-tags split`）注册后起 30s 周期心跳发布
	// （instance-heartbeat{instance_id}）；合并单进程形态（默认构建）为空实现（不发布、不判超时）。
	// CLI 无对端地址参数 → 传空。
	stopHeartbeat := instance.StartHeartbeat(bus, instanceID, "")
	defer stopHeartbeat()

	// -- 启动会话 --
	session := newUUID()
	turn := newUUID()

	doneCh := make(chan struct{})
	var finalText string

	// session-receive：LLM 流式内容
	bus.On(msgkeys.TopicSessionReceive, 0, func(_ context.Context, _ string, v *mq.Value) error {
		payload := v.Payload
		var ev struct {
			Session string          `json:"session"`
			Turn    string          `json:"turn"`
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		_ = json.Unmarshal(payload, &ev)

		switch output {
		case "verbose":
			// verbos 模式：dump 原始报文
			fmt.Printf("[RECV] %s %s\n", ev.Type, string(payload))
		case "sse":
			switch ev.Type {
			case "text":
				var text string
				_ = json.Unmarshal(ev.Payload, &text)
				fmt.Print(text)
			case "tool-call":
				var tc struct {
					ToolCallID string `json:"tool_call_id"`
					Tool       string `json:"tool"`
				}
				_ = json.Unmarshal(ev.Payload, &tc)
				if tc.Tool != "" {
					fmt.Printf("\n[工具] %s\n", tc.Tool)
				}
			}
		}
		// final 模式：忽略所有中间输出
		return nil
	})

	// session-complete：轮次终态
	bus.On(msgkeys.TopicSessionComplete, 0, func(_ context.Context, _ string, v *mq.Value) error {
		payload := v.Payload
		var ev struct {
			Session      string `json:"session"`
			Turn         string `json:"turn"`
			Status       string `json:"status"`
			FinishReason string `json:"finish_reason,omitempty"`
			Text         string `json:"text,omitempty"`
		}
		_ = json.Unmarshal(payload, &ev)
		finalText = ev.Text

		if output == "verbose" {
			fmt.Printf("[COMPLETE] %s\n", string(payload))
		}

		close(doneCh)
		return nil
	})

	// task-done：工具完成通知
	bus.On(msgkeys.TopicTaskDone, 0, func(_ context.Context, _ string, v *mq.Value) error {
		payload := v.Payload
		if output == "verbose" {
			fmt.Printf("[TASK] %s\n", string(payload))
		}
		var ev struct {
			TaskID        string `json:"task_id"`
			Tool          string `json:"tool"`
			State         string `json:"state"`
			ResultSummary string `json:"result_summary,omitempty"`
			Error         string `json:"error,omitempty"`
		}
		_ = json.Unmarshal(payload, &ev)
		if output == "sse" {
			if ev.State == "done" && ev.ResultSummary != "" {
				summary := ev.ResultSummary
				if len(summary) > 200 {
					summary = summary[:200] + "..."
				}
				fmt.Printf("[工具完成] %s → %s\n", ev.Tool, summary)
			} else if ev.State == "error" {
				fmt.Printf("[工具错误] %s: %s\n", ev.Tool, ev.Error)
			}
		}
		return nil
	})

	// 发 session-start
	startPayload := map[string]any{
		"instance_id": instanceID,
		"session":     session,
		"turn":        turn,
	}
	if llmModel != "" {
		startPayload["llm"] = llmModel
	}
	if scenario != "" {
		startPayload["scenario_id"] = scenario
	}
	if think != "" {
		startPayload["think"] = think
	}
	if effort != "" {
		startPayload["effort"] = effort
	}
	if output == "verbose" {
		startJSON, _ := json.Marshal(startPayload)
		fmt.Printf("[START] %s\n", string(startJSON))
	}

	startJSON, _ := json.Marshal(startPayload)
	bus.Emit(context.Background(), "session-start", startJSON)

	// 发 session-send（用户消息）
	sendPayload := map[string]any{
		"session": session,
		"turn":    turn,
		"type":    "text-user",
		"content": prompt,
	}
	sendJSON, _ := json.Marshal(sendPayload)
	if output == "verbose" {
		fmt.Printf("[SEND] %s\n", string(sendJSON))
	}
	bus.Emit(context.Background(), "session-send", sendJSON)

	// -- 等待完成 --
	select {
	case <-doneCh:
	case <-ctx.Done():
	}

	if output == "final" && finalText != "" {
		fmt.Println(finalText)
	}
}

func resolveDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

func newUUID() string {
	f, err := os.CreateTemp("", "cli-*.uuid")
	if err != nil {
		return "cli-" + fmt.Sprint(os.Getpid())
	}
	name := filepath.Base(f.Name())
	_ = f.Close()
	_ = os.Remove(f.Name())
	return strings.TrimSuffix(name, ".uuid")
}
