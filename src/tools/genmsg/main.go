// genmsg 命令行入口：读 schema → 生成 Go/JS 键常量。
//
//	go run src/tools/genmsg            # 生成（写回仓库）
//	go run src/tools/genmsg -check     # 仅校验生成物是否与 schema 一致（不一致 = 非零退出）
//
// 路径默认相对仓库根（自 cwd 向上查找 docs/spec/60-reference/61-messages.schema.json）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	var (
		schemaPath = flag.String("schema", "", "schema 路径（默认 <repo>/docs/spec/60-reference/61-messages.schema.json）")
		goOut      = flag.String("go", "", "Go 产物路径（默认 <repo>/src/lib/core/msgkeys/msgkeys_gen.go）")
		jsOut      = flag.String("js", "", "JS 产物路径（默认 <repo>/src/frontend/src/events/msgkeys.js）")
		check      = flag.Bool("check", false, "仅校验生成物是否为最新（不写入）")
	)
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if *schemaPath == "" {
		*schemaPath = filepath.Join(root, "docs", "spec", "60-reference", "61-messages.schema.json")
	}
	if *goOut == "" {
		*goOut = filepath.Join(root, "src", "lib", "core", "msgkeys", "msgkeys_gen.go")
	}
	if *jsOut == "" {
		*jsOut = filepath.Join(root, "src", "frontend", "src", "events", "msgkeys.js")
	}

	raw, err := os.ReadFile(*schemaPath)
	if err != nil {
		fatal(fmt.Errorf("读取 schema: %w", err))
	}
	out, err := Generate(raw)
	if err != nil {
		fatal(err)
	}

	if *check {
		if err := checkUpToDate(*goOut, out.Go); err != nil {
			fatal(err)
		}
		if err := checkUpToDate(*jsOut, out.JS); err != nil {
			fatal(err)
		}
		fmt.Println("genmsg: 生成物与 schema 一致")
		return
	}

	if err := writeFile(*goOut, out.Go); err != nil {
		fatal(err)
	}
	if err := writeFile(*jsOut, out.JS); err != nil {
		fatal(err)
	}
	fmt.Printf("genmsg: 已生成\n  %s\n  %s\n", *goOut, *jsOut)
}

// repoRoot 自 cwd 向上查找含 schema 的仓库根目录。
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		cand := filepath.Join(dir, "docs", "spec", "60-reference", "61-messages.schema.json")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到仓库根（自 %s 向上无 61-messages.schema.json）", dir)
		}
		dir = parent
	}
}

// checkUpToDate 比对已提交文件与生成内容（逐字）。
func checkUpToDate(path, want string) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s 不存在或不可读（请重新生成）: %w", path, err)
	}
	if string(got) != want {
		return fmt.Errorf("%s 与 schema 不一致（请重新生成：go run src/tools/genmsg）", path)
	}
	return nil
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// 显式 UTF8（含非 ASCII 注释）。
	return os.WriteFile(path, []byte(content), 0o644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "genmsg: "+err.Error())
	os.Exit(1)
}
