package fileops

import (
	"fmt"
	"os"
	"strings"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// HandleDiff 生成两文件（或两组路径）间的统一 diff。
func HandleDiff(workDir string, args map[string]interface{}) *cli.Result {
	// pair 携带两侧参数的来源标签（la/lb），便于路径违规时指明是哪一个。
	type pair struct{ a, b, la, lb string }
	var pairs []pair

	if raw, ok := args["files"].([]interface{}); ok {
		for i, r := range raw {
			if m, ok := r.(map[string]interface{}); ok {
				p, _ := m["path"].(string)
				p2, _ := m["path2"].(string)
				if p != "" && p2 != "" {
					pairs = append(pairs, pair{p, p2,
						fmt.Sprintf("files[%d].path", i), fmt.Sprintf("files[%d].path2", i)})
				}
			}
		}
	}
	if f1, _ := args["file1"].(string); f1 != "" {
		if f2, _ := args["file2"].(string); f2 != "" {
			pairs = append(pairs, pair{f1, f2, "file1", "file2"})
		}
	}
	if raw, ok := args["path"].([]interface{}); ok && len(raw) == 2 {
		a, _ := raw[0].(string)
		b, _ := raw[1].(string)
		if a != "" && b != "" {
			pairs = append(pairs, pair{a, b, "path[0]", "path[1]"})
		}
	}
	if len(pairs) == 0 {
		return cli.Err("file_diff", "provide file1+file2, path:[a,b], or files:[{path,path2}]")
	}

	// 参数级路径违规（R-11）→ 顶层错误（指明来源参数）
	for _, pr := range pairs {
		if msg := ValidateField(pr.la, pr.a); msg != "" {
			return cli.Err("file_diff", msg)
		}
		if msg := ValidateField(pr.lb, pr.b); msg != "" {
			return cli.Err("file_diff", msg)
		}
	}

	dmp := diffmatchpatch.New()
	var outputs []string
	for _, pr := range pairs {
		ra, errMsg := ResolvePath(pr.a, workDir)
		if errMsg != "" {
			return cli.Err("file_diff", errMsg)
		}
		rb, errMsg := ResolvePath(pr.b, workDir)
		if errMsg != "" {
			return cli.Err("file_diff", errMsg)
		}
		// agentbox 沙箱（仅隔离开启时生效）：两侧文件均须在允许读目录内
		if err := sandboxErr(ra, false); err != nil {
			return cli.Err("file_diff", err.Error())
		}
		if err := sandboxErr(rb, false); err != nil {
			return cli.Err("file_diff", err.Error())
		}
		da, err := os.ReadFile(ra)
		if err != nil {
			return cli.Err("file_diff", fmt.Sprintf("read %s: %s", pr.a, err))
		}
		db, err := os.ReadFile(rb)
		if err != nil {
			return cli.Err("file_diff", fmt.Sprintf("read %s: %s", pr.b, err))
		}
		textA := DecodeText(da)
		textB := DecodeText(db)
		diffs := dmp.DiffMain(textA, textB, false)
		dmp.DiffCleanupSemantic(diffs)
		outputs = append(outputs, renderUnifiedDiff(pr.a, pr.b, diffs))
	}
	return cli.Ok("file_diff", strings.Join(outputs, "\n"), map[string]interface{}{"pairs": len(pairs)})
}

// renderUnifiedDiff 生成简化的统一 diff 文本（- / + / 空格前缀行）。
func renderUnifiedDiff(a, b string, diffs []diffmatchpatch.Diff) string {
	var bd strings.Builder
	fmt.Fprintf(&bd, "--- %s\n+++ %s\n", a, b)
	for _, d := range diffs {
		lines := strings.Split(d.Text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for _, ln := range lines {
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
