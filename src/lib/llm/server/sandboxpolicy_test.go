package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-framework/host/core/lockfile"
)

// SANDBOX-WRITE-1 P2：未命中在册实例 → 只给内置默认项（work_dir + <work_dir>/.chonkpilot/tmp）。
func TestSandboxPolicyForWorkDirDefaultsOnly(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	wd := t.TempDir()

	dirs, err := s.SandboxPolicyForWorkDir(wd)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("只应有 2 条内置默认项: %#v", dirs)
	}
	if !strings.EqualFold(dirs[0], wd) {
		t.Fatalf("首项应为 work_dir: %#v", dirs)
	}
	if want := filepath.Join(wd, lockfile.ProjectDataDirName, "tmp"); !strings.EqualFold(dirs[1], want) {
		t.Fatalf("次项应为 tmp: got %q want %q", dirs[1], want)
	}

	if _, err := s.SandboxPolicyForWorkDir("  "); err == nil {
		t.Fatal("空 work_dir 须报错")
	}
}

// 命中在册实例 → 合并该实例的 prj security-*（只写白名单），并与默认项去重。
func TestSandboxPolicyForWorkDirMergesSecurity(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerG75Instance(t, s, "ins-p2")

	userDir := t.TempDir()
	val := fmt.Sprintf(`{"dir":%q}`, userDir)
	if _, err := s.cfg.ConfigKVSet(facade.ConfigKVSetRequest{
		Domain:     facade.DomainPrjSecurity,
		InstanceID: "ins-p2",
		Entries:    map[string]string{"sec-p2": val},
		Scope:      facade.Scope{WorkDir: testWorkDir},
	}); err != nil {
		t.Fatalf("ConfigKVSet: %v", err)
	}

	dirs, err := s.SandboxPolicyForWorkDir(testWorkDir)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	has := func(dir string) bool {
		for _, d := range dirs {
			if strings.EqualFold(d, dir) {
				return true
			}
		}
		return false
	}
	if !has(testWorkDir) || !has(userDir) {
		t.Fatalf("应含 work_dir 与用户 security 条目: %#v", dirs)
	}
	// 去重：同目录（大小写 / 分隔符归一）只出现一次。
	seen := map[string]int{}
	for _, d := range dirs {
		seen[strings.ToLower(filepath.Clean(d))]++
	}
	for _, n := range seen {
		if n > 1 {
			t.Fatalf("白名单存在重复条目: %#v", dirs)
		}
	}
}
