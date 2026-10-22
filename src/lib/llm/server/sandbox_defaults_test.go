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
	"github.com/chonkpilot/chonkpilot-lib/lockfile"
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

// TestGatewaySandboxRulesUserEntryKept（G-75）：用户 security-* 条目保留（可写与只读都在），
// 规则集无重复（同目录+同可写性去重）。
func TestGatewaySandboxRulesUserEntryKept(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerG75Instance(t, s, "ins-g75b")

	userRO := t.TempDir()
	userRW := t.TempDir()
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
	set("sec-ro", userRO, false)
	set("sec-rw", userRW, true)

	rules := s.gatewaySandboxRules()
	var roFound, rwFound bool
	dup := 0
	seen := map[string]int{}
	for _, r := range rules {
		k := strings.ToLower(r.Dir)
		if r.Writable {
			k += "|w"
		} else {
			k += "|r"
		}
		seen[k]++
		if strings.EqualFold(r.Dir, userRO) && !r.Writable {
			roFound = true
		}
		if strings.EqualFold(r.Dir, userRW) && r.Writable {
			rwFound = true
		}
	}
	for _, n := range seen {
		if n > 1 {
			dup += n - 1
		}
	}
	if !roFound || !rwFound {
		t.Fatalf("用户 security 条目缺失（ro=%v rw=%v）: %+v", roFound, rwFound, rules)
	}
	if dup != 0 {
		t.Fatalf("规则集存在重复条目: %+v", rules)
	}
	// 可写视图含用户可写目录（等价原 sandbox.WritableRoots 口径：只取 writable=true）。
	found := false
	for _, r := range rules {
		if r.Writable && strings.TrimSpace(r.Dir) != "" && strings.EqualFold(r.Dir, userRW) {
			found = true
		}
	}
	if !found {
		t.Fatalf("WritableRoots 缺用户可写目录: %+v", rules)
	}
}
