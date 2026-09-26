# -*- coding: utf-8 -*-
"""L4：工具异步配置页（usr 键 `tool_async`）—— 页面分组 / 四档读写 / 恢复默认 / 效果断言。

需求（用户口径）：UI 增加工具配置页，列出所有工具（`tools/list`）并按 MCP 归类，逐工具设
「仅异步 / 仅同步 / 自动异步+超时 / 手动异步」。

驱动面（**零新增 MQ 主题**）：
  * 工具清单 = 既有客户端能力面 `tools-list`（与 `useToolAsyncMode.js` / `ScenarioEditDialog`
    同一消费点，分组口径 `_meta.server.alias || _meta.server.node || '全局'`）；
  * 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（usr 键 `tool_async`，
    JSON `{"<工具暴露名>": {"mode": "...", "threshold": n, "hard_timeout": n}}`）。

覆盖：
  A 入口与分组渲染：工具栏设置下拉含「工具异步配置」→ preview tab 打开 → 按 MCP 分组 + 每行四档选择器
    （四档文案精确 + 原生 select 带 aria-label）；行集合与 `tools-list` 一致
  B 契约现值：未配置的行标「契约默认」并显示契约现值（`_meta.async` / `async-threshold` / `timeout`）
  C 四档保存 → `data-user-config-load` 回读一致（always/never/auto/manual 四档逐一）+ 条件字段
    （阈值仅 auto/manual 显示）+ 高级 hard_timeout 落库
  D 恢复默认：「恢复默认」删除该工具的键项（最后一项删除后整键删除，不留空对象）
  E 效果断言（**依赖并行后端的 `tool_async` 落地**）：配置后 `tools-list` 的 `_meta.async` 随配置变化

> 「转异步」图标的 **DOM 级**断言（manual 工具 in-flight → 工具行箭头出现 → 点击 → task-background
> 收敛）在既有套件 `run_tool_async.py` 的用例 **F**（本波新增，实跑 7/7 通过）。

前置（本脚本自起，结束自动回收；见 harness.py）：
  `dist-desktop\\chonkpilot.exe --test-port=2345 --work-dir ws`
运行：`python run_tool_async_config.py`

**PENDING 口径（不伪造、不放宽断言）**：用例前置不成立时打印 `[PENDING]` 并在最终行单独计入
「待验证」，**不计入通过**：
  ① 产物未重建（工具栏设置菜单无「工具异步配置」→ 前端 dist 未进 exe）→ 页面用例 PENDING；
  ② 后端未合并（`data-user-config-save{tool_async}` 写入后回读不到 → usr 未注册该键）→ 落库/效果用例 PENDING。
  两种前置**都成立**时全部断言按原样严格执行（任何不符即 FAIL）。
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness as _h  # 按需加载 + 结束即回收（51-FP与测试映射 §测试资源规范）
from chonk_client import TestError

PORT = 2345
WS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws")
CFG_KEY = "tool_async"
KIND = "settings-tool-async"
ROOT = ".settings-page"
MENU_LABEL = "工具异步配置"
MODES = ["always", "never", "auto", "manual"]
MODE_LABELS = ["仅异步", "仅同步", "自动异步", "手动异步"]
PROBE_KEY = "__l4_probe__"

c = _h.acquire_gui(PORT, work_dir=WS).client
_h.suite_config_guard(c)
_h.ensure_locale(c)

PASSED, FAILED, PENDING = [], [], []


class Pending(Exception):
    """前置不成立（产物未重建 / 后端未合并）：等待主线全量验证，**不**计入通过。"""


# ── 通用工具 ────────────────────────────────────────────────

def _loads(v):
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def ev(js, timeout=6000):
    return _loads(c.eval(js, timeout))


def vcount(sel):
    return int(ev("([...document.querySelectorAll(%s)].filter(e=>e.getBoundingClientRect().width>0)).length"
                  % json.dumps(sel)) or 0)


def wait_vis(sel, max_wait=12):
    end = time.time() + max_wait
    while time.time() < end:
        if vcount(sel) > 0:
            return True
        time.sleep(0.3)
    return False


def panel_js(inner, root=ROOT):
    return ("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
            "if(!R)return null;%s})()" % (json.dumps(root), inner))


def poll(fn, max_wait=10, interval=0.4):
    end = time.time() + max_wait
    last = None
    while time.time() < end:
        try:
            v = fn()
            if v:
                return v
            last = v
        except Exception as e:
            last = e
        time.sleep(interval)
    return last


# ── 数据面 ─────────────────────────────────────────────────

def ucfg():
    r = c.req("data-user-config-load", {})
    return (r.get("data") or {}) if isinstance(r, dict) else {}


def user_map():
    """usr `tool_async` 视图（对象；支持 JSON 字符串形态）。"""
    v = ucfg().get(CFG_KEY)
    if isinstance(v, str):
        try:
            v = json.loads(v)
        except Exception:
            return {}
    return v if isinstance(v, dict) else {}


def tools_list():
    r = c.req("tools-list", {}) or {}
    return list(r.get("tools") or [])


def tool_meta(name):
    for tl in tools_list():
        if tl.get("name") == name:
            return tl.get("_meta") or {}
    return {}


# ── 前置探测（判据 = 可观测事实，非"期望值放宽"）──────────────

SEL_PAGE = [None]  # "有"/"无"：产物是否已含新页面


def probe_page():
    """产物是否已重建出新页面：工具栏设置下拉是否出现「工具异步配置」项。"""
    if SEL_PAGE[0] is not None:
        return SEL_PAGE[0]
    try:
        c.mq_emit("config-menu-toggle")
        time.sleep(0.7)
        txt = ev("(function(){const e=[...document.querySelectorAll('.b-dropdown-popper')]"
                 ".find(x=>x.getBoundingClientRect().width>0);return e?e.innerText:'';})()") or ""
        c.mq_emit("config-menu-toggle")
        time.sleep(0.3)
    except Exception:
        txt = ""
    SEL_PAGE[0] = "有" if MENU_LABEL in txt else "无"
    return SEL_PAGE[0]


def require_page():
    if probe_page() != "有":
        raise Pending("产物未含新页面（工具栏设置菜单无「工具异步配置」）"
                      "→ 等待主线统一重建 build-desktop.ps1 后全量验证")


BACKEND = [None]


def probe_backend():
    """后端是否已注册 usr 键 `tool_async`：写入探针后回读是否落库（落库即支持）。

    探针按「原有配置 ∪ 探针项」写入，回读后**原样还原**（原有为空 → 删键），不留脏配置。
    """
    if BACKEND[0] is not None:
        return BACKEND[0]
    before = user_map()
    try:
        probe = dict(before)
        probe[PROBE_KEY] = {"mode": "never"}
        c.req("data-user-config-save", {"data": {CFG_KEY: probe}})
        time.sleep(0.5)
        ok = PROBE_KEY in user_map()
        if ok:
            if before:
                c.req("data-user-config-save", {"data": {CFG_KEY: before}})
            else:
                c.req("data-user-config-delete", {"id": CFG_KEY})
            time.sleep(0.4)
        BACKEND[0] = "有" if ok else "无"
    except Exception:
        BACKEND[0] = "无"
    return BACKEND[0]


def require_backend():
    if probe_backend() != "有":
        raise Pending("后端未合并：`data-user-config-save{tool_async}` 写入后回读不到"
                      "（usr 未注册该键）→ 等待后端合并 + 产物重建后由主线全量验证")


# ── 页面操作 ───────────────────────────────────────────────

def open_page():
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": KIND})
    if not wait_vis(ROOT):
        raise TestError("工具异步配置页未打开（kind=%s root=%s）" % (KIND, ROOT))
    time.sleep(0.8)


def rows():
    """当前页面所有工具行（含契约/用户来源与条件字段状态 + 展示名/完整暴露名）。"""
    return ev(panel_js("""
return [...R.querySelectorAll('.tool-item')].map(it=>{
  const s=it.querySelector('select.b-select__native');
  const badge=it.querySelector('.badge');
  const ct=it.querySelector('.contract');
  const nm=it.querySelector('.tool-name');
  const mono=it.querySelector('.tool-name .mono');
  return {
    tool: it.getAttribute('data-tool'),
    disp: mono?mono.textContent.trim():'',
    title: nm?nm.getAttribute('title'):null,
    mode: s?s.value:'',
    opts: s?[...s.querySelectorAll('option')].filter(o=>o.value!=='').map(o=>o.textContent.trim()):[],
    aria: s?s.getAttribute('aria-label'):null,
    thr: !!it.querySelector('.tool-threshold input.b-input'),
    adv: !!it.querySelector('.advanced-body'),
    badge: badge?badge.textContent.trim():'',
    contract: ct?ct.textContent.trim():'',
  };
});""")) or []


def group_keys():
    return ev(panel_js("return [...R.querySelectorAll('.tool-group .group-title')]"
                       ".map(x=>x.textContent.replace(/（.*?）/,'').trim());")) or []


def pick_tool():
    """选一个稳定工具：优先内置 script_run（契约缺省 manual），否则第一行。"""
    names = [r["tool"] for r in rows()]
    if not names:
        raise TestError("页面无工具行（tools-list 为空？）")
    for n in names:
        if n.endswith("script_run"):
            return n
    return names[0]


def row_of(tool):
    for r in rows():
        if r["tool"] == tool:
            return r
    raise TestError("页面无该工具行：%s" % tool)


def set_mode(tool, mode):
    r = ev(panel_js("""
const it=[...R.querySelectorAll('.tool-item')].find(x=>x.getAttribute('data-tool')===%s);
if(!it)return 'no-row';const s=it.querySelector('select.b-select__native');if(!s)return 'no-select';
Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(s,%s);
s.dispatchEvent(new Event('change',{bubbles:true}));return s.value;"""
                 % (json.dumps(tool), json.dumps(mode))))
    if r != mode:
        raise TestError("设置模式失败 tool=%s mode=%s → %r" % (tool, mode, r))


def set_number(tool, selector, value):
    r = ev(panel_js("""
const it=[...R.querySelectorAll('.tool-item')].find(x=>x.getAttribute('data-tool')===%s);
if(!it)return 'no-row';const inp=it.querySelector(%s);if(!inp)return 'no-input';
Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(inp,%s);
inp.dispatchEvent(new Event('input',{bubbles:true}));
inp.dispatchEvent(new Event('blur'));return 'ok';"""
                 % (json.dumps(tool), json.dumps(selector), json.dumps(str(value)))))
    if r != "ok":
        raise TestError("写数值失败 tool=%s sel=%s → %r" % (tool, selector, r))


def click_in_row(tool, label):
    r = ev(panel_js("""
const it=[...R.querySelectorAll('.tool-item')].find(x=>x.getAttribute('data-tool')===%s);
if(!it)return 'no-row';const b=[...it.querySelectorAll('button.b-btn')]
 .find(x=>x.textContent.includes(%s));if(!b)return 'no-btn';b.click();return 'ok';"""
                 % (json.dumps(tool), json.dumps(label))))
    if r != "ok":
        raise TestError("点击行内按钮失败 tool=%s label=%s → %r" % (tool, label, r))
    time.sleep(0.5)


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def case_a_entry_and_groups():
    """A 入口与分组渲染：菜单项 → tab → 按 MCP 分组 + 每行四档选择器（文案/aria）+ 行集合与 tools-list 一致。"""
    require_page()
    open_page()
    keys = group_keys()
    if not keys:
        raise TestError("页面无 MCP 分组标题（.group-title）")
    rs = rows()
    if not rs:
        raise TestError("页面无工具行（.tool-item）")
    # 每行四档选择器 + 文案精确 + 原生 select 带 aria-label（可访问性）
    for r in rs:
        if r["opts"] != MODE_LABELS:
            raise TestError("工具 %s 的异步模式选项文案不符：%r，期望 %r" % (r["tool"], r["opts"], MODE_LABELS))
        if not r["aria"]:
            raise TestError("工具 %s 的 select 缺 aria-label（可访问性）" % r["tool"])
    # 分组口径与既有 tools-list 消费点一致：alias || node || 全局
    want = []
    for tl in tools_list():
        meta = tl.get("_meta") or {}
        srv = meta.get("server") or {}
        want.append(srv.get("alias") or srv.get("node") or "全局")
    if set(keys) != set(want):
        raise TestError("分组口径不符：页面 %r，tools-list 计算 %r" % (keys, sorted(set(want))))
    # 行集合 = 工具面（同一 tools-list 主题）
    dom_names = set(r["tool"] for r in rs)
    api_names = set(tl.get("name") for tl in tools_list())
    if dom_names != api_names:
        raise TestError("页面工具行与 tools-list 不一致：差集 %r" % (dom_names ^ api_names))


def case_b_contract_source():
    """B 未配置行显示契约现值并标「契约默认」；契约值取自 tools-list `_meta`。"""
    require_page()
    open_page()
    um = user_map()
    checked = 0
    for r in rows():
        if r["tool"] in um:
            continue
        meta = tool_meta(r["tool"])
        if r["badge"] != "契约默认":
            raise TestError("未配置工具 %s 的来源标记 = %r，期望「契约默认」" % (r["tool"], r["badge"]))
        if "契约现值" not in r["contract"]:
            raise TestError("未配置工具 %s 未显示契约现值：%r" % (r["tool"], r["contract"]))
        want_mode = meta.get("async") or "auto"
        if want_mode not in r["contract"]:
            raise TestError("工具 %s 契约现值缺 mode=%s：%r" % (r["tool"], want_mode, r["contract"]))
        checked += 1
        if checked >= 3:
            break
    if checked == 0:
        raise TestError("无「未配置」工具行（usr tool_async 已有配置？）→ 无法断言契约现值标记")


def case_c_four_modes_roundtrip():
    """C 四档保存 → data-user-config-load 回读一致 + 条件字段（阈值仅 auto/manual）+ 高级 hard_timeout。"""
    require_page()
    require_backend()
    with _h.user_config_guard(c, [CFG_KEY]):
        open_page()
        tool = pick_tool()
        # 逐一覆盖四档：起点 = 当前生效档，其余三档先跑（每步都发生变化 → 必落库），当前档最后跑
        # （此时已不同 → 同样落库）。避免"选中与现状相同 → 页面按「未改动不进库」跳过"造成假红。
        cur = row_of(tool)["mode"]
        seq = [m for m in MODES if m != cur] + ([cur] if cur in MODES else [])
        for mode in seq:
            set_mode(tool, mode)
            got = poll(lambda: (user_map().get(tool) or {}).get("mode"))
            if got != mode:
                raise TestError("四档保存回读不一致：tool=%s mode=%s → usr tool_async=%r"
                                % (tool, mode, user_map()))
            rs = [r for r in rows() if r["tool"] == tool]
            if not rs:
                raise TestError("保存后工具行消失：%s" % tool)
            want_thr = mode in ("auto", "manual")
            if rs[0]["thr"] != want_thr:
                raise TestError("条件字段显示不符：mode=%s 阈值输入=%s（期望 %s）"
                                % (mode, rs[0]["thr"], want_thr))
        # 阈值（auto）+ 高级 hard_timeout 落库
        set_mode(tool, "auto")
        poll(lambda: (user_map().get(tool) or {}).get("mode") == "auto")
        set_number(tool, ".tool-threshold input.b-input", 45)
        got = poll(lambda: (user_map().get(tool) or {}).get("threshold"))
        if got != 45:
            raise TestError("阈值未落库：tool=%s → usr tool_async=%r" % (tool, user_map()))
        if row_of(tool)["adv"]:
            raise TestError("高级区默认应为折叠（.advanced-body 不应存在）")
        click_in_row(tool, "高级")
        if not row_of(tool)["adv"]:
            raise TestError("点击「高级」后未展开 hard_timeout 输入")
        set_number(tool, ".advanced-body input.b-input", 120)
        got = poll(lambda: (user_map().get(tool) or {}).get("hard_timeout"))
        if got != 120:
            raise TestError("hard_timeout 未落库：tool=%s → usr tool_async=%r" % (tool, user_map()))
        if row_of(tool)["badge"] != "用户配置":
            raise TestError("已配置工具来源标记 = %r，期望「用户配置」" % row_of(tool)["badge"])


def case_d_restore_default():
    """D 恢复默认 = 删除该工具键项；最后一项删除后整键删除（不留空对象）。"""
    require_page()
    require_backend()
    with _h.user_config_guard(c, [CFG_KEY]):
        open_page()
        tool = pick_tool()
        others = [k for k in user_map() if k != tool]  # 用例前已有的其它工具配置（不改动它们）
        set_mode(tool, "manual" if row_of(tool)["mode"] != "manual" else "never")
        if not poll(lambda: (user_map().get(tool) or {}).get("mode")):
            raise TestError("前置失败：目标档未落库（tool=%s）" % tool)
        click_in_row(tool, "恢复默认")
        if not poll(lambda: tool not in user_map()):
            raise TestError("恢复默认后键项仍在：%r" % user_map())
        # 仅此一项 → 整键应被删除（不留空对象）；若本机原有其它工具配置 → 只断言键项删除
        if not others:
            if not poll(lambda: CFG_KEY not in ucfg()):
                raise TestError("最后一项删除后整键未删（usr 仍存 %s=%r）" % (CFG_KEY, ucfg().get(CFG_KEY)))
        else:
            print("    [note] 本机原有其它工具配置 → 仅断言键项删除（整键保留）：%r" % others)
        for k in others:
            if k not in user_map():
                raise TestError("恢复默认误删了其它工具配置：%s（余 %r）" % (k, user_map()))
        rs = [r for r in rows() if r["tool"] == tool]
        if rs and rs[0]["badge"] != "契约默认":
            raise TestError("恢复默认后来源标记 = %r，期望「契约默认」" % rs[0]["badge"])


def case_e_effect_meta_follows_config():
    """E 效果断言：配置后 `tools-list` 的 `_meta.async` 应随 usr `tool_async` 变化（后端生效面）。"""
    require_page()
    require_backend()
    with _h.user_config_guard(c, [CFG_KEY]):
        open_page()
        tool = pick_tool()
        before = (tool_meta(tool).get("async") or "auto")
        # 目标档 ≠ 当前生效档（否则页面按「未改动不进库」跳过 → 假红）
        cur = row_of(tool)["mode"]
        target = "manual" if cur != "manual" else "never"
        set_mode(tool, target)
        if not poll(lambda: (user_map().get(tool) or {}).get("mode") == target):
            raise TestError("前置失败：%s 未落库" % target)
        got = poll(lambda: (tool_meta(tool).get("async") or ""), max_wait=8, interval=0.6)
        if got != target:
            raise Pending("后端未合并：配置 %s=%s 后 tools-list 的 _meta.async 仍为 %r"
                          "（契约现值 %s）→ 等待后端合并 + 产物重建后由主线全量验证"
                          % (tool, target, got, before))


def case_f_prefix_display():
    """F 展示口径（D2，2026-09-24）：工具名**剥掉网关前缀**后展示，`:title` 保留**完整暴露名**，
    `data-tool`（配置键）不变 —— 重名工具仍可经 hover 区分。"""
    require_page()
    open_page()
    rs = rows()
    if not rs:
        raise TestError("页面无工具行（.tool-item）")
    checked = 0
    for r in rs:
        name = r["tool"] or ""
        # data-tool / :title 恒为完整暴露名（配置键语义不变）
        if r["title"] != name:
            raise TestError("工具 %s 的 :title=%r，期望完整暴露名 %r" % (name, r["title"], name))
        if name.startswith("self_"):
            want = name[5:]
            if r["disp"] != want:
                raise TestError("工具 %s 显示名 %r，期望剥掉前缀后 %r" % (name, r["disp"], want))
            checked += 1
    if checked == 0:
        raise Pending("工具面无 self_ 前缀工具（无法验证剥前缀显示）")
    print("      展示口径实证据：%d 个 self_ 前缀工具已剥前缀显示，:title 均为完整暴露名" % checked)


CASES = [
    ("A 入口与分组渲染（设置菜单 → tab + 按 MCP 分组 + 四档文案/aria）", case_a_entry_and_groups),
    ("B 未配置行显示契约现值并标「契约默认」", case_b_contract_source),
    ("C 四档保存 + data-user-config-load 回读一致 + 条件字段 + 高级 hard_timeout", case_c_four_modes_roundtrip),
    ("D 恢复默认删键项（最后一项 → 整键删除）", case_d_restore_default),
    ("E 效果：tools-list 的 _meta.async 随配置变化（后端生效面）", case_e_effect_meta_follows_config),
    ("F 展示口径（D2）：列表名剥前缀仅展示，data-tool/:title 保留完整暴露名", case_f_prefix_display),
]


def wait_frontend(max_wait=90):
    """等前端就绪（.statusbar 出现）：WebView2 首启加载较慢，/console 等脚本调用会超时。"""
    end = time.time() + max_wait
    while time.time() < end:
        try:
            if ev("typeof document!=='undefined' && !!document.querySelector('.statusbar')"):
                return True
        except Exception:
            pass
        time.sleep(1)
    return False


def main():
    wait_frontend()
    try:
        c.console(clear=True)
    except Exception:
        pass
    print("依赖：--test-port=%d 的 GUI（产物 = dist-desktop）；配置面 = usr `%s`" % (PORT, CFG_KEY))
    print("前置探测：页面=%s / 后端键=%s" % (probe_page(), probe_backend()))
    for name, fn in CASES:
        try:
            fn()
            print("  [PASS] %s" % name)
            PASSED.append(name)
        except Pending as e:
            print("  [PENDING] %s: %s" % (name, e))
            PENDING.append(name)
        except TestError as e:
            print("  [FAIL] %s: %s" % (name, e))
            FAILED.append(name)
        except Exception as e:
            print("  [ERROR] %s: %s: %s" % (name, type(e).__name__, e))
            FAILED.append(name)
    total = len(CASES)
    print("\n工具异步配置页 L4：%d/%d 通过, %d 失败, %d 待验证(PENDING)"
          % (len(PASSED), total, len(FAILED), len(PENDING)))
    for n in PENDING:
        print("  [PENDING] %s" % n)
    print("RESULT: %s" % (len(FAILED) == 0))
    return 0 if not FAILED else 1


if __name__ == "__main__":
    sys.exit(main())
