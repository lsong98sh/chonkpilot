"""E2E：插件一览 / 各插件配置页与详情页 / 启用禁用（工具面 + 功能面）/ 多主题背景。

覆盖（全部经 mq 消息驱动 + DOM/消息面回读断言）：
  A. 插件一览（`preview-tab-open{kind:'plugin-manager'}`）在 light / dark / nord 下：
     卡片背景 / 边框 / 文本对比度 / 字号 / 横向溢出 / 开关轨道色 / 状态栏 badge iframe 背景。
  B. 每个插件的配置页（`ui.views` id=config，共 9 个）与详情页（codegraph status /
     crontab tasks / web status）：iframe 就绪、`ck-theme` 运行态、背景随主题即时变化、
     无横向溢出、控件与文案非空。
  C. 启用 / 禁用开关（逐个插件，UI 开关点击）：plugin-list enabled/status、工具面
     `tools-list` 中该插件工具出现/消失、配置资源（HEAD `/plugins/<id>/webui/…`）
     200/404、插件禁用后 RPC 失败；禁用态配置页显示「插件未就绪」占位。
  D. 结果：PASS/FAIL 表 + `E:\\TEMP\\ck-plugin-e2e` 截图；结束时恢复启用表与主题。

入口：`python test_plugin_config_e2e.py`（需要 `-Debug` 构建，含 `--test-port`）。
"""
import json
import os
import re
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness as _h  # noqa: E402
from drive import Checker  # noqa: E402

PORT = _h.free_port()
SHOT_DIR = r"E:\TEMP\ck-plugin-e2e"
# 固定工作区（不随套件删除）：异常退出时保留 `<work>/.chonkpilot/logs/gui.log` 供定位。
WORK_DIR = r"E:\TEMP\ck-plugin-cfg-ws"
THEMES = ["light", "dark", "nord"]
# 详情页（非 config 的 ui.views）；badge 属状态栏，单独在 A 段校验。
DETAIL_VIEWS = [("codegraph", "status", "webui/status.html"),
                ("crontab", "tasks", "webui/tasks.html"),
                ("web", "status", "webui/status.html")]
CFG_ENTRY = "webui/config.html"


# ── 基础工具 ──────────────────────────────────────────────
def J(gui, js):
    """`/eval` 的 result 可能多包一层 JSON 字符串（字符串返回值会被再编码）→ 逐层解到原生值。"""
    r = gui.eval(js)
    for _ in range(4):
        if not isinstance(r, str):
            return r
        try:
            r = json.loads(r)
        except Exception:
            return r
    return r


def poll(gui, js, pred, timeout=20.0, interval=0.5):
    """轮询 eval 结果并套谓词；返回最后一次结果（超时不抛，由断言判定）。"""
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        try:
            last = J(gui, js)
        except Exception:
            last = None
        if pred(last):
            return last
        time.sleep(interval)
    return last


def plugins(gui):
    return (gui.req("plugin-list", {}, timeout=30000) or {}).get("plugins") or []


def plugin_of(gui, pid):
    return next((p for p in plugins(gui) if p.get("id") == pid), None)


def tool_names(gui):
    res = gui.req("tools-list", {}, timeout=30000) or {}
    out = set()
    for t in res.get("tools") or []:
        if isinstance(t, str):
            out.add(t)
        elif isinstance(t, dict) and t.get("name"):
            out.add(t["name"])
    return out


def set_theme(gui, theme):
    gui.eval("window.mq.emit('theme-select', {themeId: %s}); 'ok'" % json.dumps(theme))
    ok = _h.wait_probe(gui, [
        "document.documentElement.getAttribute('data-theme') === %s" % json.dumps(theme),
    ], timeout=15.0)
    _h.wait_idle(gui, max_wait=10.0)
    return ok


def shot(gui, name):
    try:
        os.makedirs(SHOT_DIR, exist_ok=True)
        path = os.path.join(SHOT_DIR, name)
        gui.screenshot(path)
        return path
    except Exception as e:
        print("[shot] %s 失败: %s" % (name, e), flush=True)
        return ""


def open_manager(gui, want_cards=0):
    gui.eval("window.mq.emit('preview-tab-open', {kind:'plugin-manager'}); 'ok'")
    ok = _h.wait_probe(gui, ["!!document.querySelector('.plugin-manager')"], timeout=20.0)
    if want_cards:
        ok = ok and _h.wait_probe(
            gui, ["document.querySelectorAll('.pm-card').length >= %d" % want_cards], timeout=30.0)
    return ok


def card_expr(pid):
    """按 `.pm-meta`（`<id> · v<ver>`）定位卡片（id 唯一，避免与中文名/同名前缀混淆）。"""
    return ("[...document.querySelectorAll('.pm-card')].find(c=>"
            "((c.querySelector('.pm-meta')||{}).textContent||'').trim().startsWith(%s))" % json.dumps(pid))


# ── 颜色/对比度 ────────────────────────────────────────────
def parse_rgb(s):
    m = re.search(r"rgba?\(([^)]+)\)", s or "")
    if not m:
        return None
    parts = [p.strip() for p in m.group(1).split(",")]
    try:
        r, g, b = (int(round(float(x))) for x in parts[:3])
        a = float(parts[3]) if len(parts) > 3 else 1.0
    except ValueError:
        return None
    return (r, g, b, a)


def flat(c, bg):
    if c is None:
        return None
    if c[3] >= 0.999:
        return c[:3]
    return tuple(int(round(c[i] * c[3] + bg[i] * (1 - c[3]))) for i in range(3))


def rel_lum(c):
    def f(v):
        v /= 255.0
        return v / 12.92 if v <= 0.03928 else ((v + 0.055) / 1.055) ** 2.4
    return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2])


def contrast(fg, bg):
    if not fg or not bg:
        return 0.0
    l1, l2 = rel_lum(fg), rel_lum(bg)
    hi, lo = max(l1, l2), min(l1, l2)
    return (hi + 0.05) / (lo + 0.05)


# ── 页面探针 ──────────────────────────────────────────────
MANAGER_JS = r"""
(() => {
  const probe = (css) => {
    const d = document.createElement('div');
    d.style.cssText = 'position:absolute;visibility:hidden;' + css;
    document.body.appendChild(d);
    const v = getComputedStyle(d).backgroundColor;
    d.remove();
    return v;
  };
  const rr = (el) => { const r = el.getBoundingClientRect();
    return {x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height)}; };
  const g = document.querySelector('.pm-grid');
  return JSON.stringify({
    theme: document.documentElement.getAttribute('data-theme'),
    expect: {
      panel: probe('background:var(--panel-bg)'),
      bg: probe('background:var(--bg-primary)'),
      border: probe('background:var(--border)'),
      text: probe('background:var(--text-primary)'),
      muted: probe('background:var(--text-muted)'),
      accent: probe('background:var(--accent)'),
      bar: probe('background:var(--bg-secondary)')
    },
    grid: g ? Object.assign(rr(g), {sw: g.scrollWidth, cw: g.clientWidth}) : null,
    cards: [...document.querySelectorAll('.pm-card')].map((c) => {
      const cs = getComputedStyle(c);
      const nm = c.querySelector('.pm-name'), cmd = c.querySelector('.pm-cmd-value');
      const bt = c.querySelector('.pm-btn'), sw = c.querySelector('.pm-switch .b-switch');
      const dot = c.querySelector('.pm-dot'), desc = c.querySelector('.pm-desc');
      return {
        meta: ((c.querySelector('.pm-meta') || {}).textContent || '').trim(),
        off: c.classList.contains('is-off'), rect: rr(c),
        bg: cs.backgroundColor, border: cs.borderColor, color: cs.color, fs: cs.fontSize,
        overflow: c.scrollWidth > c.clientWidth + 1,
        cmdBg: cmd ? getComputedStyle(cmd).backgroundColor : '',
        cmdColor: cmd ? getComputedStyle(cmd).color : '',
        cmdOverflow: cmd ? cmd.scrollWidth > cmd.clientWidth + 1 : null,
        btnColor: bt ? getComputedStyle(bt).color : '',
        btnBg: bt ? getComputedStyle(bt).backgroundColor : '',
        btnFs: bt ? getComputedStyle(bt).fontSize : '',
        swBg: sw ? getComputedStyle(sw).backgroundColor : '',
        swChecked: sw ? sw.classList.contains('is-checked') : null,
        swW: sw ? Math.round(sw.getBoundingClientRect().width) : 0,
        dotBg: dot ? getComputedStyle(dot).backgroundColor : '',
        descColor: desc ? getComputedStyle(desc).color : '',
        nameFs: nm ? getComputedStyle(nm).fontSize : ''
      };
    }),
    statusbar: [...document.querySelectorAll('.statusbar iframe')].map((f) => {
      let bg = '', th = '', color = '', text = '';
      try {
        const d = f.contentDocument;
        if (d && d.body) {
          bg = getComputedStyle(d.body).backgroundColor;
          color = getComputedStyle(d.body).color;
          th = d.documentElement.getAttribute('data-theme') || '';
          text = (d.body.innerText || '').replace(/\s+/g, ' ').trim().slice(0, 40);
        }
      } catch (e) { /* 跨源忽略 */ }
      const r = f.getBoundingClientRect();
      return {src: f.getAttribute('src') || '', bg: bg, theme: th, color: color, text: text,
              w: Math.round(r.width), h: Math.round(r.height)};
    })
  });
})()
"""


def iframe_js(pid, entry=""):
    """取承载 `/plugins/<pid>/<entry>` 的 iframe，并回读其文档主题/背景/布局/控件。"""
    frag = "/plugins/%s/%s" % (pid, entry) if entry else "/plugins/%s/" % pid
    return r"""
(() => {
  const probe = (css) => {
    const d = document.createElement('div');
    d.style.cssText = 'position:absolute;visibility:hidden;' + css;
    document.body.appendChild(d);
    const v = getComputedStyle(d).backgroundColor;
    d.remove();
    return v;
  };
  const f = [...document.querySelectorAll('iframe.pph-frame')]
    .find((x) => (x.getAttribute('src') || '').indexOf(%s) >= 0);
  const hostBg = probe('background:var(--bg-primary)');
  if (!f) return JSON.stringify({found: false, hostBg: hostBg,
    placeholders: document.querySelectorAll('.pph-placeholder').length});
  let d = null, err = '';
  try { d = f.contentDocument; } catch (e) { err = String(e); }
  if (!d || !d.body) return JSON.stringify({found: true, inaccessible: true,
    src: f.getAttribute('src'), err: err, hostBg: hostBg});
  const de = d.documentElement, b = getComputedStyle(d.body), r = f.getBoundingClientRect();
  let cur = '';
  try { cur = f.contentWindow.ckTheme ? f.contentWindow.ckTheme.current() : ''; } catch (e) {}
  return JSON.stringify({
    found: true, src: f.getAttribute('src'), ready: de.readyState,
    themeAttr: de.getAttribute('data-theme'), ckTheme: cur,
    bodyBg: b.backgroundColor, bodyColor: b.color,
    ckBg: getComputedStyle(de).getPropertyValue('--ck-bg').trim(),
    hostBg: hostBg,
    controls: d.querySelectorAll('input,select,textarea,button').length,
    overflowX: de.scrollWidth - de.clientWidth,
    rect: {w: Math.round(r.width), h: Math.round(r.height)},
    title: d.title || '',
    text: (d.body.innerText || '').replace(/\s+/g, ' ').trim().slice(0, 160)
  });
})()
""" % json.dumps(frag)


def page_loaded(v):
    """iframe 文档已解析出实际内容（仅 bodyBg 非空会在 HTML 尚未解析时误判为「就绪」）。"""
    return (isinstance(v, dict) and v.get("found") and not v.get("inaccessible")
            and (v.get("controls", 0) >= 1 or len(v.get("text") or "") >= 10))


def _enabled_table():
    """读插件启用表 `enabled.json`。

    位置 = 插件平台数据根 `<DataDir>/plugins/enabled.json`；desktop 缺省 DataDir =
    `%LOCALAPPDATA%\\chonkpilot`（framework `pluginDataRoot`，2026-11-01 实测）。
    先在工作区内找（若未来改为按 workdir 存放），再回落机器级数据根。
    """
    cands = []
    for root, _dirs, files in os.walk(WORK_DIR):
        if os.path.basename(root) == "plugins" and "enabled.json" in files:
            cands.append(os.path.join(root, "enabled.json"))
    la = os.environ.get("LOCALAPPDATA", "")
    if la:
        cands.append(os.path.join(la, "chonkpilot", "plugins", "enabled.json"))
    for path in cands:
        try:
            with open(path, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception:
            continue
    return {}


def _wait_enabled_flag(pid, want, timeout=15):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = _enabled_table().get(pid)
        if last is want:
            return last
        time.sleep(0.5)
    return last


def main():
    os.makedirs(WORK_DIR, exist_ok=True)
    h = _h.start_gui(port=PORT, work_dir=WORK_DIR,
                     home=_h.tmp_home(), ready_timeout=120)
    gui = h.client
    c = Checker()
    snap = None
    initial_enabled = {}
    try:
        _h.wait_probe(gui, ["!!document.querySelector('.panel-inner')"], timeout=60.0)
        _h.wait_idle(gui, max_wait=60.0)
        snap = _h.snapshot_config(gui)
        _h.ensure_locale(gui, "zh-CN")
        _h.wait_idle(gui, max_wait=60.0)
        # 插件平台就绪（既有方法面 plugin-list 应答；零新增主题）
        deadline = time.time() + 120
        ps = []
        while time.time() < deadline:
            try:
                ps = plugins(gui)
            except Exception:
                ps = []
            if ps:
                break
            time.sleep(0.4)
        if not ps:
            print("[env] warn: 插件平台未在 120s 内就绪", flush=True)
        by_id = {p["id"]: p for p in ps}
        initial_enabled = {p["id"]: p.get("enabled") is not False for p in ps}
        print("[env] plugins=%r" % sorted(by_id), flush=True)

        # ── 基线：全部启用（保证后续禁用/启用对照可比）──
        for p in ps:
            if p.get("enabled") is False:
                gui.req("plugin-set-enabled", {"id": p["id"], "enabled": True}, timeout=60000)
        running = set()
        deadline = time.time() + 120
        while time.time() < deadline:
            running = {p["id"] for p in plugins(gui) if p.get("status") == "running"}
            if len(running) >= len(ps):
                break
            time.sleep(1.0)
        c.check("A0 基线：全部插件进入 running", running >= set(initial_enabled),
                "running=%r" % sorted(running))

        # ── A. 插件一览多主题 UI 审计 ─────────────────────
        c.check("A1 打开插件维护视图", open_manager(gui, want_cards=len(ps)), "")
        for theme in THEMES:
            if not set_theme(gui, theme):
                c.check("A2-%s 主题切换生效" % theme, False, "data-theme 未更新")
                continue
            # 状态栏插件 badge（iframe）需在切主题后重新收到 theme 推送
            poll(gui, "JSON.stringify([...document.querySelectorAll('.statusbar iframe')].map(f=>{"
                      "let th='';try{const d=f.contentDocument;if(d&&d.body){"
                      "th=d.documentElement.getAttribute('data-theme')||''}}catch(e){}"
                      "return th}))",
                 lambda v: isinstance(v, list) and v and all(x == theme for x in v), timeout=20.0)
            rep = J(gui, MANAGER_JS)
            shot(gui, "manager-%s.png" % theme)
            if not isinstance(rep, dict):
                c.check("A2-%s 插件一览采样" % theme, False, repr(rep))
                continue
            cards = rep.get("cards") or []
            exp = rep.get("expect") or {}
            c.check("A2-%s 插件一览：主题生效 + 卡片数 = 插件数" % theme,
                    rep.get("theme") == theme and len(cards) == len(ps),
                    "theme=%r cards=%d plugins=%d" % (rep.get("theme"), len(cards), len(ps)))

            bad_bg = []
            for cd in cards:
                if cd["bg"] != exp.get("panel") and cd["bg"] != exp.get("bg"):
                    bad_bg.append((cd["meta"], cd["bg"], exp.get("panel")))
            c.check("A3-%s 卡片背景 = --panel-bg（主题 token）" % theme, not bad_bg,
                    repr(bad_bg[:3]))

            bad_contrast = []
            for cd in cards:
                bg = parse_rgb(cd["bg"])
                if not bg or bg[3] < 0.999:
                    bg = parse_rgb(exp.get("bg")) or bg
                for kind, color in (("name", cd["color"]), ("desc", cd["descColor"]),
                                    ("cmd", cd["cmdColor"])):
                    fg = flat(parse_rgb(color), bg)
                    ratio = contrast(fg, bg)
                    if ratio < 4.5:
                        bad_contrast.append((cd["meta"], kind, color, cd["bg"], round(ratio, 2)))
            c.check("A4-%s 卡片文字对比度 ≥ 4.5（正文/说明/命令）" % theme, not bad_contrast,
                    repr(bad_contrast[:3]))

            bad_layout = [cd["meta"] for cd in cards
                          if cd["nameFs"] != "14px" or (cd["btnFs"] and cd["btnFs"] != "12px")
                          or cd["overflow"] or cd["rect"]["w"] < 200]
            grid = rep.get("grid") or {}
            c.check("A5-%s 卡片布局：字号(14/12)+无横向溢出+宽度合理" % theme,
                    not bad_layout and grid.get("sw", 0) <= grid.get("cw", 0) + 1,
                    "bad=%r grid=%r" % (bad_layout[:3], grid))

            bad_sw = []
            for cd in cards:
                want = parse_rgb(exp.get("accent")) if cd["swChecked"] else parse_rgb(exp.get("border"))
                got = parse_rgb(cd["swBg"])
                if not got or not want or got[:3] != want[:3]:
                    bad_sw.append((cd["meta"], cd["swChecked"], cd["swBg"]))
            c.check("A6-%s 启用开关轨道色 = 主题 accent/border" % theme, not bad_sw,
                    repr(bad_sw[:3]))

            sbs = rep.get("statusbar") or []
            bar_bg = parse_rgb(exp.get("bar"))
            bad_sb = []
            for s in sbs:
                bg = parse_rgb(s["bg"])
                transparent = (not bg) or bg[3] == 0
                # badge 页设计为**透明底**（叠在状态栏 --bg-secondary 上）→ 二者都接受
                if s["theme"] != theme or (not transparent and s["bg"] != exp.get("bar")):
                    bad_sb.append((s["src"], s["bg"], s["theme"], "bg/theme"))
                    continue
                fg = flat(parse_rgb(s["color"]), bar_bg)
                if contrast(fg, bar_bg) < 4.5:
                    bad_sb.append((s["src"], s["color"], round(contrast(fg, bar_bg), 2)))
            c.check("A8-%s 状态栏插件 badge 主题同步 + 文字对比度（%d 个）" % (theme, len(sbs)),
                    bool(sbs) and not bad_sb, "bad=%r" % (bad_sb[:3],))

        # ── B. 各插件配置页 / 详情页（主题即时切换）────────
        cfg_pids = [p["id"] for p in ps if p.get("config_view")]
        c.check("B0 有配置页的插件清单（>=9）", len(cfg_pids) >= 9, "pids=%r" % cfg_pids)

        # 从 light 开始；每页打开后依次切三个主题回读（同时验证 iframe 主题下发）
        set_theme(gui, "light")
        open_manager(gui)
        for pid in cfg_pids:
            click = J(gui, "(()=>{const c=%s;if(!c)return 'no-card';"
                           "const b=[...c.querySelectorAll('.pm-btn')].find(x=>(x.textContent||'').indexOf('配置')>=0);"
                           "if(!b)return 'no-btn';b.click();return 'ok'})()" % card_expr(pid))
            rep0 = poll(gui, iframe_js(pid, CFG_ENTRY), page_loaded, timeout=30.0)
            shot(gui, "cfg-%s-light.png" % pid)
            if not isinstance(rep0, dict) or not rep0.get("found"):
                c.check("B1-%s 配置页打开（%s）" % (pid, click), False, repr(rep0))
                continue
            ok_open = (rep0.get("controls", 0) >= 1 and len(rep0.get("text") or "") >= 10)
            c.check("B1-%s 配置页加载：iframe 就绪 + 控件/文案非空" % pid, ok_open,
                    "click=%s controls=%r text=%r" % (click, rep0.get("controls"),
                                                      (rep0.get("text") or "")[:60]))

            for theme in THEMES:
                if not set_theme(gui, theme):
                    c.check("B2-%s-%s 主题切换" % (pid, theme), False, "data-theme 未更新")
                    continue
                rep = poll(gui, iframe_js(pid, CFG_ENTRY),
                           lambda v: isinstance(v, dict) and v.get("themeAttr") == theme
                           and v.get("bodyBg") == v.get("hostBg"), timeout=15.0)
                if not isinstance(rep, dict):
                    c.check("B2-%s-%s 页面主题同步" % (pid, theme), False, repr(rep))
                    continue
                c.check("B2-%s-%s 配置页背景随主题（body bg = %s 主题底色）" % (pid, theme, theme),
                        rep.get("themeAttr") == theme and rep.get("ckTheme") == theme
                        and rep.get("bodyBg") == rep.get("hostBg"),
                        "attr=%r ck=%r bg=%r host=%r" % (rep.get("themeAttr"), rep.get("ckTheme"),
                                                         rep.get("bodyBg"), rep.get("hostBg")))
                c.check("B3-%s-%s 配置页无横向溢出 + 尺寸合理" % (pid, theme),
                        (rep.get("overflowX") or 0) <= 2 and (rep.get("rect") or {}).get("w", 0) > 200,
                        "overflowX=%r rect=%r" % (rep.get("overflowX"), rep.get("rect")))
                if theme != "light" and pid == "web":
                    shot(gui, "cfg-%s-%s.png" % (pid, theme))

        # 详情页（status / tasks）；同样三主题回读
        set_theme(gui, "light")
        for pid, page, entry in DETAIL_VIEWS:
            gui.eval("window.mq.emit('preview-tab-open', {kind:'plugin-page', plugin:%s, page:%s, title:%s}); 'ok'"
                     % (json.dumps(pid), json.dumps(page), json.dumps(pid + "-" + page)))
            rep = poll(gui, iframe_js(pid, entry), page_loaded, timeout=30.0)
            shot(gui, "detail-%s-%s.png" % (pid, page))
            c.check("B4-%s/%s 详情页加载" % (pid, page),
                    isinstance(rep, dict) and rep.get("found") and len(rep.get("text") or "") >= 5,
                    repr(rep)[:160])
            if not isinstance(rep, dict) or not rep.get("found"):
                continue
            bad = []
            for theme in THEMES:
                if not set_theme(gui, theme):
                    bad.append((theme, "theme-not-applied"))
                    continue
                r2 = poll(gui, iframe_js(pid, entry),
                          lambda v: isinstance(v, dict) and v.get("themeAttr") == theme, timeout=15.0)
                if not isinstance(r2, dict) or r2.get("themeAttr") != theme \
                        or r2.get("bodyBg") != r2.get("hostBg"):
                    bad.append((theme, (r2 or {}).get("themeAttr"), (r2 or {}).get("bodyBg")))
            c.check("B5-%s/%s 详情页背景随三主题正确" % (pid, page), not bad, repr(bad[:3]))
        set_theme(gui, "light")

        # 关掉 B 段挂载的插件页（卸载 iframe，避免插件被禁用后其轮询继续报错、污染控制台断言）
        gui.eval("window.mq.emit('preview-tab-close-all', {}); 'ok'")
        _h.wait_probe(gui, ["!document.querySelector('.pph-frame')"], timeout=20.0)

        # ── C. 启用 / 禁用（逐个插件，UI 开关）────────────
        open_manager(gui, want_cards=len(ps))
        for p in ps:
            pid = p["id"]
            want_tools = [t for t in (p.get("tools") or []) if isinstance(t, str)]
            has_cfg = bool(p.get("config_view"))

            # 关闭
            click = _click_switch(gui, pid)
            card_off = poll(gui, "JSON.stringify((()=>{const c=%s;return c?{off:c.classList.contains('is-off'),"
                                 "dis:(c.querySelector('.b-switch')||{}).className,"
                                 "label:(c.querySelector('.pm-switch')||{}).textContent}:null})())" % card_expr(pid),
                            lambda v: isinstance(v, dict) and v.get("off") is True, timeout=60.0)
            p_off = _wait_plugin(gui, pid, False, {"disabled", "stopped"}, timeout=90)
            c.check("C1-%s 关闭开关 → 卡片 is-off + 插件 disabled/stopped" % pid,
                    bool(card_off) and p_off is not None,
                    "click=%s card=%r plugin=%r" % (click, card_off,
                                                    p_off and (p_off.get("status"), p_off.get("enabled"))))
            shot(gui, "toggle-%s-off.png" % pid)
            c.check("C1b-%s 禁用落库（enabled.json=false，重启后仍禁用）" % pid,
                    _wait_enabled_flag(pid, False) is False, repr(_enabled_table().get(pid)))

            if want_tools:
                left = _wait_tools_absent(gui, want_tools, timeout=90)
                c.check("C2-%s 禁用后工具面移除该插件 %d 个工具" % (pid, len(want_tools)), not left,
                        "残留=%r" % (sorted(left),))
            else:
                c.check("C2-%s 禁用后状态正确（该插件无 MCP 工具）" % pid, p_off is not None, "")

            if has_cfg:
                st = _wait_head(gui, pid, 404, timeout=30)
                c.check("C3-%s 禁用后配置资源不可用（HEAD 404）" % pid, st == 404, "status=%r" % st)
                rpc_ok = _wait_rpc_fail(gui, pid, timeout=30)
                c.check("C4-%s 禁用后 RPC 失败（功能面关闭）" % pid, rpc_ok is False, "rpc_ok=%r" % rpc_ok)

            # 启用
            click_on = _click_switch(gui, pid)
            card_on = poll(gui, "JSON.stringify((()=>{const c=%s;return c?{off:c.classList.contains('is-off')}:null})())"
                           % card_expr(pid), lambda v: isinstance(v, dict) and v.get("off") is False, timeout=60.0)
            p_on = _wait_plugin(gui, pid, True, {"running"}, timeout=120)
            c.check("C5-%s 重新启用 → 卡片恢复 + 插件 running" % pid,
                    bool(card_on) and p_on is not None,
                    "click=%s plugin=%r" % (click_on, p_on and p_on.get("status")))
            c.check("C5b-%s 启用落库（enabled.json=true）" % pid,
                    _wait_enabled_flag(pid, True) is True, repr(_enabled_table().get(pid)))
            if want_tools:
                miss = _wait_tools_present(gui, want_tools, timeout=120)
                c.check("C6-%s 启用后工具面恢复 %d 个工具" % (pid, len(want_tools)), not miss,
                        "缺失=%r" % (sorted(miss),))
            if has_cfg:
                st2 = _wait_head(gui, pid, 200, timeout=60)
                c.check("C7-%s 启用后配置资源恢复（HEAD 200）" % pid, st2 == 200, "status=%r" % st2)

        # 禁用态配置页占位（真实 UI，且必须是**全新挂载**）：web 插件
        # 先关掉全部预览页签，避免复用 B 段已 ready 的 iframe（其 stage=ready 会保留旧页面）。
        if "web" in by_id:
            open_manager(gui, want_cards=len(ps))
            if _click_switch(gui, "web") == "ok":
                _wait_plugin(gui, "web", False, {"disabled", "stopped"}, timeout=90)
            gui.eval("window.mq.emit('preview-tab-close-all', {}); 'ok'")
            _h.wait_probe(gui, ["!document.querySelector('.pph-frame')"], timeout=20.0)
            open_manager(gui, want_cards=len(ps))
            J(gui, "(()=>{const c=%s;const b=[...c.querySelectorAll('.pm-btn')]"
                   ".find(x=>(x.textContent||'').indexOf('配置')>=0);b&&b.click();return 'ok'})()" % card_expr("web"))
            ph = poll(gui, "JSON.stringify({ph:document.querySelectorAll('.pph-placeholder').length,"
                           "txt:(document.querySelector('.pph-placeholder')||{}).innerText||''})",
                      lambda v: isinstance(v, dict) and v.get("ph", 0) >= 1, timeout=30.0)
            c.check("C8 禁用后打开配置页 → 「插件未就绪」占位（含状态说明）",
                    isinstance(ph, dict) and ph.get("ph", 0) >= 1 and "未就绪" in (ph.get("txt") or ""),
                    repr(ph)[:200])
            shot(gui, "disabled-config-placeholder.png")
            gui.req("plugin-set-enabled", {"id": "web", "enabled": True}, timeout=60000)
            _wait_plugin(gui, "web", True, {"running"}, timeout=120)
            gui.eval("window.mq.emit('preview-tab-close-all', {}); 'ok'")

        errs = [e.get("text") for e in (gui.console() or {}).get("entries", [])
                if e.get("level") == "error"]
        c.check("C9 无前端错误（全程）", len(errs) == 0, repr(errs[:3]))
    finally:
        # 还原启用表（enabled.json，非 config 面）+ 主题 → 再还原配置快照
        try:
            for pid, en in (initial_enabled or {}).items():
                try:
                    gui.req("plugin-set-enabled", {"id": pid, "enabled": bool(en)}, timeout=60000)
                except Exception:
                    pass
            set_theme(gui, "light")
        except Exception:
            pass
        if snap is not None:
            try:
                _h.restore_config(gui, snap)
            except Exception as e:
                print("[restore] %s" % e, flush=True)
        h.stop()
    print("\n截图目录: %s" % SHOT_DIR, flush=True)
    sys.exit(0 if c.summary() else 1)


# ── 插件状态/工具面/配置资源探针 ───────────────────────────
def _wait_plugin(gui, pid, enabled, statuses, timeout=90):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        p = plugin_of(gui, pid)
        last = p
        if p and (p.get("enabled") is not False) == enabled and p.get("status") in statuses:
            return p
        time.sleep(1.0)
    return None


def _wait_tools_absent(gui, names, timeout=90):
    deadline = time.time() + timeout
    left = set(names)
    while time.time() < deadline:
        cur = tool_names(gui)
        left = set(names) & cur
        if not left:
            return set()
        time.sleep(1.0)
    return left


def _wait_tools_present(gui, names, timeout=120):
    deadline = time.time() + timeout
    miss = set(names)
    while time.time() < deadline:
        cur = tool_names(gui)
        miss = set(names) - cur
        if not miss:
            return set()
        time.sleep(1.0)
    return miss


def _head_status(gui, pid, entry="webui/config.html"):
    gui.eval("window.__ckprobe = null; fetch('/plugins/%s/%s', {method:'HEAD', cache:'no-store'})"
             ".then(r => window.__ckprobe = r.status).catch(() => window.__ckprobe = -1); 'ok'"
             % (pid, entry))
    v = poll(gui, "String(window.__ckprobe)",
             lambda x: isinstance(x, str) and x not in ("", "None", "null", "undefined"), timeout=15.0)
    try:
        return int(v)
    except (TypeError, ValueError):
        return None


def _wait_head(gui, pid, want, timeout=60):
    deadline = time.time() + timeout
    st = None
    while time.time() < deadline:
        st = _head_status(gui, pid)
        if st == want:
            return st
        time.sleep(1.0)
    return st


def _rpc_ping(gui, pid):
    try:
        r = gui.req("plugin-call", {"id": pid, "method": "rpc",
                                    "params": {"method": "ping", "params": {}}}, timeout=20000)
        return not (isinstance(r, dict) and r.get("ok") is False)
    except Exception:
        return False


def _wait_rpc_fail(gui, pid, timeout=30):
    """禁用后 RPC 必须失败（插件进程已停 → `pin` 通道不可达）。给停止留收敛窗口。"""
    deadline = time.time() + timeout
    last = True
    while time.time() < deadline:
        last = _rpc_ping(gui, pid)
        if last is False:
            return False
        time.sleep(1.0)
    return last


def _click_switch(gui, pid, timeout=30):
    """点卡片启用开关：开关在请求在途时 `is-disabled`（`pending`），需重试到真正点下去。"""
    deadline = time.time() + timeout
    last = ''
    while time.time() < deadline:
        last = J(gui, "(()=>{const c=%s;if(!c)return 'no-card';"
                      "const s=c.querySelector('.pm-switch .b-switch');"
                      "if(!s)return 'no-switch';if(s.classList.contains('is-disabled'))return 'busy';"
                      "s.click();return 'ok'})()" % card_expr(pid))
        if last == "ok":
            return last
        time.sleep(0.5)
    return last


if __name__ == "__main__":
    main()
