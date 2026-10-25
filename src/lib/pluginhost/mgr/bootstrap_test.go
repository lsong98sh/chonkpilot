package mgr

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }

func TestParseBootstrap_NoSection(t *testing.T) {
	in := `
# comment
plugins/codegraph/chonkpilot-plugin-codegraph.exe
plugins/vfts/chonkpilot-plugin-vfts.exe --data "C:\Program Files\ck"
`
	got, err := ParseBootstrap(stringsReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Exe != "plugins/codegraph/chonkpilot-plugin-codegraph.exe" {
		t.Fatalf("exe = %q", got[0].Exe)
	}
	if !reflect.DeepEqual(got[1].Args, []string{"--data", `C:\Program Files\ck`}) {
		t.Fatalf("args = %#v", got[1].Args)
	}
	if got[0].Line != 3 || got[1].Line != 4 {
		t.Fatalf("lines = %d,%d", got[0].Line, got[1].Line)
	}
}

func TestParseBootstrap_BootstrapSectionOnly(t *testing.T) {
	in := `
[other]
plugins/ignored.exe

[bootstrap]
plugins/codegraph/chonkpilot-plugin-codegraph.exe
plugins/vfts/chonkpilot-plugin-vfts.exe

[again]
plugins/ignored2.exe
`
	got, err := ParseBootstrap(stringsReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %#v", len(got), got)
	}
	for _, e := range got {
		if e.Exe == "plugins/ignored.exe" || e.Exe == "plugins/ignored2.exe" {
			t.Fatalf("section filter failed: %q", e.Exe)
		}
	}
}

func TestParseBootstrap_UnbalancedQuote(t *testing.T) {
	if _, err := ParseBootstrap(stringsReader("a.exe --x \"oops")); err == nil {
		t.Fatal("want error")
	}
}

func TestLoadBootstrap_MissingFile(t *testing.T) {
	got, err := LoadBootstrap(filepath.Join(t.TempDir(), "plugin.ini"))
	if err != nil || got != nil {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestLoadBootstrap_File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plugin.ini")
	if err := os.WriteFile(p, []byte("[bootstrap]\nplugins/a/a.exe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBootstrap(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Exe != "plugins/a/a.exe" {
		t.Fatalf("got %#v", got)
	}
}
