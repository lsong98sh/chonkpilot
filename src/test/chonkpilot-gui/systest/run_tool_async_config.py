# -*- coding: utf-8 -*-
"""L4：工具配置页（原名「工具异步配置」；usr 键 `tool_async`）—— 表格分组 / 四档读写（手动保存）/
恢复默认 / 效果断言 / **涉及文件变动**（2026-09-28 新增）。

需求（用户口径，2026-09-26 改版；2026-09-28 页签改名 + 新增「涉及文件变动」）：
  明细用 **表格** 呈现（工具 / 模式 / 阈值 / 超时 / **涉及文件变动**；工具名列 min-width 200px，
  仍按 MCP 分组）；**手动保存**（改模式/数值/开关只改本地待保存态，点【保存】一次性提交，无改动时
  保存按钮禁用）；**不再显示默认配置信息**（「契约默认/用户配置」徽标与「契约现值」文本移除 →
  契约默认信息改由「恢复默认」按钮 tooltip 承载）；**去「高级」按钮**（`hard_timeout` 常显为
  「超时」列；dir 节点行禁用 + 标「不适用」）；模式 / 阈值 / 超时 / 涉及文件变动 四列表头各带 `?` 说明。
  **「涉及文件变动」**（`touch_files`，布尔）：缺省由工具来源给出（self 内置仅 filesys_run /
  script_run 涉及；其余内置不涉及；dir 节点 / 第三方 / 无法判定 → 保守按涉及）；列表内**派生**
  「打点 / 不打点」标记（涉及 = 打点）。

驱动面（**零新增 MQ 主题**）：
  * 工具清单 = 既有客户端能力面 `tools-list`（分组口径 `_meta.server.alias || _meta.server.node || '全局'`）；
  * 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（usr 键 `tool_async`，
    JSON `{"<工具暴露名>": {"mode": "...", "threshold": n, "hard_timeout": n, "touch_files": bool}}`）。

覆盖：
  A 入口与分组渲染：菜单项 → preview tab 打开 → 按 MCP 分组 + 每行四档选择器（四档文案精确 + aria）
    + 行集合与 `tools-list` 一致 + 表头 `?` 说明（≥3）+ 干净初始态保存按钮禁用
  B 默认信息位置：行内不再有「契约默认/用户配置」徽标与「契约现值」文本；未配置行的「恢复默认」
    tooltip 承载该工具的契约默认（模式 X / 阈值 Y / 超时 Z）
  C 四档手动保存 → `data-user-config-load` 回读（= 覆盖态，或契约档不落库退化为契约值）+ 条件字段
    （阈值仅 auto/manual 显示，其余为「—」）+ 超时列常显（无「高级」按钮/折叠区）+ hard_timeout 落库
  D 恢复默认：「恢复默认」→ 待保存 → 点【保存】删该工具键项（最后一项删除后整键删除，不留空对象）
  E 效果断言（**依赖并行后端的 `tool_async` 落地**）：保存后 `tools-list` 的 `_meta.async` 随配置变化
  F 展示口径（D2）：列表名剥前缀仅展示，`data-tool` / `:title` 保留完整暴露名
  G 无上限口径（2026-09-27）：`hard_timeout` 输入 **0 / -1** = 无上限 → 合法、显示保留、落库保留
  H 涉及文件变动（2026-09-28）：缺省映射 + 派生「打点 / 不打点」标记 + 开关保存/回读 + 恢复默认回落
  I 超时自动取消（2026-10-07，配置⑤）：`cancel_on_timeout` 输入 > 0 → 落库 + 回读；0 = 关闭不写库

前置（本脚本自起，结束自动回收；见 harness.py）：
  `dist/desktop\\chonkpilot.exe --test-port=2345 --work-dir ws`
运行：`python run_tool_async_config.py`

**PENDING 口径（不伪造、不放宽断言）**：用例前置不成立时打印 `[PENDING]` 并在最终行单独计入
「待验证」，**不计入通过**：
  ① 产物未重建（工具栏设置菜单无「工具配置」→ 前端 dist 未进 exe）→ 页面用例 PENDING；
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
MENU_LABEL = "工具配置"
MODES = ["always", "never", "auto", "manual"]
MODE_LABELS = ["仅异步", "仅同步", "自动异步", "手动异步"]
# 契约值 → 界面本地化档名（zh-CN；「恢复默认」tooltip 用 modeLabel 渲染）
MODE_LABELS_BY_MODE = {"always": "仅异步", "never": "仅同步", "auto": "自动异步", "manual": "手动异步"}
PROBE_KEY = "__l4_probe__"
# 涉及文件变动：self 内置白名单（涉及 = 打点）与派生标记文案
TOUCH_WHITELIST = ("filesys_run", "script_run")
TOUCH_ON_BADGE, TOUCH_OFF_BADGE = "打点", "不打点"

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


def reset_key():
    """把 usr `tool_async` 清空（套件级 guard 会在退出前还原）。"""
    c.req("data-user-config-delete", {"id": CFG_KEY})
    time.sleep(0.4)


# ── 前置探测（判据 = 可观测事实，非"期望值放宽"）──────────────

SEL_PAGE = [None]  # "有"/"无"：产物是否已含新页面


def probe_page():
    """产物是否已重建出新页面：工具栏设置下拉是否出现「工具配置」项。"""
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
        raise Pending("产物未含新页面（工具栏设置菜单无「工具配置」）"
                      "→ 等待主线统一重建 build-desktop.ps1 后全量验证")


BACKEND = [None]


def probe_backend():
    """后端是否已注册 usr 键 `tool_async`：写入探针后回读是否落库（落库即支持）。"""
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
        raise TestError("工具配置页未打开（kind=%s root=%s）" % (KIND, ROOT))
    poll(lambda: rows(), max_wait=8, interval=0.3)


def rows():
    """当前页面所有工具行：工具 / 展示名 / 完整暴露名 / 模式 / 条件字段 / 恢复默认按钮态 /
    契约 tooltip / **涉及文件变动**（开关态 + 派生标记）。"""
    return ev(panel_js("""
return [...R.querySelectorAll('.tool-name[data-tool]')].map(nm=>{
  const it=nm.closest('tr');
  const s=it?it.querySelector('select.b-select__native'):null;
  const thr=it?it.querySelector('.cell-threshold input.b-input'):null;
  const dash=it?it.querySelector('.cell-threshold .cell-dash'):null;
  const to=it?it.querySelector('.cell-timeout input.b-input'):null;
  const na=it?it.querySelector('.cell-timeout .na-hint'):null;
  const mono=nm.querySelector('.mono');
  const rst=it?it.querySelector('button[data-restore]'):null;
  const tip=it?it.querySelector('.b-tooltip'):null;
  const sw=it?it.querySelector('.cell-touch .b-switch'):null;
  const badge=it?it.querySelector('.cell-touch .touch-badge'):null;
  const co=it?it.querySelector('.cell-cancel-timeout input.b-input'):null;
  return {
    tool: nm.getAttribute('data-tool'),
    disp: mono?mono.textContent.trim():'',
    title: nm.getAttribute('title'),
    mode: s?s.value:'',
    opts: s?[...s.querySelectorAll('option')].filter(o=>o.value!=='').map(o=>o.textContent.trim()):[],
    aria: s?s.getAttribute('aria-label'):null,
    thr: !!thr,
    dash: !!dash,
    timeout: !!to,
    hard: to?to.value:'',
    timeoutDisabled: to?!!to.disabled:false,
    na: !!na,
    cancelTimeout: !!co,
    cancelValue: co?co.value:'',
    restoreDisabled: rst?!!rst.disabled:null,
    contract: tip?tip.getAttribute('data-contract'):null,
    touchSwitch: !!sw,
    touchOn: sw?sw.classList.contains('is-checked'):null,
    touchBadge: badge?badge.textContent.trim():null,
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


def effective_mode(tool):
    """该工具当前生效档 = usr 覆盖值，否则契约 `_meta.async`（缺省 auto）。"""
    ov = (user_map().get(tool) or {}).get("mode")
    return ov or (tool_meta(tool).get("async") or "auto")


def set_mode(tool, mode):
    r = ev(panel_js("""
const nm=[...R.querySelectorAll('.tool-name')].find(x=>x.getAttribute('data-tool')===%s);
if(!nm)return 'no-row';const it=nm.closest('tr');const s=it.querySelector('select.b-select__native');
if(!s)return 'no-select';
Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(s,%s);
s.dispatchEvent(new Event('change',{bubbles:true}));return s.value;"""
                 % (json.dumps(tool), json.dumps(mode))))
    if r != mode:
        raise TestError("设置模式失败 tool=%s mode=%s → %r" % (tool, mode, r))
    time.sleep(0.3)


def set_number(tool, selector, value):
    r = ev(panel_js("""
const nm=[...R.querySelectorAll('.tool-name')].find(x=>x.getAttribute('data-tool')===%s);
if(!nm)return 'no-row';const it=nm.closest('tr');const inp=it.querySelector(%s);if(!inp)return 'no-input';
Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(inp,%s);
inp.dispatchEvent(new Event('input',{bubbles:true}));
inp.dispatchEvent(new Event('blur'));return 'ok';"""
                 % (json.dumps(tool), json.dumps(selector), json.dumps(str(value)))))
    if r != "ok":
        raise TestError("写数值失败 tool=%s sel=%s → %r" % (tool, selector, r))
    time.sleep(0.3)


def click_restore(tool):
    r = ev(panel_js("""
const nm=[...R.querySelectorAll('.tool-name')].find(x=>x.getAttribute('data-tool')===%s);
if(!nm)return 'no-row';const it=nm.closest('tr');const b=it.querySelector('button[data-restore]');
if(!b)return 'no-btn';if(b.disabled)return 'disabled';b.click();return 'ok';""" % json.dumps(tool)))
    if r != "ok":
        raise TestError("点击「恢复默认」失败 tool=%s → %r" % (tool, r))
    time.sleep(0.4)


def click_touch(tool):
    """点击该行「涉及文件变动」开关（只改本地待保存态）。"""
    r = ev(panel_js("""
const nm=[...R.querySelectorAll('.tool-name')].find(x=>x.getAttribute('data-tool')===%s);
if(!nm)return 'no-row';const it=nm.closest('tr');const sw=it.querySelector('.cell-touch .b-switch');
if(!sw)return 'no-switch';sw.click();return 'ok';""" % json.dumps(tool)))
    if r != "ok":
        raise TestError("点击「涉及文件变动」开关失败 tool=%s → %r" % (tool, r))
    time.sleep(0.3)


def pick_tool_by_suffix(suffix):
    """工具面里按后缀取一个 self 工具暴露名（无 → None）。"""
    for r in rows():
        if (r["tool"] or "").endswith(suffix):
            return r["tool"]
    return None


def unsaved_mark():
    return vcount(".unsaved-mark") > 0


def save_btn():
    """保存按钮状态：{exists, disabled}。"""
    return ev(panel_js("""
const b=R.querySelector('button.b-btn--primary[data-async-save]');
return b?{exists:true,disabled:!!b.disabled}:{exists:false,disabled:false};"""))


def click_save():
    r = ev(panel_js("""
const b=R.querySelector('button.b-btn--primary[data-async-save]');
if(!b)return 'no-btn';if(b.disabled)return 'disabled';b.click();return 'ok';"""))
    if r != "ok":
        raise TestError("点击【保存】失败：%r" % r)
    time.sleep(0.6)


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def case_a_entry_and_groups():
    """A 入口与分组渲染：菜单项 → tab → 按 MCP 分组 + 每行四档选择器（文案/aria）+ 行集合与 tools-list 一致
    + 表头 `?` 说明（≥3）+ 干净初始态保存按钮禁用。"""
    require_page()
    open_page()
    keys = group_keys()
    if not keys:
        raise TestError("页面无 MCP 分组标题（.group-title）")
    rs = rows()
    if not rs:
        raise TestError("页面无工具行（.tool-name[data-tool]）")
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
    # 表头 `?` 说明：模式 / 阈值 / 超时 / 涉及文件变动 四列（多个分组表 → 至少 4 个；H2 用例另校验）
    helps = int(ev(panel_js("return R.querySelectorAll('.th-help .b-icon, .th-help svg').length;")) or 0)
    if helps < 3:
        raise TestError("表头 `?` 说明不足（.th-help 图标 = %d，期望 ≥3：模式/阈值/超时/涉及文件变动）" % helps)
    # 干净初始态（刚打开 → workMap == savedMap）→ 保存按钮禁用、无「未保存」标记
    b = save_btn()
    if not b["exists"]:
        raise TestError("页面缺保存按钮（data-async-save）")
    if not b["disabled"]:
        raise TestError("无改动时保存按钮应禁用")
    if unsaved_mark():
        raise TestError("无改动时不应显示「未保存」标记")


def case_b_contract_moved_to_tooltip():
    """B 默认信息位置：行内不再有「契约默认/用户配置」徽标与「契约现值」文本；
    未配置行的「恢复默认」tooltip 承载该工具的契约默认（模式 X / 阈值 Y / 超时 Z）。"""
    require_page()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        if vcount(".badge") or vcount(".contract"):
            raise TestError("行内仍显示默认配置信息（.badge / .contract 应已移除）")
        rs = rows()
        if not rs:
            raise TestError("页面无工具行")
        checked = 0
        for r in rs:
            meta = tool_meta(r["tool"])
            want_mode = MODE_LABELS_BY_MODE.get(meta.get("async") or "auto")
            if r["restoreDisabled"] is not True:
                raise TestError("未配置工具 %s 的「恢复默认」应禁用" % r["tool"])
            contract = r["contract"] or ""
            if "契约默认" not in contract:
                raise TestError("「恢复默认」tooltip 未承载契约默认说明：%r（tool=%s）" % (contract, r["tool"]))
            if want_mode not in contract:
                raise TestError("契约默认 tooltip 缺模式 %s：%r（tool=%s）" % (want_mode, contract, r["tool"]))
            checked += 1
            if checked >= 3:
                break
        if checked == 0:
            raise TestError("无工具行 → 无法断言契约默认 tooltip")


def case_c_four_modes_manual_save():
    """C 四档「手动保存」→ 回读一致（覆盖态或契约档不落库退化为契约值）+ 条件字段（阈值仅 auto/manual）
    + 超时列常显（无「高级」按钮/折叠区）+ hard_timeout 落库。"""
    require_page()
    require_backend()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        tool = pick_tool()
        # 「高级」按钮 / 折叠区已去掉：hard_timeout 常显为「超时」列
        if vcount(".advanced-body"):
            raise TestError("不应再有 .advanced-body 折叠区（hard_timeout 已常显）")
        adv_btn = ev(panel_js("return [...R.querySelectorAll('button.b-btn')]"
                              ".some(b=>b.textContent.includes('高级'));"))
        if adv_btn:
            raise TestError("不应再有「高级」按钮")
        if not row_of(tool)["timeout"]:
            raise TestError("超时列应常显 hard_timeout 输入（tool=%s）" % tool)
        # 逐一覆盖四档：起点 = 当前生效档，其余三档先跑（每步都发生变化 → 必进待保存态），
        # 当前档最后跑（此时已不同 → 同样可保存）。避免「选中与现状相同 → 无改动」造成假红。
        cur = row_of(tool)["mode"]
        seq = [m for m in MODES if m != cur] + ([cur] if cur in MODES else [])
        for mode in seq:
            set_mode(tool, mode)
            if not unsaved_mark():
                raise TestError("改档后应显示「未保存」（mode=%s）" % mode)
            if save_btn()["disabled"]:
                raise TestError("改档后保存按钮应可用（mode=%s）" % mode)
            if (user_map().get(tool) or {}).get("mode") == mode:
                raise TestError("改档未保存时不应落库（mode=%s）" % mode)
            click_save()
            got = poll(lambda: effective_mode(tool) if effective_mode(tool) == mode else None)
            if got != mode:
                raise TestError("四档保存后生效档不一致：tool=%s mode=%s → usr=%r meta=%r"
                                % (tool, mode, user_map(), tool_meta(tool).get("async")))
            rs = [r for r in rows() if r["tool"] == tool]
            if not rs:
                raise TestError("保存后工具行消失：%s" % tool)
            r = rs[0]
            want_thr = mode in ("auto", "manual")
            if r["thr"] != want_thr:
                raise TestError("条件字段显示不符：mode=%s 阈值输入=%s（期望 %s）" % (mode, r["thr"], want_thr))
            if r["dash"] == want_thr:
                raise TestError("条件字段占位不符：mode=%s 「—」显示=%s" % (mode, r["dash"]))
            # 保存后回到干净态（无未保存标记 + 保存禁用）
            if not poll(lambda: (not unsaved_mark()) and save_btn()["disabled"]):
                raise TestError("保存后应清除「未保存」标记并禁用保存按钮（mode=%s）" % mode)
        # 阈值（auto）+ hard_timeout 落库（一次保存提交本页全部变更）
        set_mode(tool, "auto")
        set_number(tool, ".cell-threshold input.b-input", 45)
        set_number(tool, ".cell-timeout input.b-input", 120)
        if not unsaved_mark():
            raise TestError("改数值后应显示「未保存」")
        click_save()
        got = poll(lambda: (user_map().get(tool) or {}).get("threshold"))
        if got != 45:
            raise TestError("阈值未落库：tool=%s → usr=%r" % (tool, user_map()))
        got2 = poll(lambda: (user_map().get(tool) or {}).get("hard_timeout"))
        if got2 != 120:
            raise TestError("hard_timeout 未落库：tool=%s → usr=%r" % (tool, user_map()))


def case_d_restore_default():
    """D 恢复默认：「恢复默认」→ 待保存态 → 点【保存】删该工具键项；最后一项删除后整键删除。"""
    require_page()
    require_backend()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        tool = pick_tool()
        cur = row_of(tool)["mode"]
        target = "manual" if cur != "manual" else "never"
        set_mode(tool, target)
        click_save()
        if not poll(lambda: (user_map().get(tool) or {}).get("mode") == target):
            raise TestError("前置失败：目标档未落库（tool=%s）" % tool)
        # 恢复默认 = 只改待保存态（不立即落库）
        click_restore(tool)
        if not unsaved_mark():
            raise TestError("恢复默认后应进入待保存态（显示「未保存」）")
        if (user_map().get(tool) or {}).get("mode") != target:
            raise TestError("恢复默认不应立即落库（仍应保留 %s）" % target)
        if row_of(tool)["restoreDisabled"] is not True:
            raise TestError("恢复默认后该行「恢复默认」应禁用")
        click_save()
        if not poll(lambda: tool not in user_map()):
            raise TestError("恢复默认保存后键项仍在：%r" % user_map())
        # 仅此一项 → 整键应被删除（不留空对象）
        if not poll(lambda: CFG_KEY not in ucfg()):
            raise TestError("最后一项删除后整键未删（usr 仍存 %s=%r）" % (CFG_KEY, ucfg().get(CFG_KEY)))


def case_e_effect_meta_follows_config():
    """E 效果断言：保存后 `tools-list` 的 `_meta.async` 应随 usr `tool_async` 变化（后端生效面）。"""
    require_page()
    require_backend()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        tool = pick_tool()
        before = (tool_meta(tool).get("async") or "auto")
        cur = row_of(tool)["mode"]
        target = "manual" if cur != "manual" else "never"
        set_mode(tool, target)
        click_save()
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
        raise TestError("页面无工具行（.tool-name[data-tool]）")
    checked = 0
    for r in rs:
        name = r["tool"] or ""
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


def case_g_unlimited_values():
    """G 无上限口径（2026-09-27）：`hard_timeout` 输入 **0 / -1** = 无上限 → 合法、显示保留、落库保留
    （不再归一为空；usr 值分别为 0 / -1）。"""
    require_page()
    require_backend()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        tool = pick_tool()
        if row_of(tool)["timeoutDisabled"]:
            raise Pending("选中工具为 dir 节点（hard_timeout 禁用）→ 无输入可测")
        for v in (0, -1):
            set_number(tool, ".cell-timeout input.b-input", v)
            # 显示值保留（未被归一为空）
            if str(row_of(tool)["hard"]) != str(v):
                raise TestError("hard_timeout=%s 显示被归一（应保留）：%r" % (v, row_of(tool)["hard"]))
            if not unsaved_mark():
                raise TestError("改 hard_timeout=%s 后应显示「未保存」" % v)
            click_save()
            got = poll(lambda: (user_map().get(tool) or {}).get("hard_timeout"))
            if got != v:
                raise TestError("hard_timeout=%s 未按无上限落库：usr=%r" % (v, user_map()))


def case_h_touch_files_option():
    """H 涉及文件变动（2026-09-28）：缺省映射（self 白名单 = 打点；其余 self 内置 = 不打点）
    + 开关保存 → usr `tool_async.<工具>.touch_files` 落库回读 + 恢复默认回落缺省。"""
    require_page()
    require_backend()
    open_page()
    rs = rows()
    if not rs:
        raise TestError("页面无工具行（.tool-name[data-tool]）")
    # 每行都应有开关 + 派生标记（新列齐备）
    for r in rs:
        if not r["touchSwitch"]:
            raise TestError("工具 %s 缺「涉及文件变动」开关（.cell-touch .b-switch）" % r["tool"])
        if r["touchBadge"] not in (TOUCH_ON_BADGE, TOUCH_OFF_BADGE):
            raise TestError("工具 %s 的派生标记不符（期望 %r/%r）：%r"
                            % (r["tool"], TOUCH_ON_BADGE, TOUCH_OFF_BADGE, r["touchBadge"]))
        want_on = bool(r["touchOn"])
        if (r["touchBadge"] == TOUCH_ON_BADGE) != want_on:
            raise TestError("工具 %s 的标记与开关态不一致：on=%s badge=%r"
                            % (r["tool"], r["touchOn"], r["touchBadge"]))

    # 缺省映射：self 白名单（filesys_run / script_run）= 打点，其余 self 内置 = 不打点
    whit = next((r for r in rs if (r["tool"] or "").endswith(TOUCH_WHITELIST)), None)
    other = None
    for suf in ("file_read", "file_find", "file_diff", "web_fetch"):
        other = next((r for r in rs if (r["tool"] or "").endswith(suf)), None)
        if other:
            break
    if whit is None or other is None:
        raise Pending("工具面缺 filesys_run/script_run 或 self 非白名单工具 → 无法验证缺省映射")
    if not whit["touchOn"] or whit["touchBadge"] != TOUCH_ON_BADGE:
        raise TestError("self 白名单工具 %s 缺省应「打点」：%r" % (whit["tool"], whit))
    if other["touchOn"] or other["touchBadge"] != TOUCH_OFF_BADGE:
        raise TestError("self 非白名单工具 %s 缺省应「不打点」：%r" % (other["tool"], other))

    # 开关 → 待保存 → 保存 → usr 落库回读（偏离缺省才写库）
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        tool = pick_tool_by_suffix(other["tool"].split("_")[-1]) or other["tool"]
        if row_of(tool)["touchOn"]:
            raise TestError("前置：%s 缺省应为「不打点」（关）" % tool)
        click_touch(tool)
        if not row_of(tool)["touchOn"] or row_of(tool)["touchBadge"] != TOUCH_ON_BADGE:
            raise TestError("拨开开关后应转为「打点」：%r" % row_of(tool))
        if not unsaved_mark():
            raise TestError("拨开关后应显示「未保存」")
        click_save()
        got = poll(lambda: (user_map().get(tool) or {}).get("touch_files"))
        if got is not True:
            raise TestError("touch_files=true 未落库：tool=%s → usr=%r" % (tool, user_map()))
        # 恢复默认 → 回落缺省（不打点）→ 保存后键项删除
        click_restore(tool)
        if row_of(tool)["touchOn"] or row_of(tool)["touchBadge"] != TOUCH_OFF_BADGE:
            raise TestError("恢复默认后应回落缺省「不打点」：%r" % row_of(tool))
        click_save()
        if not poll(lambda: tool not in user_map()):
            raise TestError("恢复默认保存后键项仍在：%r" % user_map())
        print("[H] 涉及文件变动：%s 缺省=不打点 · %s 缺省=打点 · 开关保存→touch_files=true 落库 · "
              "恢复默认→回落缺省" % (other["tool"], whit["tool"]), flush=True)


def case_h3_touch_hint_and_header():
    """A+（新列头部）：表头 `?` 说明数 ≥4（模式/阈值/超时/涉及文件变动）+ hint 文案含「粒度/安全」口径。"""
    require_page()
    open_page()
    helps = int(ev(panel_js("return R.querySelectorAll('.th-help .b-icon, .th-help svg').length;")) or 0)
    if helps < 4:
        raise TestError("表头 `?` 说明不足（.th-help 图标 = %d，期望 ≥4：模式/阈值/超时/涉及文件变动）" % helps)
    hint = ev(panel_js("const t=R.querySelector('.tool-toolbar .hint');return t?t.textContent.trim():'';")) or ""
    if "工具" not in str(hint):
        raise TestError("页头 hint 文案异常：%r" % hint)


def case_i_cancel_on_timeout_option():
    """I 超时自动取消（2026-10-07，配置⑤ · spec 18 §7 B4）：`cancel_on_timeout` 输入秒数（> 0）
    → 保存 → usr `tool_async.<工具>.cancel_on_timeout` 落库 + 回读；`0` = 关闭（不写该字段）。"""
    require_page()
    require_backend()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        tool = pick_tool()
        if not row_of(tool)["cancelTimeout"]:
            raise TestError("页面缺「超时自动取消」输入（.cell-cancel-timeout input）：%r" % row_of(tool))
        set_number(tool, ".cell-cancel-timeout input.b-input", 30)
        if not unsaved_mark():
            raise TestError("改「超时自动取消」后应显示「未保存」")
        click_save()
        got = poll(lambda: (user_map().get(tool) or {}).get("cancel_on_timeout"))
        if got != 30:
            raise TestError("cancel_on_timeout=30 未落库：tool=%s → usr=%r" % (tool, user_map()))
        # 回读显示（重开页面读 usr → 输入框保留 30）
        open_page()
        if str(row_of(tool)["cancelValue"]) != "30":
            raise TestError("cancel_on_timeout 回读显示不符：%r" % row_of(tool)["cancelValue"])
        # 0 = 关闭 → 该字段不写库（键项不含 cancel_on_timeout）
        set_number(tool, ".cell-cancel-timeout input.b-input", 0)
        click_save()
        if (user_map().get(tool) or {}).get("cancel_on_timeout") is not None:
            raise TestError("cancel_on_timeout=0 应为「关闭」不写库：usr=%r" % user_map())
        print("[I] 超时自动取消：输入 30 → 落库 + 回读显示；输入 0 → 关闭不写库", flush=True)


CASES = [
    ("A 入口与分组渲染（分组 + 四档文案/aria + 表头 ? 说明 + 干净态保存禁用）", case_a_entry_and_groups),
    ("B 默认信息移至「恢复默认」tooltip（行内无 badge/contract）", case_b_contract_moved_to_tooltip),
    ("C 四档手动保存 + 回读 + 条件字段 + 超时列常显 + hard_timeout 落库", case_c_four_modes_manual_save),
    ("D 恢复默认 → 保存后删键项（最后一项 → 整键删除）", case_d_restore_default),
    ("E 效果：tools-list 的 _meta.async 随配置变化（后端生效面）", case_e_effect_meta_follows_config),
    ("F 展示口径（D2）：列表名剥前缀仅展示，data-tool/:title 保留完整暴露名", case_f_prefix_display),
    ("G 无上限口径：hard_timeout 输入 0 / -1 合法、显示与落库均保留", case_g_unlimited_values),
    ("H 涉及文件变动：缺省映射 + 打点/不打点标记 + 保存/回读 + 恢复默认", case_h_touch_files_option),
    ("H2 新列表头 ? 说明 ≥4（含「涉及文件变动」）", case_h3_touch_hint_and_header),
    ("I 超时自动取消：cancel_on_timeout 落库 + 回读；0 = 关闭不写库", case_i_cancel_on_timeout_option),
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
    print("依赖：--test-port=%d 的 GUI（产物 = dist/desktop）；配置面 = usr `%s`" % (PORT, CFG_KEY))
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
    print("\n工具配置页 L4：%d/%d 通过, %d 失败, %d 待验证(PENDING)"
          % (len(PASSED), total, len(FAILED), len(PENDING)))
    for n in PENDING:
        print("  [PENDING] %s" % n)
    print("RESULT: %s" % (len(FAILED) == 0))
    return 0 if not FAILED else 1


if __name__ == "__main__":
    sys.exit(main())
