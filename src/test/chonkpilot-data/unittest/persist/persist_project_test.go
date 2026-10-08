// 「场景向导」（Agent Wizard）project 域（facade.ProjectAPI）L2 黑盒测试：
//   - 只读探测（空目录判定 / 源码 & 工具链识别）；
//   - 工程规格文件 <workDir>/.chonkpilot/project_spec.md 的存在性 / 读写往返。
//
// 复用本包既有宿主 helper（newTestPersist / regInstance）+ inline 门面绑定
// （facade/inline；与 MQ 面同源实现，见 23 §7）。驱动一律经门面方法，不直开库。
package persist_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/inline"
)

// writeProbeFile 在探测夹具里落一个文件（自动建父目录）。
func writeProbeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// containsStr 判定字符串切片是否含目标值。
func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestProjectProbeEmptyDir：空工作目录 → Empty=true、HasCode=false、FileCount=0。
func TestProjectProbeEmptyDir(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)
	api := inline.New(bus)

	probe, err := api.ProjectProbe(facade.ProjectProbeRequest{InstanceID: facadeInstance})
	if err != nil {
		t.Fatalf("ProjectProbe: %v", err)
	}
	if !probe.Empty {
		t.Fatalf("空目录应 Empty=true：%+v", probe)
	}
	if probe.HasCode {
		t.Fatalf("空目录应 HasCode=false：%+v", probe)
	}
	if probe.FileCount != 0 {
		t.Fatalf("空目录 FileCount=%d want 0", probe.FileCount)
	}
}

// TestProjectProbeDetectsToolchain：落 package.json（vue+vite）+ pnpm-lock.yaml +
// vite.config.ts + src/main.ts → 探测出 HasCode / PackageManager=pnpm / BuildTool=vite /
// Languages 含 TypeScript。
func TestProjectProbeDetectsToolchain(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	wd := regInstance(t, bus)
	api := inline.New(bus)

	writeProbeFile(t, filepath.Join(wd, "package.json"),
		`{"dependencies":{"vue":"^3.4.0"},"devDependencies":{"vite":"^5.0.0"}}`)
	writeProbeFile(t, filepath.Join(wd, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
	writeProbeFile(t, filepath.Join(wd, "vite.config.ts"), "export default {}\n")
	writeProbeFile(t, filepath.Join(wd, "src", "main.ts"), "console.log('hi')\n")

	probe, err := api.ProjectProbe(facade.ProjectProbeRequest{InstanceID: facadeInstance})
	if err != nil {
		t.Fatalf("ProjectProbe: %v", err)
	}
	if !probe.HasCode {
		t.Fatalf("含 .ts 源码应 HasCode=true：%+v", probe)
	}
	if probe.Empty {
		t.Fatalf("非空目录不应 Empty=true：%+v", probe)
	}
	if probe.PackageManager != "pnpm" {
		t.Fatalf("PackageManager=%q want pnpm", probe.PackageManager)
	}
	if probe.BuildTool != "vite" {
		t.Fatalf("BuildTool=%q want vite", probe.BuildTool)
	}
	if !containsStr(probe.Languages, "TypeScript") {
		t.Fatalf("Languages=%v 应含 TypeScript", probe.Languages)
	}
	if !containsStr(probe.Frameworks, "Vue") || !containsStr(probe.Frameworks, "Vite") {
		t.Fatalf("Frameworks=%v 应含 Vue / Vite", probe.Frameworks)
	}
}

// TestProjectSpecWriteReadExists：规格文件读写往返 —— 初始不存在；写入 "hello" 后
// Exists=true、Read.Content=="hello"、落点以 .chonkpilot/project_spec.md 结尾且已落盘。
func TestProjectSpecWriteReadExists(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	wd := regInstance(t, bus)
	api := inline.New(bus)

	// 初始：不存在（Exists=false 不是错误）
	before, err := api.ProjectSpecExists(facade.ProjectSpecExistsRequest{InstanceID: facadeInstance})
	if err != nil {
		t.Fatalf("ProjectSpecExists(before): %v", err)
	}
	if before.Exists {
		t.Fatalf("初始不应存在规格文件：%+v", before)
	}

	// 写入
	wr, err := api.ProjectSpecWrite(facade.ProjectSpecWriteRequest{
		InstanceID: facadeInstance, Content: "hello",
	})
	if err != nil {
		t.Fatalf("ProjectSpecWrite: %v", err)
	}
	if !wr.OK {
		t.Fatalf("ProjectSpecWrite 应 OK=true：%+v", wr)
	}
	if !strings.HasSuffix(wr.Path, ".chonkpilot/project_spec.md") {
		t.Fatalf("落点应以 .chonkpilot/project_spec.md 结尾：%q", wr.Path)
	}

	// 存在性 + 读回
	exists, err := api.ProjectSpecExists(facade.ProjectSpecExistsRequest{InstanceID: facadeInstance})
	if err != nil {
		t.Fatalf("ProjectSpecExists(after): %v", err)
	}
	if !exists.Exists {
		t.Fatalf("写入后应 Exists=true：%+v", exists)
	}
	read, err := api.ProjectSpecRead(facade.ProjectSpecReadRequest{InstanceID: facadeInstance})
	if err != nil {
		t.Fatalf("ProjectSpecRead: %v", err)
	}
	if !read.Exists || read.Content != "hello" {
		t.Fatalf("读回应 Exists=true 且 Content=hello：%+v", read)
	}
	if !strings.HasSuffix(read.Path, ".chonkpilot/project_spec.md") {
		t.Fatalf("读回落点应以 .chonkpilot/project_spec.md 结尾：%q", read.Path)
	}

	// 落盘断言（黑盒：文件确实写在实例工作目录下）
	onDisk, err := os.ReadFile(filepath.Join(wd, ".chonkpilot", "project_spec.md"))
	if err != nil {
		t.Fatalf("规格文件未落盘：%v", err)
	}
	if string(onDisk) != "hello" {
		t.Fatalf("落盘内容=%q want hello", string(onDisk))
	}
}
