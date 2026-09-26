// chonkpilot-data persist 独立入口（对齐 40-演进计划：persist 服务面 + 存储内核
// 同归 chonkpilot-data，可独立启动 data 服务）。
//
// 形态：进程内内存 mq（2026-09-03 去 NATS，61-消息一览 §9）——本入口单独运行时不接驳
// 其他组件，数据服务订阅 data-* 处于待命；嵌入形态（server/gui 宿主）不启动本入口，而由
// 宿主在共享总线上直接 start persist.Service。本入口用途：独立数据服务骨架 + service
// start/stop 生命周期自检；多进程桥接形态就绪后作为持久层宿主进程。
//
// 实例数据根（prj 库）不经命令行注入：persist 自持实例视图，订阅 instance-register
// （携带 work_dir/data_dir）解析——宿主仅需在总线上登记实例（40-演进计划）。
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

func main() {
	// 总线命名空间前缀与宿主一致（业务 publish/subscribe 一律写相对主题）。
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		log.Fatalf("[persist] mq.New: %v", err)
	}
	defer bus.Close()

	svc := persist.New(bus, persist.Options{})
	if err := svc.Start(); err != nil {
		log.Fatalf("[persist] start: %v", err)
	}
	log.Print("[persist] data service started (data-<domain>-* on bus)")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Print("[persist] shutting down ...")
	svc.Stop()
}
