package browser

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// ckAttrRe 匹配打标属性（导出 DOM 前清理残留）。
var ckAttrRe = regexp.MustCompile(`\sdata-ck-loc="[a-z0-9]+"`)

// locatorJS 注入页面一次。内核 __ckResolve(raw) 返回匹配元素数组（不打标不清理）；
// 上层包装：
//   - __ckFind(raw, tag)：解析后给首元素打 data-ck-loc=<tag>，返回 true/false（供动作）
//   - __ckProbe(raw, mode, want)：只读探测（visible/enabled/text/count/attr/exists/url/title）
const locatorJS = `
window.__ckResolve = function(raw) {
  var segs = raw.split('>>');
  var nodes = null;
  for (var si = 0; si < segs.length; si++) {
    var seg = segs[si].trim();
    if (seg === '') continue;
    if (seg.indexOf('nth=') === 0) {
      if (!nodes || nodes.length === 0) return [];
      var n = parseInt(seg.slice(4), 10);
      if (isNaN(n)) return [];
      if (n < 0) n = nodes.length + n;
      if (n < 0 || n >= nodes.length) return [];
      nodes = [nodes[n]];
      continue;
    }
    var scope = (nodes && nodes.length) ? nodes[0] : document;
    var hits = [];
    if (seg.indexOf('css=') === 0) {
      var q1 = seg.slice(4);
      var l1 = (scope === document) ? document.querySelectorAll(q1) : scope.querySelectorAll(q1);
      for (var i1 = 0; i1 < l1.length; i1++) hits.push(l1[i1]);
    } else if (seg.indexOf('testid=') === 0) {
      var q2 = '[data-testid="' + seg.slice(7).replace(/"/g, '\\"') + '"]';
      var l2 = (scope === document) ? document.querySelectorAll(q2) : scope.querySelectorAll(q2);
      for (var i2 = 0; i2 < l2.length; i2++) hits.push(l2[i2]);
    } else if (seg.indexOf('xpath=') === 0) {
      var xr = document.evaluate(seg.slice(6), scope, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
      for (var x = 0; x < xr.snapshotLength; x++) hits.push(xr.snapshotItem(x));
    } else if (seg.indexOf('text=') === 0) {
      var want = seg.slice(5);
      var cands = (scope === document) ? document.querySelectorAll('body *') : scope.querySelectorAll('*');
      var f1 = null;
      for (var t = 0; t < cands.length; t++) {
        var el = cands[t];
        if (el.children.length === 0 && el.textContent && el.textContent.trim() === want) { f1 = el; break; }
      }
      if (f1) hits.push(f1);
    } else if (seg.indexOf('role=') === 0) {
      var rr = seg.slice(5);
      var roleName = rr, nameWant = '';
      var m = rr.match(/^([^[]+)\[name=(.+)\]$/);
      if (m) { roleName = m[1]; nameWant = m[2].replace(/^"|"$/g, ''); }
      var els2 = (scope === document) ? document.querySelectorAll('body *') : scope.querySelectorAll('*');
      for (var e2 = 0; e2 < els2.length; e2++) {
        var el2 = els2[e2];
        var isBtn = el2.tagName === 'BUTTON' || (el2.tagName === 'INPUT' && (el2.type === 'button' || el2.type === 'submit'));
        var r = (el2.getAttribute('role') || '').toLowerCase();
        if (r !== roleName.toLowerCase() && !(roleName.toLowerCase() === 'button' && isBtn)) continue;
        if (nameWant !== '') {
          var label = (el2.getAttribute('aria-label') || el2.textContent || '').trim();
          if (label !== nameWant) continue;
        }
        hits.push(el2); break;
      }
    } else {
      var q3 = seg;
      var l3 = (scope === document) ? document.querySelectorAll(q3) : scope.querySelectorAll(q3);
      for (var i3 = 0; i3 < l3.length; i3++) hits.push(l3[i3]);
    }
    if (hits.length === 0) return [];
    nodes = hits;
  }
  return nodes || [];
};
window.__ckFind = function(raw, tag) {
  var olds = document.querySelectorAll('[data-ck-loc]');
  for (var i = 0; i < olds.length; i++) olds[i].removeAttribute('data-ck-loc');
  var els = window.__ckResolve(raw);
  if (els.length === 0) return false;
  els[0].setAttribute('data-ck-loc', tag);
  return true;
};
window.__ckProbe = function(raw, mode, want) {
  if (raw === 'page') {
    if (mode === 'url') return { ok: true, value: location.href };
    if (mode === 'title') return { ok: true, value: document.title };
    return { ok: false, value: null };
  }
  var els = window.__ckResolve(raw);
  if (els.length === 0) return { ok: false, value: null };
  var el = els[0];
  switch (mode) {
    case 'exists': return { ok: true, value: true };
    case 'visible': {
      var r = el.getBoundingClientRect();
      var cs = window.getComputedStyle(el);
      var vis = r.width > 0 && r.height > 0 && cs.visibility !== 'hidden' && cs.display !== 'none';
      return { ok: true, value: vis };
    }
    case 'enabled': {
      var dis = el.disabled === true || el.getAttribute('aria-disabled') === 'true' || el.classList.contains('disabled');
      return { ok: true, value: !dis };
    }
    case 'text': return { ok: true, value: (el.textContent || '').trim() };
    case 'count': return { ok: true, value: els.length };
    case 'attr': return { ok: true, value: el.getAttribute(String(want)) };
    default: return { ok: false, value: null };
  }
};
`

// probeResult 解码 __ckProbe 返回。
type probeResult struct {
	OK    bool        `json:"ok"`
	Value interface{} `json:"value"`
}

// ensureLocatorJS 幂等注入。
func ensureLocatorJS() string {
	return `if (!window.__ckResolve) { ` + locatorJS + ` } true;`
}

// resolveJS 动作定位：给匹配元素打 tag，返回 bool。
func resolveJS(locator, tag string) string {
	return fmt.Sprintf(`window.__ckFind(%s, %s);`, quote(locator), quote(tag))
}

// probeJS 只读探测。
func probeJS(locator, mode string, want string) string {
	return fmt.Sprintf(`window.__ckProbe(%s, %s, %s);`, quote(locator), quote(mode), quote(want))
}

// ckSelector 标记元素 css。
func ckSelector(tag string) string { return `[data-ck-loc="` + tag + `"]` }

// newTag 随机打标 ID。
func newTag() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "ck" + hex.EncodeToString(b)
}

// quote JS 字符串安全包装（单引号包裹；转义 \、' 及换行/回车，避免注入与语法破坏）。
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
