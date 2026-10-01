// chonkpilot-cli：CLI 单体（批处理/脚本模式，工具调用强制同步，无 WebView2）。
//
// 单进程内嵌：filesys + data/persist + LLM server + gateway + mcp-server + plugins，
// 与 GUI 单体共享同一 lib 集，通过内存 MQ 通讯。
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

	"github.com/chonkpilot/chonkpilot-data/facade/inline"
	"github.com/chonkpilot/chonkpilot-filesys"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-llm/server"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin-codegraph"
	"github.com/chonkpilot/chonkpilot-plugin-compress"
	"github.com/chonkpilot/chonkpilot-plugin-history"
	"github.com/chonkpilot/chonkpilot-plugin-memory"
	"github.com/chonkpilot/chonkpilot-plugin-vfts"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
)

func main() {
	var (
		prompt     string
		promptFile string
		scenario   string
		workDir    string
		dataDir    string
		output     string
		llmModel   string
		think      string
		effort     string
	)
	flag.StringVar(&prompt, "prompt", "", "提示词（与 --prompt-file 二选一）")
	flag.StringVar(&promptFile, "prompt-file", "", "提示词文件路径（与 --prompt 二选一）")
	flag.StringVar(&scenario, "scenario", "", "场景目录名（空=默认场景）")
	flag.StringVar(&workDir, "work-dir", "", "工作目录（默认 cwd）")
	flag.StringVar(&dataDir, "data-dir", "", "数据根（不传=真实根；留空=强制临时隔离；给路径=该路径作数据根）")
	flag.StringVar(&output, "output", "sse", "输出模式：sse（流式）/ final（仅最终结果）/ verbose（全报文 dump）")
	flag.StringVar(&llmModel, "llm", "", "LLM 模型名（默认 server 配置）")
	flag.StringVar(&think, "think", "", "思考模式（high / medium / low）")
	flag.StringVar(&effort, "effort", "", "思考力度（high / medium / low）")
	flag.Parse()

	// dataDirSet 区分「--data-dir 未传」与「--data-dir=（显式留空）」（三态数据根语义，见 dataprep.go）
	dataDirSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "data-dir" {
			dataDirSet = true
		}
	})

	if prompt == "" && promptFile == "" {
		// 尝试从 stdin 读取
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			fmt.Fprintln(os.Stderr, "请提供 --prompt 或 --prompt-file，或通过 stdin 传入提示词")
			os.Exit(1)
		}
		scanner := bufio.NewScanner(os.Stdin)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
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

	switch output {
	case "sse", "final", "verbose":
	default:
		fmt.Fprintf(os.Stderr, "无效输出模式 %q，可选：sse / final / verbose\n", output)
		os.Exit(1)
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

	// -- 数据根（三态语义：未传=真实根 / 留空=临时隔离 / 路径=该路径作数据根） --
	instDataDir, cleanupData, err := prepareDataDir(workDir, dataDir, dataDirSet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[cli] prepare data dir:", err)
		os.Exit(1)
	}
	defer cleanupData()
	dataDir = instDataDir

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
			// 插件依赖的门面绑定在**装配处**选择（23 §7）：本形态 = inline（同进程直调；bus 传宿主
			// 实际总线 → config 域写入的变更广播照旧送达订阅方，见 facade/inline）
			compress.New(compress.DefaultOptions(), inline.New(bus)),
			memory.New(memory.DefaultOptions()),
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
	bus.Emit(context.Background(), "instance-register", instanceJSON)

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
	bus.On("session-receive", 0, func(_ context.Context, _ string, v *mq.Value) error {
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
					ToolCallID string `json:"tool-call-id"`
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
	bus.On("session-complete", 0, func(_ context.Context, _ string, v *mq.Value) error {
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
	bus.On("task-done", 0, func(_ context.Context, _ string, v *mq.Value) error {
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
