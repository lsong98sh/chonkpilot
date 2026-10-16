package contractmd

import (
	"strings"
	"testing"
)

const sampleTool = "# 基本信息\n" +
	"```yaml\n" +
	"name: web_fetch\n" +
	"title: 网页抓取\n" +
	"runtime: ../../executors/chonkpilot-core-executor.exe\n" +
	"hot: true\n" +
	"category: core\n" +
	"timeout: 120\n" +
	"args: web_fetch --input={RAW-INPUT-FILE}\n" +
	"output: stdout\n" +
	"```\n\n" +
	"# 摘要\n" +
	"抓取网页正文。\n\n" +
	"# 参数\n" +
	"```json\n" +
	"{\n  \"type\": \"object\",\n  \"properties\": {\"url\": {\"type\": \"string\"}},\n  \"required\": [\"url\"]\n}\n" +
	"```\n"

func TestParseTool(t *testing.T) {
	doc, err := Parse(sampleTool)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Name != "web_fetch" {
		t.Fatalf("name = %q", doc.Name)
	}
	if doc.Title != "网页抓取" {
		t.Fatalf("title = %q", doc.Title)
	}
	if doc.Meta["runtime"] != "../../executors/chonkpilot-core-executor.exe" {
		t.Fatalf("runtime = %q", doc.Meta["runtime"])
	}
	if doc.Meta["timeout"] != "120" {
		t.Fatalf("timeout = %q", doc.Meta["timeout"])
	}
	if doc.Description != "抓取网页正文。" {
		t.Fatalf("desc = %q", doc.Description)
	}
	if !strings.Contains(doc.Parameters, "\"url\"") {
		t.Fatalf("params = %q", doc.Parameters)
	}
}

func TestBuildRoundTrip(t *testing.T) {
	doc := Template(KindTool, "my_tool")
	doc.Meta["runtime"] = "x.exe"
	doc.Description = "说明"
	raw := Build(doc)
	back, err := Parse(raw)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if back.Name != "my_tool" || back.Title != "my_tool" {
		t.Fatalf("name/title: %q/%q", back.Name, back.Title)
	}
	if back.Meta["output"] != "stdout" || back.Meta["runtime"] != "x.exe" {
		t.Fatalf("meta: %#v", back.Meta)
	}
	if back.Parameters == "" {
		t.Fatalf("params empty")
	}
}

func TestContentH1Preserved(t *testing.T) {
	raw := "# 基本信息\n```yaml\nname: s\n```\n\n# 正文\n# 我是正文里的 H1\n正文内容\n"
	doc, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Content != "# 我是正文里的 H1\n正文内容" {
		t.Fatalf("content = %q", doc.Content)
	}
	if doc.Description != "" {
		t.Fatalf("desc should be empty, got %q", doc.Description)
	}
}

func TestRefsAndDelegate(t *testing.T) {
	raw := "# 基本信息\n```yaml\nname: a\n```\n\n" +
		"# 参考\n```yaml\nskills:\n  - debug\ntools:\n  - web_fetch\n```\n\n" +
		"# 委托\n当任务需要调试时。\n"
	doc, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Refs["skills"]) != 1 || doc.Refs["skills"][0] != "debug" {
		t.Fatalf("skills = %#v", doc.Refs["skills"])
	}
	if len(doc.Refs["tools"]) != 1 || doc.Refs["tools"][0] != "web_fetch" {
		t.Fatalf("tools = %#v", doc.Refs["tools"])
	}
	if doc.Delegate != "当任务需要调试时。" {
		t.Fatalf("delegate = %q", doc.Delegate)
	}
}

func TestNameRequired(t *testing.T) {
	_, err := Parse("# 基本信息\n```yaml\ntitle: x\n```\n")
	if err == nil {
		t.Fatalf("expected error for missing name")
	}
}

// TestMimetypeOfFile：资源外部内容文件的 mimetype 按后缀识别（确定性表）；未知/无后缀 → octet-stream。
func TestMimetypeOfFile(t *testing.T) {
	cases := map[string]string{
		"config.json":      "application/json",
		"data.yaml":        "application/yaml",
		"notes.md":         "text/markdown",
		"readme.txt":       "text/plain",
		"logo.PNG":         "image/png",
		"page.html":        "text/html",
		"table.csv":        "text/csv",
		"blob.binunknown":  "application/octet-stream",
		"noext":            "application/octet-stream",
	}
	for in, want := range cases {
		if got := MimetypeOfFile(in); got != want {
			t.Fatalf("MimetypeOfFile(%q) = %q, want %q", in, got, want)
		}
	}
}
