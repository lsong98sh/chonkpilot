package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
	"github.com/lsong98sh/chonkpilot-framework/host/core/lockfile"
)

// registerG75Instance 注册一个绑定 testWorkDir 的测试实例。
func registerG75Instance(t *testing.T, s *Server, id string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"instance_id": id, "client_type": "unittest", "work_dir": testWorkDir,
	})
	if err := s.im.HandleRegister(payload); err != nil {
		t.Fatalf("HandleRegister: %v", err)
	}
}

// TestGatewaySandboxRulesDefaults（G-75）：无用户 security-* 时，规则集默认合并
// work_dir / 项目数据根 / prjusr 数据根 / 系统 temp 为可写条目（不落盘）。
func TestGatewaySandboxRulesDefaults(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerG75Instance(t, s, "ins-g75")

	rules := s.gatewaySandboxRules()
	has := func(dir string) bool {
		for _, r := range rules {
			if r.Writable && strings.EqualFold(r.Dir, dir) {
				return true
			}
		}
		return false
	}
	if !has(testWorkDir) {
		t.Fatalf("缺少 work_dir 默认可写条目: %+v", rules)
	}
	if !has(filepath.Join(testWorkDir, lockfile.ProjectDataDirName)) {
		t.Fatalf("缺少项目数据根默认可写条目: %+v", rules)
	}
	pr, err := data.PrjUsrDir(testWorkDir, "")
	if err != nil {
		t.Fatalf("PrjUsrDir: %v", err)
	}
	if !has(pr) {
		t.Fatalf("缺少 prjusr 数据根默认可写条目: %+v", rules)
	}
	if !has(os.TempDir()) {
		t.Fatalf("缺少系统 temp 默认可写条目: %+v", rules)
	}
}

// TestGatewaySandboxRulesUserEntryKept（G-75 / C1）：用户 security-* 条目保留且**一律视为可写**
// （只写白名单：旧值 writable=false 被忽略），规则集无重复（同目录去重）。
func TestGatewaySandboxRulesUserEntryKept(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerG75Instance(t, s, "ins-g75b")

	userA := t.TempDir()
	userB := t.TempDir()
	set := func(key, dir string, writable bool) {
		t.Helper()
		v := fmt.Sprintf(`{"dir":%q,"writable":%v}`, dir, writable)
		if _, err := s.cfg.ConfigKVSet(facade.ConfigKVSetRequest{
			Domain:     facade.DomainPrjSecurity,
			InstanceID: "ins-g75b",
			Entries:    map[string]string{key: v},
			Scope:      facade.Scope{WorkDir: testWorkDir},
		}); err != nil {
			t.Fatalf("ConfigKVSet(%s): %v", key, err)
		}
	}
	set("sec-a", userA, false) // 旧值：只读 → C1 起忽略，仍按可写
	set("sec-b", userB, true)

	rules := s.gatewaySandboxRules()
	var aFound, bFound bool
	seen := map[string]int{}
	for _, r := range rules {
		seen[strings.ToLower(r.Dir)]++
		if strings.EqualFold(r.Dir, userA) && r.Writable {
			aFound = true
		}
		if strings.EqualFold(r.Dir, userB) && r.Writable {
			bFound = true
		}
	}
	for _, n := range seen {
		if n > 1 {
			t.Fatalf("规则集存在重复条目: %+v", rules)
		}
	}
	if !aFound || !bFound {
		t.Fatalf("用户 security 条目缺失或未视为可写（a=%v b=%v）: %+v", aFound, bFound, rules)
	}
	// WritableRoots（gateway 侧）应含两条用户目录。
	roots := mcpgateway.WritableRoots(rules)
	hit := func(dir string) bool {
		for _, r := range roots {
			if strings.EqualFold(r, dir) {
				return true
			}
		}
		return false
	}
	if !hit(userA) || !hit(userB) {
		t.Fatalf("WritableRoots 缺用户目录: %+v", roots)
	}
}
