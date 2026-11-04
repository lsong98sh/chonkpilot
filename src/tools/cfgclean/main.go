// cfgclean —— 一次性清理「配置迁入 plugins.<id>.* 之前」的旧宿主键（G-82④）。
//
// **外挂工具，不参与任何构建 / 发布**：只读或改写单个 chonkpilot.db 的 `config` 表。
// 旧键的唯一事实源 = 插件仓 `chonkpilot-plugins/codegraph/src/config.go` 的 `legacyKeyMap`；
// 本工具的 legacyKeys 是它的一份**人工镜像**（迁移完成后旧键会整体消失，届时删除本工具即可）。
//
// 用法（先干跑，确认无误再 -apply）：
//
//	go run . -db "E:\proj\.chonkpilot\chonkpilot.db"
//	go run . -db "...\chonkpilot.db" -migrate -apply
//
// 参数：
//
//	-db      逗号分隔的 chonkpilot.db 路径（prj 层；prjusr / usr 同理）
//	-migrate 旧键存在且新键缺失时，先复制到新键（值为 {"v": ...} 记录），再删旧键
//	-apply   真正写入（缺省只打印将要发生的改动）
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// legacyKeys 旧宿主键 → 新命名空间键（镜像 codegraph legacyKeyMap；只用于清理 / 保守迁移）。
var legacyKeys = []struct{ Old, New string }{
	{"enable-codegraph", "plugins.codegraph.enabled"},
	{"codegraph.exts", "plugins.codegraph.exts"},
	{"codegraph.skip-dirs", "plugins.codegraph.skip-dirs"},
	{"codegraph.stack-gitignore", "plugins.codegraph.stack-gitignore"},
	{"enable-vfts", "plugins.codegraph.vfts.enabled"},
	{"vfts.exts", "plugins.codegraph.vfts.exts"},
	{"vfts.skip-dirs", "plugins.codegraph.vfts.skip-dirs"},
	{"vfts.stack-gitignore", "plugins.codegraph.vfts.stack-gitignore"},
	{"vfts.docs", "plugins.codegraph.vfts.docs"},
	{"vfts.doc-max-mb", "plugins.codegraph.vfts.doc-max-mb"},
}

const configBucket = "config"

func main() {
	dbList := flag.String("db", "", "逗号分隔的 chonkpilot.db 路径（必填）")
	migrate := flag.Bool("migrate", false, "旧键存在且新键缺失时先复制到新键，再删旧键")
	apply := flag.Bool("apply", false, "真正写入（缺省只干跑）")
	flag.Parse()

	paths := splitPaths(*dbList)
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "缺少 -db：请给出至少一个 chonkpilot.db 路径")
		flag.Usage()
		os.Exit(2)
	}
	failed := false
	for _, p := range paths {
		if err := cleanOne(p, *migrate, *apply); err != nil {
			fmt.Fprintf(os.Stderr, "[FAIL] %s: %v\n", p, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func splitPaths(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cleanOne(path string, migrate, apply bool) error {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second, ReadOnly: !apply})
	if err != nil {
		return err
	}
	defer db.Close()

	mode := "DRY-RUN"
	if apply {
		mode = "APPLY"
	}
	fmt.Printf("== %s（%s）\n", path, mode)

	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(configBucket))
		if b == nil {
			fmt.Println("   （无 config 桶，跳过）")
			return nil
		}
		for _, lm := range legacyKeys {
			oldVal := b.Get([]byte(lm.Old))
			if oldVal == nil {
				continue
			}
			newExists := b.Get([]byte(lm.New)) != nil
			switch {
			case newExists:
				fmt.Printf("   - %-28s 新键已存在 → 删除旧键\n", lm.Old)
			case migrate:
				fmt.Printf("   → %-28s 复制到 %s 后删除旧键\n", lm.Old, lm.New)
			default:
				fmt.Printf("   - %-28s 仅删除旧键（未指定 -migrate，值丢弃）\n", lm.Old)
			}
			if !apply {
				continue
			}
			if migrate && !newExists {
				if err := b.Put([]byte(lm.New), oldVal); err != nil {
					return fmt.Errorf("写新键 %s 失败：%w", lm.New, err)
				}
			}
			if err := b.Delete([]byte(lm.Old)); err != nil {
				return fmt.Errorf("删旧键 %s 失败：%w", lm.Old, err)
			}
		}
		return nil
	})
}
