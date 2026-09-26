# -*- coding: utf-8 -*-
"""L4：工具沙箱配置页（usr 键 `tool_sandbox`，**executor 级**）—— 三行渲染 / 只读工具清单 /
手动保存（无改动禁用）/ 恢复未设置 / 第三方·http·sse 边界说明。

需求（用户口径，2026-09-26）：沙箱不是工具级、而是 **self 的 executor 级** —— 三个执行器
（core / desktop / browser，= 契约 `_meta.category`）各一个开关，管制该执行器下全部工具；
拨动只改本地待保存态，点【保存】才落库；**无改动时保存按钮禁用**（「不做 change 就保存」）。

驱动面（**零新增 MQ 主题**）：
  * 工具清单 = 既有客户端能力面 `tools-list`（每项 `_meta.category` / `_meta.server`）；
  * 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（usr 键 `tool_sandbox`，
    JSON `{"core":bool,"desktop":bool,"browser":bool}`）。

覆盖：
  A 入口与三行渲染：菜单项 → preview tab 打开 → `.exec-row` × 3（data-exec = core/desktop/browser）
    + 每行一个 `.b-switch`（带 aria-label）
  B 只读工具清单：每行列出的工具 = `tools-list` 中该类别工具（展示剥前缀；`:title` = 完整暴露名）
  C 无改动时保存禁用：干净初始态 → 保存按钮 disabled 且无「未保存」标记
  D 拨动 → 「未保存」标记出现 + 保存按钮可用 → 点保存 → `data-user-config-load` 回读
    `tool_sandbox` = `{"core":true}`（executor 级形态；不含任何工具名键）
  E 「恢复未设置」→ 待保存态 → 保存 → 键项/整键被删除（回落不隔离）
  F 第三方 / http·sse 边界说明存在（.exec-note 文案含「第三方」+「http」）

前置（本脚本自起，结束自动回收；见 harness.py）：
  `dist/desktop\\chonkpilot.exe --test-port=2345 --work-dir ws`
运行：`python run_tool_sandbox.py`

**PENDING 口径（不伪造、不放宽断言）**：产物未含新页面（工具栏设置菜单无「工具沙箱配置」）时
打印 `[PENDING]` 并单独计入「待验证」，**不计入通过**（等待重建 build-desktop.ps1 后全量验证）。
前置成立时全部断言按原样严格执行（任何不符即 FAIL）。
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
CFG_KEY = "tool_sandbox"
KIND = "settings-tool-sandbox"
ROOT = ".settings-page"
MENU_LABEL = "工具沙箱配置"
CATEGORIES = ["core", "desktop", "browser"]

c = _h.acquire_gui(PORT, work_dir=WS).client
_h.suite_config_guard(c)
_h.ensure_locale(c)

PASSED, FAILED, PENDING = [], [], []


class Pending(Exception):
    """前置不成立（产物未重建）：等待主线全量验证，**不**计入通过。"""


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
    """usr `tool_sandbox` 视图（对象；支持 JSON 字符串形态）。"""
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


def tools_by_category():
    """tools-list 中三个 executor 类别的工具（暴露名列表），键 = 类别。"""
    out = {k: [] for k in CATEGORIES}
    for tl in tools_list():
        meta = tl.get("_meta") or {}
        cat = str(meta.get("category") or "").lower()
        if cat in out:
            out[cat].append(tl.get("name"))
    return out


def reset_key():
    """把 usr `tool_sandbox` 清为「未设置」以取得确定初始态（套件级 guard 会在退出前还原）。"""
    c.req("data-user-config-delete", {"id": CFG_KEY})
    time.sleep(0.4)


# ── 前置探测 ───────────────────────────────────────────────

SEL_PAGE = [None]


def probe_page():
    """产物是否已重建出新页面：工具栏设置下拉是否出现「工具沙箱配置」项。"""
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
        raise Pending("产物未含新页面（工具栏设置菜单无「工具沙箱配置」）"
                      "→ 等待主线统一重建 build-desktop.ps1 后全量验证")


# ── 页面操作 ───────────────────────────────────────────────

def open_page():
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": KIND})
    if not wait_vis(ROOT):
        raise TestError("工具沙箱配置页未打开（kind=%s root=%s）" % (KIND, ROOT))
    time.sleep(0.8)


def rows():
    """三个 executor 行：类别 / 开关态 / 状态文字 / 只读工具清单（含完整暴露名与展示名）。"""
    return ev(panel_js("""
return [...R.querySelectorAll('.exec-row')].map(it=>{
  const sw=it.querySelector('.b-switch');
  const st=it.querySelector('.exec-state');
  return {
    cat: it.getAttribute('data-exec'),
    aria: sw?sw.getAttribute('aria-label'):null,
    checked: sw?sw.classList.contains('is-checked'):false,
    disabled: sw?sw.classList.contains('is-disabled'):false,
    state: st?st.textContent.trim():'',
    tools: [...it.querySelectorAll('.exec-tool')].map(t=>({
      name: t.getAttribute('data-tool'),
      disp: t.textContent.trim(),
      title: t.getAttribute('title'),
    })),
    naBadges: it.querySelectorAll('.tool-na').length,
  };
});""")) or []


def row_of(cat):
    for r in rows():
        if r["cat"] == cat:
            return r
    raise TestError("页面无该 executor 行：%s" % cat)


def unsaved_mark():
    return vcount(".unsaved-mark") > 0


def save_btn():
    """保存按钮状态：{exists, disabled}。"""
    return ev(panel_js("""
const b=R.querySelector('button.b-btn--primary[data-sandbox-save]');
return b?{exists:true,disabled:!!b.disabled}:{exists:false,disabled:false};"""))


def click_switch(cat):
    r = ev(panel_js("""
const it=[...R.querySelectorAll('.exec-row')].find(x=>x.getAttribute('data-exec')===%s);
if(!it)return 'no-row';const sw=it.querySelector('.b-switch');if(!sw)return 'no-sw';
if(sw.classList.contains('is-disabled'))return 'disabled';sw.click();return 'ok';""" % json.dumps(cat)))
    if r != "ok":
        raise TestError("拨动 executor Switch 失败 cat=%s → %r" % (cat, r))
    time.sleep(0.5)


def click_restore(cat):
    r = ev(panel_js("""
const it=[...R.querySelectorAll('.exec-row')].find(x=>x.getAttribute('data-exec')===%s);
if(!it)return 'no-row';const b=[...it.querySelectorAll('button.b-btn')].find(x=>x.textContent.includes('恢复未设置'));
if(!b)return 'no-btn';if(b.disabled)return 'disabled';b.click();return 'ok';""" % json.dumps(cat)))
    if r != "ok":
        raise TestError("点击「恢复未设置」失败 cat=%s → %r" % (cat, r))
    time.sleep(0.5)


def click_save():
    r = ev(panel_js("""
const b=R.querySelector('button.b-btn--primary[data-sandbox-save]');
if(!b)return 'no-btn';if(b.disabled)return 'disabled';b.click();return 'ok';"""))
    if r != "ok":
        raise TestError("点击保存按钮失败：%r" % r)
    time.sleep(0.6)


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def case_a_three_executor_rows():
    """A 入口与三行渲染：三行（core/desktop/browser）+ 每行一个 Switch（带 aria-label）。"""
    require_page()
    open_page()
    rs = rows()
    if len(rs) != 3:
        raise TestError("executor 行数 = %d，期望 3（core/desktop/browser）" % len(rs))
    if [r["cat"] for r in rs] != CATEGORIES:
        raise TestError("executor 行顺序/类别不符：%r" % [r["cat"] for r in rs])
    for r in rs:
        if not r["aria"] or r["cat"] not in r["aria"]:
            raise TestError("executor %s 的 Switch 缺 aria-label（可访问性）：%r" % (r["cat"], r["aria"]))


def case_b_readonly_tool_lists():
    """B 只读工具清单：每行工具 = tools-list 中该类别工具；展示剥前缀；title = 完整暴露名。"""
    require_page()
    open_page()
    want = tools_by_category()
    for r in rows():
        got = [t["name"] for t in r["tools"]]
        if sorted(got) != sorted(want.get(r["cat"], [])):
            raise TestError("executor %s 的工具清单与 tools-list 不一致：页面 %r，期望 %r"
                            % (r["cat"], sorted(got), sorted(want.get(r["cat"], []))))
        for t in r["tools"]:
            name = t["name"] or ""
            if t["title"] != name:
                raise TestError("工具 %s 的 :title=%r，期望完整暴露名（重名可区分）" % (name, t["title"]))
            if name.startswith("self_") and t["disp"] != name[5:]:
                raise TestError("工具 %s 展示名 %r，期望剥掉 self_ 前缀后 %r" % (name, t["disp"], name[5:]))


def case_c_save_disabled_when_clean():
    """C 无改动时保存禁用：干净初始态 → 保存按钮 disabled 且无「未保存」标记。"""
    require_page()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        b = save_btn()
        if not b["exists"]:
            raise TestError("页面缺保存按钮（data-sandbox-save）")
        if not b["disabled"]:
            raise TestError("无改动时保存按钮应禁用（不做 change 就保存）")
        if unsaved_mark():
            raise TestError("无改动时不应显示「未保存」标记")
        if user_map():
            raise TestError("前置失败：usr tool_sandbox 未清空：%r" % user_map())


def case_d_toggle_then_save():
    """D 拨动 → 未保存标记 + 保存可用 → 保存 → 回读 executor 级形态。"""
    require_page()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        if not save_btn()["disabled"]:
            raise TestError("前置失败：干净态保存按钮应禁用")
        click_switch("core")
        if not unsaved_mark():
            raise TestError("拨动后应显示「未保存」标记")
        if save_btn()["disabled"]:
            raise TestError("拨动后保存按钮应可用")
        r = row_of("core")
        if not r["checked"] or r["state"] not in ("开", "On"):
            raise TestError("拨动后 core 行态不符：checked=%s state=%r" % (r["checked"], r["state"]))
        # 拨动本身不落库（仍是干净库）
        if user_map():
            raise TestError("拨动未保存时不应落库：%r" % user_map())
        click_save()
        got = poll(lambda: user_map() if user_map().get("core") is True else None)
        if not got:
            raise TestError("保存后回读不一致：usr tool_sandbox=%r，期望 {\"core\": true}" % user_map())
        if set(got.keys()) - set(CATEGORIES) or got.get("core") is not True:
            raise TestError("落库形态须为 executor 级 {\"core\": true}：%r" % got)
        # 保存后回到干净态（无未保存标记 + 保存禁用）
        if not poll(lambda: (not unsaved_mark()) and save_btn()["disabled"]):
            raise TestError("保存后应清除「未保存」标记并禁用保存按钮")


def case_e_restore_unset():
    """E 恢复未设置 → 待保存态 → 保存 → 键项/整键删除（回落不隔离）。"""
    require_page()
    with _h.user_config_guard(c, [CFG_KEY]):
        reset_key()
        open_page()
        click_switch("core")
        click_save()
        if not poll(lambda: user_map().get("core") is True):
            raise TestError("前置失败：core 未落库：%r" % user_map())
        # 恢复未设置 → 待保存态（不立即落库）
        click_restore("core")
        if not unsaved_mark():
            raise TestError("恢复未设置后应进入待保存态（显示「未保存」）")
        if user_map().get("core") is not True:
            raise TestError("恢复未设置不应立即落库（仍应保留 core=true）：%r" % user_map())
        click_save()
        if not poll(lambda: CFG_KEY not in ucfg()):
            raise TestError("恢复未设置保存后应删整键（usr 仍存 %s=%r）" % (CFG_KEY, ucfg().get(CFG_KEY)))


def case_f_boundary_hint():
    """F 第三方 / http·sse 边界说明 + 跳转 MCP 入口存在。"""
    require_page()
    open_page()
    note = ev(panel_js("""
const n=R.querySelector('.exec-note');return n?{text:n.innerText,hasBtn:!!n.querySelector('button.b-btn')}:null;"""))
    if not note:
        raise TestError("缺第三方 / http·sse 边界说明（.exec-note）")
    txt = note["text"]
    if "第三方" not in txt or "http" not in txt:
        raise TestError("边界说明须含「第三方」与 http/sse 不可隔离说明：%r" % txt)
    if not note["hasBtn"]:
        raise TestError("边界说明须给跳转 MCP 配置的入口")


CASES = [
    ("A 入口与三行渲染（core/desktop/browser + 每行 Switch 带 aria-label）", case_a_three_executor_rows),
    ("B 只读工具清单（= tools-list 类别工具；剥前缀展示 + title 完整暴露名）", case_b_readonly_tool_lists),
    ("C 无改动时保存禁用（不做 change 就保存）", case_c_save_disabled_when_clean),
    ("D 拨动 → 未保存标记 + 保存 → 回读 executor 级 {\"core\":true}", case_d_toggle_then_save),
    ("E 恢复未设置 → 待保存态 → 保存 → 删键/整键删除", case_e_restore_unset),
    ("F 第三方 / http·sse 边界说明 + 跳转 MCP 入口", case_f_boundary_hint),
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
    print("依赖：--test-port=%d 的 GUI（产物 = dist/desktop）；配置面 = usr `%s`（executor 级）" % (PORT, CFG_KEY))
    print("前置探测：页面=%s" % probe_page())
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
    print("\n工具沙箱配置页 L4：%d/%d 通过, %d 失败, %d 待验证(PENDING)"
          % (len(PASSED), total, len(FAILED), len(PENDING)))
    for n in PENDING:
        print("  [PENDING] %s" % n)
    print("RESULT: %s" % (len(FAILED) == 0))
    return 0 if not FAILED else 1


if __name__ == "__main__":
    sys.exit(main())
