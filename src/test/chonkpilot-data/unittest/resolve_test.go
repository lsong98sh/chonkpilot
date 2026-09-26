// 配置分层解析单测（12-数据层）：prjusr → prj → usr → 系统，写只写当前层，删本层即回落。
package data_test

import (
	"errors"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

func TestResolverFallbackChain(t *testing.T) {
	usr := openLayer(t, data.LayerUsr)
	prj := openLayer(t, data.LayerPrj)
	prjUsr := openLayer(t, data.LayerPrjUsr)

	sys := func(key string) (string, bool) {
		if key == "sysOnly" {
			return "S", true
		}
		return "", false
	}
	r := data.NewResolver(prjUsr, prj, usr, sys)

	// 1) 系统兜底
	if v, layer, ok := r.Get("sysOnly"); !ok || v != "S" || layer != data.SystemLayer {
		t.Fatalf("system fallback = (%q,%v,%v)", v, layer, ok)
	}
	// 2) 各层都没有 → ok=false
	if _, _, ok := r.Get("nope"); ok {
		t.Fatal("unknown key should not resolve")
	}

	// 3) 写 usr → 命中 usr
	if err := r.Set(data.LayerUsr, "k", "U"); err != nil {
		t.Fatal(err)
	}
	if v, layer, _ := r.Get("k"); v != "U" || layer != data.LayerUsr {
		t.Fatalf("usr hit = (%q,%v)", v, layer)
	}
	// 4) 写 prj → 覆盖 usr
	if err := r.Set(data.LayerPrj, "k", "P"); err != nil {
		t.Fatal(err)
	}
	if v, layer, _ := r.Get("k"); v != "P" || layer != data.LayerPrj {
		t.Fatalf("prj override = (%q,%v)", v, layer)
	}
	// 5) 写 prjusr → 覆盖 prj
	if err := r.Set(data.LayerPrjUsr, "k", "PU"); err != nil {
		t.Fatal(err)
	}
	if v, layer, _ := r.Get("k"); v != "PU" || layer != data.LayerPrjUsr {
		t.Fatalf("prjusr override = (%q,%v)", v, layer)
	}

	// 6) 删 prjusr → 回落 prj
	if err := r.Reset(data.LayerPrjUsr, "k"); err != nil {
		t.Fatal(err)
	}
	if v, layer, _ := r.Get("k"); v != "P" || layer != data.LayerPrj {
		t.Fatalf("after reset prjusr = (%q,%v)", v, layer)
	}
	// 7) 删 prj → 回落 usr
	if err := r.Reset(data.LayerPrj, "k"); err != nil {
		t.Fatal(err)
	}
	if v, layer, _ := r.Get("k"); v != "U" || layer != data.LayerUsr {
		t.Fatalf("after reset prj = (%q,%v)", v, layer)
	}
	// 8) 删 usr → 不再命中
	if err := r.Reset(data.LayerUsr, "k"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := r.Get("k"); ok {
		t.Fatal("should not resolve after resetting all layers")
	}
}

func TestResolverLayerUnavailable(t *testing.T) {
	usr := openLayer(t, data.LayerUsr)
	r := data.NewResolver(nil, nil, usr, nil) // prj/prjusr 未挂载

	if err := r.Set(data.LayerPrj, "k", "v"); !errors.Is(err, data.ErrLayerUnavailable) {
		t.Fatalf("set unmounted layer = %v", err)
	}
	if err := r.Reset(data.LayerPrjUsr, "k"); !errors.Is(err, data.ErrLayerUnavailable) {
		t.Fatalf("reset unmounted layer = %v", err)
	}
	if r.DB(data.LayerPrj) != nil {
		t.Fatal("unmounted layer DB should be nil")
	}
	// 已挂载层正常
	if err := r.Set(data.LayerUsr, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := r.Get("k"); v != "v" {
		t.Fatalf("usr set/get = %q", v)
	}
}
