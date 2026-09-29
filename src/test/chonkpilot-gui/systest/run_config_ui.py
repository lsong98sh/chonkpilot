# -*- coding: utf-8 -*-
"""L4「配置项 UI 保存」断言套件 —— 逐配置项「模拟 click → 触发保存 → 数据验证」。

用户要求（2026-09-13）：
  * 每个配置项都要用「模拟 click + 数据验证」确认是否保存；
  * 加强 UI 元素断言：文字（i18n 精确）/ 位置（getBoundingClientRect）/ 颜色（getComputedStyle）。

覆盖矩阵
  A. 项目设置页（preview tab `settings-project`；Tabs：安全/上下文管理/CodeGraph 索引/Vfts 全文索引/文件历史）
     A1 页签文字 + 页面在视口内（位置）
     A2 上下文管理：记忆库开关**联动禁用**其子项（位置/颜色 opacity=0.5）+ 总结提示词默认非空
        → 拨记忆库开关（只改本地态）→ 点击【保存】→ data-prj-config-list 回读 memory.enabled
        （2026-09-27：K3/A2 改「手动保存」—— 拨开关/改数值不再即时落库，须点【保存】）
     A3 CodeGraph 索引：文件后缀 / 排除的目录和文件**默认值已回显**；启用开关点击保存并回读 enable-codegraph；
        保存按钮点击（handleIndexSave）→ 回读 codegraph.exts；「叠加 gitignore」勾选（勾选后输入框仍可编辑）
        → 保存 → 回读 codegraph.stack-gitignore
     A4 Vfts 全文索引：默认值回显 + 启用开关点击保存并回读 + **编辑后**保存回读 vfts.exts
        （2026-09-15 产品改动：vfts 保存改为「仅写实际改动的键」，未改动 → 不写并提示
        「无改动（未写入配置）」；故本用例先写入哨兵值再点保存，覆盖「保存落库」路径）
  B. 用户设置页（preview tab settings-llm / settings-mcp / settings-paths）
     B1 LLM 配置：2 页签（**一览** / **默认模型**）——
        一览 = provider 清单（添加/编辑/删除，**无只读行、无「设为默认」**）；
        默认模型 = **主对话** + 5 个子系统下拉（提示词优化/记忆/压缩/分析/决策）
        （2026-09-26 用户口径：原「只读内置行 + 设为默认」已移除；`gui.system.builtins` 不再下发
         `builtinLLMs`，聊天输入框选择器只列 usr providers）
        + 添加弹窗字段文字 + 弹窗在视口内（位置）+ 表单**一行两列**（位置）→ 填表保存 →
        data-user-config-load 回读 llms → 清理
     B2 MCP：transport 与 url / runtime 的**从属显示**（切换 Select；args 不受 transport 门控）
        + 保存 runtime/args → 回读 → 清理
     B3 路径/工具链：系统页 Chrome **已被探测出**（路径非空 + 版本 x.y.z.w）；用户页含 Chrome 输入；
        项目页不含 Chrome（三级归属）
  C. 已删除项确不存在：旧配置弹窗（.config-dialog-body-scroll）、独立「提示词」页签
  D. 重启持久化复核：独立 work-dir（含 .git）启动 GUI → 点击「文件历史」开关保存 history.enabled
     → **重启 GUI 后重新打开页面回读**（开关状态）
  D2. 文件历史 UI 细节（同独立实例体例）：① 保留策略两个输入（保留个数/保留天数）改值 → 保存 →
     prj 键 `history.checkpoint_keep` / `history.checkpoint_ttl_days` 落库 + **重启后回读**；
     ② 只读时间轴区块（`[data-history-status]` 字段真值渲染 + `[data-history-timeline]` 表格 +
     **空态分流**：未启用 vs 已启用无数据文案不同）；③【清空历史】确认框：取消不写 / 确定改写
     `history.clear`

颜色断言口径：主按钮背景 = var(--accent)；禁用开关 opacity=0.5、禁用输入 opacity=0.6；
保存成功提示左边框 = var(--success)。

前置：dist-desktop\\chonkpilot.exe --test-port=2345 --work-dir=<大写盘符 ws>；
      本脚本自起独立实例 2347 验证「重启持久化」，结束回收。
运行：python run_config_ui.py
"""
import json
import os
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case
import run_explore_kb as kbmod   # 复用知识库树/原语面板操作助手（G 用例：恢复默认 / 右键菜单）

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
c = _h.acquire_gui(2345, work_dir=os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws")).client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：含 usr（llms/mcps/locale）→ 退出前回滚
_h.ensure_locale(c)  # 语言确定性：本套件按 zh-CN 文案断言（DB ui.locale 可能被他套件写成 en-US）
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(HERE))))
EXE = os.path.join(ROOT, "dist", "desktop", "chonkpilot.exe")
WS = os.path.join(HERE, "ws")
SHOTS = os.path.join(HERE, "_cfg_ui_shots")

HIST_PORT = 2347
HIST_WS = os.path.join(HERE, "ws_git")
HIST_DATA = os.path.join(HERE, "_cfg_hist_data")
# 底座实例端口（由外部启动，case D 的清理**绝不能**波及它）
BASE_PORT = 2345

# G 用例（本波新改动：恢复默认 / 知识库右键菜单）使用的三级 capability 根
APP_CAP = os.path.join(ROOT, "dist", "desktop", "capability")          # 系统级（只读）
PROJ_CAP = os.path.join(WS, ".chonkpilot", "capability")               # 项目级
PROJ_TOOL_DIR = os.path.join(PROJ_CAP, "tools", "core")
PROJ_TOOL = os.path.join(PROJ_TOOL_DIR, "file_find.tool.md")           # 与 app 级同名 → 有上一级可回填

# 项目设置页签期望文字（zh-CN projectConfig.json + configIO.json）
# 2026-09-20 批 3 · ⑯：末位新增「配置导入/导出」页签（usr 全局配置的导出/导入/恢复出厂）
PROJ_TABS = ["安全", "上下文管理", "CodeGraph 索引", "Vfts 全文索引", "文件历史", "日志", "配置导入/导出"]


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
    return int(ev("([...document.querySelectorAll(%s)].filter(e=>e.getBoundingClientRect().width>0)).length" % json.dumps(sel)) or 0)


def wait_vis(sel, max_wait=10):
    end = time.time() + max_wait
    while time.time() < end:
        if vcount(sel) > 0:
            return True
        time.sleep(0.3)
    return False


def shot(name):
    os.makedirs(SHOTS, exist_ok=True)
    try:
        c.screenshot(os.path.join(SHOTS, name))
    except Exception as e:
        print("    [warn] screenshot %s 失败: %s" % (name, e))


def css_color(css):
    """把 CSS 颜色（支持 var()）解析为计算后的 rgb() 字符串。"""
    return ev("(function(c){const d=document.createElement('div');d.style.background=c;"
              "document.body.appendChild(d);const v=getComputedStyle(d).backgroundColor;"
              "d.remove();return v;})(%s)" % json.dumps(css))


def vp():
    return ev("({w:window.innerWidth,h:window.innerHeight})")


def rect(sel):
    return ev("(function(){const e=[...document.querySelectorAll(%s)].find(x=>x.getBoundingClientRect().width>0);"
              "if(!e)return null;const r=e.getBoundingClientRect();"
              "return {l:r.left,t:r.top,r:r.right,b:r.bottom,w:r.width,h:r.height};})()" % json.dumps(sel))


def assert_in_viewport(sel, name):
    r = rect(sel)
    if not r:
        raise TestError("%s 不可见" % name)
    v = vp()
    if r["l"] < -1 or r["t"] < -1 or r["r"] > v["w"] + 1 or r["b"] > v["h"] + 1:
        raise TestError("%s 越界: rect=%r vp=%r" % (name, r, v))
    return r


# ── 页面打开 / 页签 ────────────────────────────────────────

def open_page(kind, root_sel, max_wait=12):
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": kind})
    if not wait_vis(root_sel, max_wait):
        raise TestError("配置页未打开: kind=%s root=%s" % (kind, root_sel))
    time.sleep(0.8)


def tab_labels(root_sel):
    return ev("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
              "if(!R)return [];return [...R.querySelectorAll('.b-tabs-item')].map(t=>t.textContent.trim());})()"
              % json.dumps(root_sel)) or []


def click_tab(label, root_sel):
    r = ev("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
           "if(!R)return 'no-root';const t=[...R.querySelectorAll('.b-tabs-item')]"
           ".find(x=>x.textContent.trim()===%s);if(!t)return 'no-tab';t.click();return 'ok';})()"
           % (json.dumps(root_sel), json.dumps(label)))
    time.sleep(0.9)
    if r != "ok":
        raise TestError("切换页签失败 %s: %s" % (label, r))


def panel_js(root_sel, inner):
    """在可见配置页根内执行 inner（内嵌 R）。"""
    return "(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"\
           "if(!R)return null;%s})()" % (json.dumps(root_sel), inner)


def panel_text(root_sel):
    return ev(panel_js(root_sel, "return R.innerText;")) or ""


def panel_textareas(root_sel):
    return ev(panel_js(root_sel, "return [...R.querySelectorAll('textarea.b-textarea')].map(x=>x.value);")) or []


def set_panel_textarea(root_sel, idx, value):
    """把可见面板内第 idx 个 textarea.b-textarea 的值设为 value（原生 setter + input 事件）。

    与 panel_textareas 取同一集合（不筛可见性）→ 下标语义一致。
    """
    r = ev(panel_js(root_sel, """
const ts=[...R.querySelectorAll('textarea.b-textarea')];
const t=ts[%d];if(!t)return 'no-ta';
Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(t,%s);
t.dispatchEvent(new Event('input',{bubbles:true}));
t.dispatchEvent(new Event('change',{bubbles:true}));return 'ok';""" % (idx, json.dumps(value))))
    if r != "ok":
        raise TestError("写文本框失败(idx=%d): %s" % (idx, r))
    time.sleep(0.3)


def llm_rows():
    """LLM 一览行（provider 清单）：[{text, btns}]。"""
    return ev(panel_js(".settings-page", """
return [...R.querySelectorAll('tbody tr')].map(r=>({
  text:r.innerText.replace(/\\s+/g,' ').trim(),
  btns:[...r.querySelectorAll('button')].map(b=>b.textContent.trim())}));""")) or []


def find_switch_by_label(root_sel, label):
    return ev(panel_js(root_sel, """
const row=[...R.querySelectorAll('.switch-row,.cg-toggle,.vf-toggle,.history-toggle,.form-item')]
  .find(x=>{const l=x.querySelector('.form-label');return l&&l.textContent.trim()===%s;});
if(!row)return null;const sw=row.querySelector('.b-switch');if(!sw)return null;
return {checked:sw.classList.contains('is-checked'),disabled:sw.classList.contains('is-disabled'),
        opacity:getComputedStyle(sw).opacity,d:!!sw.getAttribute('style')};""" % json.dumps(label)))


def click_switch_by_label(root_sel, label):
    r = ev(panel_js(root_sel, """
const row=[...R.querySelectorAll('.switch-row,.cg-toggle,.vf-toggle,.history-toggle,.form-item')]
  .find(x=>{const l=x.querySelector('.form-label');return l&&l.textContent.trim()===%s;});
if(!row)return 'no-row';const sw=row.querySelector('.b-switch');if(!sw)return 'no-sw';
if(sw.classList.contains('is-disabled'))return 'disabled';sw.click();return 'ok';""" % json.dumps(label)))
    time.sleep(0.8)
    return r


def click_primary(root_sel, exact=None):
    r = ev(panel_js(root_sel, """
const btns=[...R.querySelectorAll('button.b-btn--primary')].filter(b=>b.getBoundingClientRect().width>0);
const b=%s; if(!b)return 'no-btn'; b.click(); return 'ok';""" %
                     ("btns.find(x=>x.textContent.trim()===%s)" % json.dumps(exact) if exact else "btns[0]")))
    time.sleep(0.8)
    return r


# ── 数据面读写 ─────────────────────────────────────────────

def prj():
    r = c.req("data-prj-config-list", {})
    return (r.get("list") or {}) if isinstance(r, dict) else {}


def ucfg():
    r = c.req("data-user-config-load", {})
    return (r.get("data") or {}) if isinstance(r, dict) else {}


def prj_save(k, v):
    return c.req("data-prj-config-save", {"data": {"key": k, "value": v}})


def prj_guard(keys):
    """prj 配置快照-还原上下文（51 §6-8）：退出（含异常）即把 keys 还原回跑前状态
    （原本无该键 → 删除；原本有值 → 写回原值）。"""
    return _h.prj_config_guard(c, list(keys))


def fill_dialog(label_cands, value):
    r = ev("(function(){const cands=%s;"
           "const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
           "if(!R)return 'no-dialog';"
           "const it=[...R.querySelectorAll('.form-item')].find(x=>{const l=x.querySelector('.form-label');"
           "return l&&cands.some(cc=>l.textContent.includes(cc));});"
           "const inp=it?it.querySelector('input,textarea'):null;if(!inp)return 'not-found';"
           "const proto=inp instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;"
           "Object.getOwnPropertyDescriptor(proto,'value').set.call(inp,%s);"
           "inp.dispatchEvent(new Event('input',{bubbles:true}));"
           "inp.dispatchEvent(new Event('change',{bubbles:true}));return 'ok';})()"
           % (json.dumps(label_cands, ensure_ascii=False), json.dumps(value)))
    if r != "ok":
        raise TestError("填表失败(label≈%s): %s" % (label_cands, r))


def _kv_item_js(label_cands, inner):
    """在可见弹窗内、按 label 定位含 .kv-editor 的 form-item，执行 inner（内嵌 it）。"""
    return ("(function(){const cands=%s;"
            "const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
            "if(!R)return 'no-dialog';"
            "const it=[...R.querySelectorAll('.form-item')].find(x=>{const l=x.querySelector('.form-label');"
            "return l&&cands.some(cc=>l.textContent.includes(cc));});"
            "if(!it)return 'no-item';if(!it.querySelector('.kv-editor'))return 'no-editor';%s})()"
            % (json.dumps(label_cands, ensure_ascii=False), inner))


def fill_list_editor(label_cands, values):
    """把「……行编辑器」（KeyValueEditor，list 模式）填成 values（逐行 input）。

    KeyValueEditor 为受控行编辑（Vue 重渲染异步）→ 增删行须**每次点击后 sleep** 再复核。
    """
    def row_count():
        return int(ev(_kv_item_js(label_cands, "return it.querySelectorAll('.kv-row').length;")) or 0)

    for _ in range(row_count()):  # 清空已有行
        r = ev(_kv_item_js(label_cands, "const d=it.querySelector('.kv-row .kv-del');if(!d)return 'no-del';d.click();return 'ok';"))
        if r != "ok":
            raise TestError("行编辑器删除失败(label≈%s): %s" % (label_cands, r))
        time.sleep(0.15)
    for i, v in enumerate(values):
        r = ev(_kv_item_js(label_cands, "const a=it.querySelector('.kv-add');if(!a)return 'no-add';a.click();return 'ok';"))
        if r != "ok":
            raise TestError("行编辑器添加失败(label≈%s): %s" % (label_cands, r))
        time.sleep(0.2)
        r = ev(_kv_item_js(label_cands,
                           "const rows=[...it.querySelectorAll('.kv-row')];const row=rows[%d];if(!row)return 'no-row';"
                           "const vi=row.querySelector('.kv-value');if(!vi)return 'no-input';"
                           "Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(vi,%s);"
                           "vi.dispatchEvent(new Event('input',{bubbles:true}));return 'ok';" % (i, json.dumps(v))))
        if r != "ok":
            raise TestError("行编辑器填值失败(label≈%s,row=%d): %s" % (label_cands, i, r))
        time.sleep(0.15)


def dialog_labels():
    return ev("(function(){const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
              "if(!R)return [];return [...R.querySelectorAll('.form-label')].map(x=>x.textContent.trim());})()") or []


def wait_toast(cls, max_wait=4):
    end = time.time() + max_wait
    while time.time() < end:
        if vcount(".b-message." + cls) > 0:
            return True
        time.sleep(0.2)
    return False


def toast_border(cls):
    return ev("(function(){const e=document.querySelector('.b-message.%s');"
              "return e?getComputedStyle(e).borderLeftColor:null;})()" % cls)


# ══════════════════════════════════════════════════════════
# A. 项目设置页
# ══════════════════════════════════════════════════════════

def case_a1_tabs_text_position():
    """A1 项目设置页：页签**文字**精确 + 页面在视口内（位置）。"""
    open_page("settings-project", ".project-config-panel")
    shot("a1-project-config.png")
    labels = tab_labels(".project-config-panel")
    if labels != PROJ_TABS:
        raise TestError("项目设置页签文字不符：%r，期望 %r" % (labels, PROJ_TABS))
    assert_in_viewport(".project-config-panel", "项目配置页")


def case_a2_context_memory():
    """A2 上下文管理：文字 + 记忆库开关联动禁用子项（颜色）+ 总结提示词默认非空 + 点击保存落库。"""
    with prj_guard(["memory.enabled"]):
        open_page("settings-project", ".project-config-panel")
        click_tab("上下文管理", ".project-config-panel")
        shot("a2-context.png")
        txt = panel_text(".project-config-panel")
        for want in ("记忆配置", "启用记忆库", "用户偏好", "上下文压缩配置",
                     "保留完整对话内容的最近轮数（默认 10）",
                     "保留完整对话内容的 token 上限（默认 24000）",
                     "简化区 Token 压缩阈值（默认 20000）",
                     "编辑总结提示词（压缩时使用）"):
            if want not in txt:
                raise TestError("上下文管理缺文案：%r" % want)
        # 总结提示词：只读展示已移除 → 改「弹框内查看」（2026-09-26）：点「编辑」开弹框，内容非空
        r = ev(panel_js(".project-config-panel", """
const hs=[...R.querySelectorAll('.prompt-editor-header')];
const h=hs[0];
const b=h?[...h.querySelectorAll('.b-btn')].find(x=>/编辑|Edit/i.test(x.textContent.trim())):null;
if(!b)return 'no-btn';b.dispatchEvent(new MouseEvent('click',{bubbles:true}));return 'ok';"""))
        if r != "ok":
            raise TestError("总结提示词缺「编辑」按钮：%r" % r)
        time.sleep(0.6)
        if not wait_vis(".text-edit-body"):
            raise TestError("总结提示词编辑弹框未打开")
        val = dlg_textarea_value() or ""
        if len(val.strip()) < 8:
            raise TestError("总结提示词弹框内容疑似为空：%r" % (val[:40],))
        close_stray_dialogs()
        # 记忆库开关：关 → 子项全部禁用（联动）
        st = find_switch_by_label(".project-config-panel", "启用记忆库")
        if not st:
            raise TestError("未找到 启用记忆库 开关")
        # 2026-09-27（手动保存口径）：开关只改本地态，点【保存】才落库。
        # 先归一到「关闭」基准：若当前为开 → 关闭后点【保存】落库 OFF，使「已保存态」= OFF。
        if st["checked"]:
            if click_switch_by_label(".project-config-panel", "启用记忆库") != "ok":
                raise TestError("关闭记忆库开关失败")
            if click_primary(".project-config-panel", "保存") != "ok":
                raise TestError("归一记忆库为关闭时点击保存失败")
            if not wait_toast("b-message--success"):
                raise TestError("归一保存后未见成功提示")
        info = ev(panel_js(".project-config-panel", """
return {dis_inp:[...R.querySelectorAll('input.b-input.is-disabled')].filter(e=>e.getBoundingClientRect().width>0).length,
        dis_sw:[...R.querySelectorAll('.b-switch.is-disabled')].filter(e=>e.getBoundingClientRect().width>0).length,
        sw_opacity:(function(){const s=[...R.querySelectorAll('.b-switch.is-disabled')][0];
          return s?getComputedStyle(s).opacity:'-';})(),
        inp_opacity:(function(){const s=[...R.querySelectorAll('input.b-input.is-disabled')][0];
          return s?getComputedStyle(s).opacity:'-';})()};"""))
        if info["dis_inp"] < 2 or info["dis_sw"] < 1:
            raise TestError("记忆库关闭时子项未联动禁用：%r" % info)
        if info["sw_opacity"] != "0.5":
            raise TestError("禁用开关 opacity=%s，期望 0.5" % info["sw_opacity"])
        if info["inp_opacity"] != "0.6":
            raise TestError("禁用输入 opacity=%s，期望 0.6" % info["inp_opacity"])
        # 点击开启 → 只改本地态（子项解禁），**此时尚未落库**（手动保存口径）
        if click_switch_by_label(".project-config-panel", "启用记忆库") != "ok":
            raise TestError("开启记忆库开关失败")
        if ev(panel_js(".project-config-panel",
                       "return [...R.querySelectorAll('input.b-input.is-disabled')].filter(e=>e.getBoundingClientRect().width>0).length;")) != 0:
            raise TestError("记忆库开启后子项仍禁用")
        if prj().get("memory.enabled") == "true":
            raise TestError("开关拨动即落库（应改为手动保存：拨动不落库）")
        # 主保存按钮：文字 + 颜色（= var(--accent)）
        save_txt = ev(panel_js(".project-config-panel",
                               "const b=[...R.querySelectorAll('button.b-btn--primary')].filter(x=>x.getBoundingClientRect().width>0)[0];"
                               "return b?b.textContent.trim():'';"))
        if save_txt != "保存":
            raise TestError("上下文管理主保存按钮文字=%r，期望 '保存'" % save_txt)
        bg = ev(panel_js(".project-config-panel",
                         "const b=[...R.querySelectorAll('button.b-btn--primary')].filter(x=>x.getBoundingClientRect().width>0)[0];"
                         "return b?getComputedStyle(b).backgroundColor:null;"))
        if bg != css_color("var(--accent)"):
            raise TestError("主按钮背景=%s，期望 var(--accent)=%s" % (bg, css_color("var(--accent)")))
        # 点击保存 → 成功提示（颜色 = var(--success)）+ memory.enabled 落库
        # 本轮变更实测（批量写）：一次【保存】= 一次 `setConfigs` 批量写 → 后端整批只广播 **1 条**
        # `data-prj-config-refresh`（载荷带 `ids` 全组键，61 §3.1）；保存前挂监听、保存后计数。
        c.mq_on_capture(["data-prj-config-refresh"])
        if click_primary(".project-config-panel", "保存") != "ok":
            raise TestError("点击上下文保存按钮失败")
        if not wait_toast("b-message--success"):
            raise TestError("保存后未见成功提示")
        if toast_border("b-message--success") != css_color("var(--success)"):
            raise TestError("成功提示左边框=%s，期望 var(--success)=%s"
                            % (toast_border("b-message--success"), css_color("var(--success)")))
        if prj().get("memory.enabled") != "true":
            raise TestError("保存后 memory.enabled=%r，期望 'true'" % prj().get("memory.enabled"))
        # 批量写广播计数：1 次保存 → 恰好 1 条 prj-config-refresh，且载荷 `ids` 覆盖本页批量键
        c.wait_events("data-prj-config-refresh", n=1, max_wait=10, clear=False)
        time.sleep(1.0)
        refs = c.events_of("data-prj-config-refresh", clear=True)
        if len(refs) != 1:
            raise TestError("一次批量保存应只发 1 条 data-prj-config-refresh，实测 %d 条：%r"
                            % (len(refs), refs))
        _ids = (refs[0].get("payload") or {}).get("ids")
        if not (isinstance(_ids, list) and "keep_full_max_turns" in _ids
                and "memory.enabled" in _ids and "compress_token_threshold" in _ids):
            raise TestError("批量 refresh 载荷 ids 应含全组键（keep_full_max_turns/memory.enabled/"
                            "compress_token_threshold…），实测 %r" % (_ids,))
        print("[A2] 一次保存 → data-prj-config-refresh 条数=%d ids=%r" % (len(refs), _ids), flush=True)
        # 2026-09-24（D1）：三项阈值随主保存落库（读回正整数字符串）
        p = prj()
        for k in ("keep_full_max_turns", "keep_full_max_tokens", "compress_token_threshold"):
            v = (p.get(k) or "").strip()
            if not v.isdigit() or int(v) <= 0:
                raise TestError("上下文管理保存后 %s=%r，期望正整数字符串" % (k, p.get(k)))


def case_a3_index_codegraph():
    """A3 CodeGraph 索引：默认值回显（文字）+ 启用开关点击保存并回读 + 保存按钮落库 exts
    + 「排除的目录和文件」label 右侧「叠加 gitignore」勾选（勾选后输入框仍可编辑）落库。"""
    with prj_guard(["enable-codegraph", "codegraph.exts", "codegraph.skip-dirs",
                    "codegraph.stack-gitignore"]):
        open_page("settings-project", ".project-config-panel")
        click_tab("CodeGraph 索引", ".project-config-panel")
        shot("a3-codegraph.png")
        txt = panel_text(".project-config-panel")
        if ("参与索引的扩展名" not in txt or "排除的目录和文件" not in txt
                or "叠加 gitignore" not in txt):
            raise TestError("CodeGraph 索引缺「参与索引的扩展名 / 排除的目录和文件 / 叠加 gitignore」文案")
        tas = panel_textareas(".project-config-panel")
        if len(tas) < 2:
            raise TestError("CodeGraph 索引缺两个文本框：%r" % tas)
        if ".go" not in tas[0] or ".py" not in tas[0]:
            raise TestError("扩展名默认值未回显：%r" % tas[0][:120])
        if "node_modules" not in tas[1] or ".chonkpilot" not in tas[1]:
            raise TestError("排除目录默认值未回显：%r" % tas[1][:120])
        # 启用开关点击 → 保存并回读
        st = find_switch_by_label(".project-config-panel", "CodeGraph 索引")
        if not st:
            raise TestError("未找到 CodeGraph 启用开关")
        want = "false" if st["checked"] else "true"
        if click_switch_by_label(".project-config-panel", "CodeGraph 索引") != "ok":
            raise TestError("点击 CodeGraph 启用开关失败")
        if prj().get("enable-codegraph") != want:
            raise TestError("点击后 enable-codegraph=%r，期望 %r" % (prj().get("enable-codegraph"), want))
        # 保存按钮 → codegraph.exts 落库并回读
        if click_primary(".project-config-panel", "保存") != "ok":
            raise TestError("点击 CodeGraph 保存按钮失败")
        got = prj().get("codegraph.exts")
        if got != tas[0]:
            raise TestError("保存后 codegraph.exts 未按文本框值落库：%r" % (got,))
        # 叠加 gitignore：勾选 → 断言输入框仍可编辑 → 保存 → 落 codegraph.stack-gitignore=true
        st = ev(panel_js(".project-config-panel", """
const row=[...R.querySelectorAll('.form-label-row')].find(x=>{const l=x.querySelector('.form-label');return l&&l.textContent.trim()==='排除的目录和文件';});
if(!row)return 'no-row';const cb=row.querySelector('input[type=checkbox]');if(!cb)return 'no-cb';
if(!cb.checked)cb.click();
const ta=row.parentElement?row.parentElement.querySelector('textarea'):null;if(!ta)return 'no-ta';
return (cb.checked?'checked':'unchecked')+'|'+(ta.disabled||ta.readOnly?'readonly':'editable');"""))
        if st != "checked|editable":
            raise TestError("叠加 gitignore 勾选后应为「选中 + 输入框可编辑」，实际 %r" % (st,))
        if click_primary(".project-config-panel", "保存") != "ok":
            raise TestError("勾选后点击 CodeGraph 保存按钮失败")
        time.sleep(0.5)
        if prj().get("codegraph.stack-gitignore") != "true":
            raise TestError("保存后 codegraph.stack-gitignore=%r，期望 'true'"
                            % (prj().get("codegraph.stack-gitignore"),))


def case_a4_index_vfts():
    """A4 Vfts 全文索引：默认值回显 + 启用开关点击保存并回读 + 编辑后保存落库。

    2026-09-15 产品改动：VftsConfig.handleIndexSave 改为「仅写入实际改动的键」
    （未改动 → message「无改动（未写入配置）」，不写项目级键，避免把默认集固化为显式配置）；
    故先写哨兵值（模拟真实编辑）再点保存，覆盖「保存落库」路径（不放宽断言）。
    """
    with prj_guard(["enable-vfts", "vfts.exts", "vfts.skip-dirs"]):
        open_page("settings-project", ".project-config-panel")
        click_tab("Vfts 全文索引", ".project-config-panel")
        tas = panel_textareas(".project-config-panel")
        if len(tas) < 2:
            raise TestError("Vfts 索引缺两个文本框：%r" % tas)
        if ".md" not in tas[0]:
            raise TestError("Vfts 扩展名默认值未回显：%r" % tas[0][:120])
        if "node_modules" not in tas[1]:
            raise TestError("Vfts 排除目录默认值未回显：%r" % tas[1][:120])
        st = find_switch_by_label(".project-config-panel", "Vfts 全文索引")
        if not st:
            raise TestError("未找到 Vfts 启用开关")
        want = "false" if st["checked"] else "true"
        if click_switch_by_label(".project-config-panel", "Vfts 全文索引") != "ok":
            raise TestError("点击 Vfts 启用开关失败")
        if prj().get("enable-vfts") != want:
            raise TestError("点击后 enable-vfts=%r，期望 %r" % (prj().get("enable-vfts"), want))
        # 编辑扩展名文本框（哨兵值）→ 保存 → 按键值落库
        sentinel = ".md\n.txt\n.probe-a4"
        set_panel_textarea(".project-config-panel", 0, sentinel)
        if click_primary(".project-config-panel", "保存") != "ok":
            raise TestError("点击 Vfts 保存按钮失败")
        time.sleep(0.8)
        if prj().get("vfts.exts") != sentinel:
            raise TestError("保存后 vfts.exts 未按文本框值落库：%r" % (prj().get("vfts.exts"),))
        # 「重置」：清项目级键并回填默认镜像（本波新增按钮/能力）
        r = ev(panel_js(".project-config-panel",
                        "const b=[...R.querySelectorAll('button')].filter(x=>x.getBoundingClientRect().width>0)"
                        ".find(x=>x.textContent.trim()==='重置');if(!b)return 'no-btn';b.click();return 'ok';"))
        if r != "ok":
            raise TestError("Vfts「重置」按钮缺失/点击失败：%s" % r)
        time.sleep(0.8)
        if prj().get("vfts.exts") not in (None, ""):
            raise TestError("「重置」后 vfts.exts 未清除：%r" % (prj().get("vfts.exts"),))
        if ".md" not in (panel_textareas(".project-config-panel") or [""])[0]:
            raise TestError("「重置」后文本框未回填默认镜像")


# ══════════════════════════════════════════════════════════
# B. 用户设置页
# ══════════════════════════════════════════════════════════

def case_b1_llm_list_params():
    """B1 LLM 配置：2 页签（一览 / 默认模型）+ 一览表头文字 + 默认模型（主对话 + 5 子系统）
    + 添加弹窗字段文字/位置/一行两列 + 保存落库。

    2026-09-26 用户口径：LLM 页改 **2 页签** —— ① 一览（provider 清单，操作列仅编辑/删除）；
    ② 默认模型（**主对话** + 5 个子系统下拉）。原「只读内置行（系统默认（启动参数））+
    「设为默认」」**整体移除**（`gui.system.builtins` 不再下发 `builtinLLMs`；聊天输入框选择器
    只列 usr providers）。
    """
    name = "ui-llm-%d" % int(time.time())
    snap = _h.snapshot_user_config(c, ["llms", "defaultLLM"])
    try:
        open_page("settings-llm", ".settings-page")
        shot("b1-llm.png")
        # ① 2 页签
        labels = tab_labels(".settings-page")
        if labels != ["一览", "默认模型"]:
            raise TestError("LLM 页签应为 ['一览','默认模型']，实际：%r" % labels)
        # ② 一览：表头/工具栏；不得再有只读行与「设为默认」
        txt = panel_text(".settings-page")
        for want in ("添加 LLM", "名称", "模型", "最大工具迭代", "操作"):
            if want not in txt:
                raise TestError("LLM 一览页缺文字 %r" % want)
        if "系统默认（启动参数）" in txt or "设为默认" in txt or "系统内置" in txt:
            raise TestError("LLM 一览页不应再有只读行/设为默认：%r" % txt)
        for r in llm_rows():
            if set(r["btns"]) - {"编辑", "删除"}:
                raise TestError("一览行操作应仅编辑/删除：%r" % r)
        # ③ 默认模型页签：主对话 + 5 子系统
        click_tab("默认模型", ".settings-page")
        dtxt = panel_text(".settings-page")
        for want in ("主对话", "提示词优化", "记忆系统", "压缩上下文", "分析系统", "决策系统"):
            if want not in dtxt:
                raise TestError("默认模型页缺 %r（实际 %r）" % (want, dtxt))
        # 回到一览，继续弹窗/保存断言
        click_tab("一览", ".settings-page")
        # 添加弹窗
        c.mq_emit("config-add-llm")
        if not wait_vis(".dialog-shell"):
            raise TestError("LLM 编辑弹窗未打开")
        time.sleep(0.5)
        labs = dialog_labels()
        # 2026-09-26：去掉「推理强度」label（下拉保留）→ 从字段清单移除该项
        for want in ("名称", "模型", "接口地址", "API 密钥", "温度", "最大输出 Token",
                     "上下文窗口", "模型能力", "思考模式", "最大工具迭代"):
            if want not in labs:
                raise TestError("LLM 弹窗缺字段 %r（实际 %r）" % (want, labs))
        if "推理强度" in labs:
            raise TestError("LLM 弹窗不应再有「推理强度」label：%r" % (labs,))
        # 位置：弹窗在视口内
        assert_in_viewport(".dialog-shell", "LLM 弹窗")
        # 位置：一行两列（前两个 form-item-12 同 top、左右并列）
        lay = ev("(function(){const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
                 "const its=[...R.querySelectorAll('.form-item-12')].filter(e=>e.getBoundingClientRect().width>0).slice(0,2);"
                 "return its.map(e=>{const r=e.getBoundingClientRect();return {t:Math.round(r.top),l:Math.round(r.left),w:Math.round(r.width)};});})()")
        if len(lay) < 2 or abs(lay[0]["t"] - lay[1]["t"]) > 2 or lay[1]["l"] <= lay[0]["l"]:
            raise TestError("LLM 弹窗未按一行两列排版：%r" % (lay,))
        # 填表保存 → 回读
        fill_dialog(["名称", "Name"], name)
        fill_dialog(["模型", "Model"], "ui-model-x")
        c.mq_emit("edit-llm-save")
        time.sleep(1.0)
        hit = [l for l in (ucfg().get("llms") or []) if l.get("name") == name]
        if not hit or hit[0].get("model") != "ui-model-x":
            raise TestError("LLM 保存未落库/字段不符：%r" % (hit,))
        # 列表即时展示
        if name not in panel_text(".settings-page"):
            raise TestError("保存后列表未展示 %s" % name)
    finally:
        try:
            c.mq_emit("edit-llm-cancel")
            time.sleep(0.3)
        except Exception:
            pass
        _h.restore_user_config(c, snap)  # 还原 usr llms/defaultLLM（缺省 → 删键 / 清集合）


def _set_dialog_select(value):
    r = ev("(function(){const s=[...document.querySelectorAll('.dialog-shell select.b-select__native')]"
           ".find(e=>e.getBoundingClientRect().width>0);if(!s)return 'no-select';"
           "Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(s,%s);"
           "s.dispatchEvent(new Event('change',{bubbles:true}));return s.value;})()" % json.dumps(value))
    time.sleep(0.6)
    return r


def _dialog_has_label(cands):
    labs = dialog_labels()
    return any(any(cc in l for cc in cands) for l in labs)


def case_b2_mcp_transport_branching():
    """B2 MCP：transport 与 url / runtime 的从属显示（模拟 Select 切换）+ 保存 runtime/args 回读。

    2026-09-27 起弹窗为「基本信息 / 运行信息 / 工具」三页签 → 字段断言按页签分别进行
    （Tabs 只渲染当前页签 → 先切页签再断言/填表；保存载荷仍为三页全量）；
    启动参数为行编辑器（KeyValueEditor，list 模式）→ 逐行填。

    transport 从属显示（EditMCPDialog.vue）：
      * 服务地址（url）：`showUrl = transport !== 'stdio'`（EditMCPDialog.vue:205）→ stdio 隐藏，auto/http/sse 显示；
      * 运行时（runtime）：`showSpawn = transport !== 'http' && transport !== 'sse'`（EditMCPDialog.vue:206）
        → http/sse 隐藏，auto/stdio 显示；
      * 启动参数（args）：**不受 transport 门控**（表单项 EditMCPDialog.vue:86-96 无 `v-if`）
        → 三种 transport 下均显示（对齐 36-配置 CFG-004-S06：仅「服务地址（仅非 stdio）」标从属）。
    """
    name = "ui_mcp_%d" % int(time.time())
    snap = _h.snapshot_user_config(c, ["mcpServers"])
    try:
        open_page("settings-mcp", ".settings-page")
        if "添加 MCP Server" not in panel_text(".settings-page"):
            raise TestError("MCP 页缺「添加 MCP Server」")
        c.mq_emit("config-add-mcp")
        if not wait_vis(".dialog-shell"):
            raise TestError("MCP 编辑弹窗未打开")
        time.sleep(0.5)
        # 三页签头就位（2026-09-27：新增「工具」页签）
        if tab_labels(".dialog-shell") != ["基本信息", "运行信息", "工具"]:
            raise TestError("MCP 弹窗页签应为 ['基本信息','运行信息','工具']，实际 %r" % (tab_labels(".dialog-shell"),))
        shot("b2-mcp.png")
        assert_in_viewport(".dialog-shell", "MCP 弹窗")
        # 默认（基本信息）：auto 态显示 url
        if not _dialog_has_label(["服务地址"]):
            raise TestError("MCP 弹窗 auto 态基本信息页应显示 url")
        # 运行信息：auto 态显示 runtime/args
        click_tab("运行信息", ".dialog-shell")
        if not (_dialog_has_label(["运行时"]) and _dialog_has_label(["启动参数"])):
            raise TestError("MCP 弹窗 auto 态运行信息页应显示 runtime/args")
        # stdio：url 隐藏（基本信息页），runtime/args 显示（运行信息页）
        click_tab("基本信息", ".dialog-shell")
        _set_dialog_select("stdio")
        if _dialog_has_label(["服务地址"]):
            raise TestError("transport=stdio 时 url 字段应隐藏")
        click_tab("运行信息", ".dialog-shell")
        if not (_dialog_has_label(["运行时"]) and _dialog_has_label(["启动参数"])):
            raise TestError("transport=stdio 时应显示 runtime/args")
        # http：url 显示（基本信息页）；运行信息页「运行时」隐藏（showSpawn=false），
        # 「启动参数」**不受 transport 门控**（EditMCPDialog.vue:206 showSpawn 只 gate `运行时`；
        # args 表单项 EditMCPDialog.vue:86-96 无 v-if）→ http 下仍显示。
        click_tab("基本信息", ".dialog-shell")
        _set_dialog_select("http")
        if not _dialog_has_label(["服务地址"]):
            raise TestError("transport=http 时应显示 url")
        click_tab("运行信息", ".dialog-shell")
        labs = dialog_labels()
        if _dialog_has_label(["运行时"]):
            raise TestError("transport=http 时「运行时」应隐藏（showSpawn=false），实际 labels=%r" % labs)
        if not _dialog_has_label(["启动参数"]):
            raise TestError("transport=http 时「启动参数」应显示（args 不受 transport 门控），实际 labels=%r" % labs)
        # auto 保存 runtime+args（名称/transport 在基本信息页；runtime/args 在运行信息页）
        click_tab("基本信息", ".dialog-shell")
        _set_dialog_select("auto")
        fill_dialog(["名称", "Name"], name)
        click_tab("运行信息", ".dialog-shell")
        fill_dialog(["运行时", "Runtime"], sys.executable or "python")
        fill_list_editor(["启动参数", "Args"], ["--demo", "value 1"])  # list 行编辑：逐行一个参数
        c.mq_emit("edit-mcp-save")
        time.sleep(1.0)
        hit = [m for m in (ucfg().get("mcpServers") or []) if m.get("name") == name]
        if not hit:
            raise TestError("MCP 保存未落库：%r" % ((ucfg().get("mcpServers") or []),))
        if not hit[0].get("runtime") or hit[0].get("args") != ["--demo", "value 1"]:
            raise TestError("MCP runtime/args 未按规则落库（逐个参数不切分）：%r" % hit[0])
    finally:
        try:
            c.mq_emit("edit-mcp-cancel")
            time.sleep(0.3)
        except Exception:
            pass
        _h.restore_user_config(c, snap)  # 还原 usr mcpServers（缺省 → 清集合）


def case_b3_paths_toolchain():
    """B3 路径/工具链：系统页 Chrome 已探测（路径非空 + 版本 x.y.z.w）；用户页含 Chrome；项目页不含。"""
    open_page("settings-paths", ".settings-page")
    shot("b3-paths.png")
    labels = tab_labels(".settings-page")
    if labels != ["系统", "用户", "项目"]:
        raise TestError("路径页签文字不符：%r" % labels)
    # 系统页：Chrome 行
    click_tab("系统", ".settings-page")
    info = ev(panel_js(".settings-page", """
const rows=[...R.querySelectorAll('.path-row')].map(r=>({
  name:(r.querySelector('.path-label')||{}).textContent ? r.querySelector('.path-label').textContent.trim():'',
  path:(r.querySelector('.mono')||{}).textContent||'',
  ver:(r.querySelector('.version')||{}).textContent||''}));
return rows;""")) or []
    chrome = next((r for r in info if r["name"] == "Chrome"), None)
    if not chrome:
        raise TestError("系统页无 Chrome 行：%r" % [r["name"] for r in info])
    if not chrome["path"].strip():
        raise TestError("系统页 Chrome 路径为空（未被探测出）")
    import re as _re
    if not _re.match(r"^\d+(\.\d+){2,4}$", chrome["ver"].strip()):
        raise TestError("系统页 Chrome 版本=%r，期望 x.y.z(.w)" % chrome["ver"])
    # 用户页：Chrome 可编辑输入
    click_tab("用户", ".settings-page")
    urows = ev(panel_js(".settings-page",
               "return [...R.querySelectorAll('.path-row')].map(r=>({"
               "name:((r.querySelector('.path-label')||{}).textContent||'').trim(),"
               "hasInput:!!r.querySelector('input.b-input')}));")) or []
    uchrome = next((r for r in urows if r["name"].startswith("Chrome")), None)
    if not uchrome or not uchrome["hasInput"]:
        raise TestError("用户页 Chrome 应可编辑（含输入框）：%r" % urows)
    # 项目页：不含 Chrome
    click_tab("项目", ".settings-page")
    prows = ev(panel_js(".settings-page",
               "return [...R.querySelectorAll('.path-row')].map(r=>"
               "((r.querySelector('.path-label')||{}).textContent||'').trim());")) or []
    if any(p.startswith("Chrome") for p in prows):
        raise TestError("项目页不应出现 Chrome（Chrome 仅系统/用户两级）：%r" % prows)


# ══════════════════════════════════════════════════════════
# C. 已删除项确不存在
# ══════════════════════════════════════════════════════════

def case_c_removed_items():
    """C 旧配置弹窗 / 独立「提示词」页签 确不存在。"""
    if c.exists(".config-dialog-body-scroll").get("count", 0) > 0:
        raise TestError("旧配置弹窗 .config-dialog-body-scroll 仍存在（应已移除）")
    open_page("settings-project", ".project-config-panel")
    labels = tab_labels(".project-config-panel")
    if any(("提示词" in x or "prompt" in x.lower()) for x in labels):
        raise TestError("项目设置仍含独立「提示词」页签：%r" % labels)


# ══════════════════════════════════════════════════════════
# E. 记忆库类别增删 + 提示词编辑（本波重点：41 I-66；2026-09-26 双编辑入口）
#    E1 「新增类别」→ 出现在清单 + 内容编辑**弹框**（TextEditDialog）保存即关（回读 data-memory-read）
#    E2 行内【编辑内容】→ 弹框内容正确/取消不落库 + 「删除类别」仅自定义可见 + 二次确认后消失
#    E3 data-memory-delete 直调 → {ok,id} + 广播 data-memory-refresh(op=delete)（贴原始 payload）
#    E4 行内【编辑提示词】→ 弹框（内置默认回填/来源提示/保存落 prj memory.prompt.<类别>/重置回落）
#    F  设置页无「MCPServerConfig」死项（源：I-64 已删死结构）
# ══════════════════════════════════════════════════════════

PRESET_MEMORY_CATEGORIES = ["项目概要", "共同库", "开发规范", "构建发布规则",
                            "接口库", "测试规范", "典型参照", "用户决策"]


def poll(fn, max_wait=8, interval=0.3):
    end = time.time() + max_wait
    while time.time() < end:
        try:
            if fn():
                return True
        except Exception:
            pass
        time.sleep(interval)
    return False


def mem_rows():
    """记忆类别表行：[{cat, hasPrompt, hasEdit, hasDel}]（2026-09-26：每行两个编辑入口）。
    2026-09-27：原生表 → 自研 Table 组件（.b-table）。"""
    return ev(panel_js(".project-config-panel", """
const rows=[...R.querySelectorAll('.b-table tbody tr')];
return rows.map(r=>{const tds=[...r.querySelectorAll('td')];
  const sp=tds[0]?tds[0].querySelector('span'):null;
  const cat=sp?sp.textContent.trim():(tds[0]?tds[0].innerText.trim():'');
  const btns=[...r.querySelectorAll('button')].map(b=>b.textContent.trim());
  return {cat:cat, hasPrompt:btns.includes('编辑提示词'),
          hasEdit:btns.includes('编辑内容'), hasDel:btns.includes('删除')};});""")) or []


def mem_ensure_ui(name):
    """点「新增类别」→ promptInput 弹窗填名 → 确认；等待行出现。"""
    r = ev(panel_js(".project-config-panel",
                    "const b=[...R.querySelectorAll('.mem-table-head button')].find(x=>x.textContent.trim()==='新增类别');"
                    "if(!b)return 'no-btn';b.click();return 'ok';"))
    if r != "ok":
        raise TestError("未找到「新增类别」按钮：%s" % r)
    if not wait_vis(".dialog-shell", 6):
        raise TestError("「新增类别」输入弹窗未打开")
    time.sleep(0.4)
    fr = ev("(function(){const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
            "if(!R)return 'no-dialog';const inp=R.querySelector('.dialog-body input.b-input');if(!inp)return 'no-input';"
            "Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(inp,%s);"
            "inp.dispatchEvent(new Event('input',{bubbles:true}));"
            "inp.dispatchEvent(new Event('change',{bubbles:true}));return 'ok';})()" % json.dumps(name))
    if fr != "ok":
        raise TestError("「新增类别」弹窗填名失败：%s" % fr)
    cr = ev("(function(){const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
            "if(!R)return 'no-dialog';const b=[...R.querySelectorAll('.dialog-body button.b-btn--primary')]"
            ".find(x=>x.getBoundingClientRect().width>0);if(!b)return 'no-confirm';b.click();return 'ok';})()")
    if cr != "ok":
        raise TestError("「新增类别」确认点击失败：%s" % cr)
    if not poll(lambda: any(r["cat"] == name for r in mem_rows()), 8):
        raise TestError("新增类别 %r 未出现在清单：%r" % (name, [r["cat"] for r in mem_rows()]))


def click_row_btn(name, label):
    """点记忆类别清单中「类别名 = name」行内文字为 label 的按钮；成功 → True。"""
    r = ev(panel_js(".project-config-panel", """
const rows=[...R.querySelectorAll('.b-table tbody tr')];
const row=rows.find(r=>{const td=r.querySelector('td');const sp=td?td.querySelector('span'):null;
  return ((sp?sp.textContent:(td?td.innerText:''))||'').trim()===%s;});
if(!row)return 'no-row';
const b=[...row.querySelectorAll('button')].find(x=>x.textContent.trim()===%s);
if(!b)return 'no-btn';b.click();return 'ok';""" % (json.dumps(name), json.dumps(label))))
    return r == "ok"


def click_userpref_btn(label):
    """点「用户偏好」行（唯一带 `.mem-token` 的 .switch-row）文字为 label 的按钮；成功 → True。"""
    r = ev(panel_js(".project-config-panel", """
const row=[...R.querySelectorAll('.switch-row')].find(x=>x.querySelector('.mem-token'));
if(!row)return 'no-row';
const b=[...row.querySelectorAll('button')].find(x=>x.textContent.trim()===%s);
if(!b)return 'no-btn';b.click();return 'ok';""" % json.dumps(label)))
    return r == "ok"


def mem_delete_row(name):
    """点某自定义行「删除」→ 二次确认（断言文案）→ 确认后行消失。"""
    if not click_row_btn(name, "删除"):
        raise TestError("点击类别 %r 删除失败（行或删除按钮缺失）" % name)
    if not wait_vis(".dialog-shell", 6):
        raise TestError("删除类别的二次确认弹窗未打开")
    time.sleep(0.4)
    body = ev("(function(){const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
              "return R?R.querySelector('.dialog-body').innerText:'';})()") or ""
    if "删除类别" not in body or name not in body:
        raise TestError("删除确认文案不符（应含「删除类别」+ 类别名）：%r" % body[:120])
    cr = ev("(function(){const R=[...document.querySelectorAll('.dialog-shell')].find(e=>e.getBoundingClientRect().width>0);"
            "if(!R)return 'no-dialog';const b=[...R.querySelectorAll('.dialog-body button.b-btn--primary')]"
            ".find(x=>x.getBoundingClientRect().width>0);if(!b)return 'no-confirm';b.click();return 'ok';})()")
    if cr != "ok":
        raise TestError("删除二次确认点击失败：%s" % cr)
    if not poll(lambda: not any(r["cat"] == name for r in mem_rows()), 8):
        raise TestError("删除确认后类别 %r 仍在清单" % name)


def _mem_cleanup(name):
    try:
        c.req("data-memory-delete", {"data": {"category": name}})
    except Exception:
        pass


# ── 内容编辑弹框（TextEditDialog）操作助手 ──────────────────
# 2026-09-24：记忆内容编辑已由「页内联编辑器」迁为**弹框**（useMemoryCategories.showContentEditor
# → TextEditDialog，bodyClass = `text-edit-dialog-body`）→ 本套件 E1/E2 按弹框口径断言，
# 与 run_memory_ctx UI-7 同口径（选点 = `.dialog-shell` 内含 `.text-edit-dialog-body` 的可见弹框）。

TE_DLG = ".text-edit-body"


def dlg_js(inner):
    """在**可见的内容编辑弹框**内执行 inner（内嵌 R）；非该弹框 → 返回 None。"""
    return ("(function(){const R=[...document.querySelectorAll('.dialog-shell')]"
            ".find(e=>e.getBoundingClientRect().width>0&&e.querySelector('.text-edit-dialog-body'));"
            "if(!R)return null;%s})()" % inner)


def wait_gone(sel, max_wait=10):
    end = time.time() + max_wait
    while time.time() < end:
        if vcount(sel) == 0:
            return True
        time.sleep(0.3)
    return False


def dlg_title():
    return ev(dlg_js("const t=R.querySelector('.dialog-title');return t?t.textContent.trim():'';")) or ""


def dlg_textarea_value():
    return ev(dlg_js("const t=R.querySelector('.text-edit-dialog-body textarea');return t?t.value:null;"))


def dlg_set_textarea(value):
    return ev(dlg_js("const t=R.querySelector('.text-edit-dialog-body textarea');if(!t)return 'no-ta';"
                     "Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(t,%s);"
                     "t.dispatchEvent(new Event('input',{bubbles:true}));return 'ok';" % json.dumps(value)))


def dlg_btn(label):
    """点可见内容编辑弹框内文字为 label 的可见按钮（保存 / 取消 / 优化 / 重置）。"""
    return ev(dlg_js("const b=[...R.querySelectorAll('button')].filter(x=>x.getBoundingClientRect().width>0)"
                     ".find(x=>x.textContent.trim()===%s);if(!b)return 'no-btn';b.click();return 'ok';"
                     % json.dumps(label)))


def dlg_has_btn(label):
    """可见文本编辑弹框内是否存在文字为 label 的可见按钮（**不点击**）。"""
    return ev(dlg_js("return [...R.querySelectorAll('button')].some(x=>x.getBoundingClientRect().width>0"
                     "&&x.textContent.trim()===%s);" % json.dumps(label))) is True


def dlg_hint():
    """可见文本编辑弹框内的来源/口径提示（`.text-edit-hint`；无 → None）。"""
    return ev(dlg_js("const h=R.querySelector('.text-edit-hint');return h?h.textContent.trim():null;"))


def close_stray_dialogs():
    """关闭任何残留可见弹框（点 X），避免上一用例弹框外溢到下一用例（E1 → E2 级联）。"""
    for _ in range(3):
        if vcount(".dialog-shell") == 0:
            return
        r = ev("(function(){const R=[...document.querySelectorAll('.dialog-shell')]"
               ".find(e=>e.getBoundingClientRect().width>0);if(!R)return 'no';"
               "const b=R.querySelector('.dialog-btn-close');if(!b)return 'no-btn';"
               "b.dispatchEvent(new MouseEvent('click',{bubbles:true}));return 'ok';})()")
        if r != "ok":
            return
        time.sleep(0.4)


def case_e1_memory_add_and_edit():
    """E1 记忆库「新增类别」→ 新类别出现 + **内容编辑弹框**（TextEditDialog）内保存
    → 保存即关 + data-memory-read 回读（2026-09-24 弹框化；与 run_memory_ctx UI-7 同口径）。
    """
    name = "ui-mem-%d" % int(time.time())
    content = "ui 记忆内容 %d\n第二行" % int(time.time())
    snap = _h.snapshot_prj_config(c, ["memory.enabled"])
    try:
        close_stray_dialogs()
        prj_save("memory.enabled", "true")  # 新增入口需记忆库启用
        open_page("settings-project", ".project-config-panel")
        click_tab("上下文管理", ".project-config-panel")
        shot("e1-memory-before.png")
        # 预置类别在列（8 项）
        cats = [r["cat"] for r in mem_rows()]
        for p in PRESET_MEMORY_CATEGORIES:
            if p not in cats:
                raise TestError("预置类别缺失：%r（实际 %r）" % (p, cats))
        # 新增（点「新增类别」→ 填名 → 确认）→ 类别入清单 + 自动打开内容编辑弹框
        mem_ensure_ui(name)
        shot("e1-memory-added.png")
        row = next((r for r in mem_rows() if r["cat"] == name), None)
        if not row:
            raise TestError("新类别未出现在清单")
        # 2026-09-26：每类别行并存两个编辑入口（提示词 / 内容）
        if not (row["hasPrompt"] and row["hasEdit"]):
            raise TestError("类别行须并存【编辑提示词】与【编辑内容】：%r" % row)
        # 弹框已开（标题 = 类别名 → 证明开的是该类别的内容编辑弹框）
        if not wait_vis(TE_DLG, 10):
            raise TestError("新增后未弹出内容编辑弹框（TextEditDialog）")
        title = dlg_title()
        if title != name:
            raise TestError("内容编辑弹框标题=%r，期望类别名 %r" % (title, name))
        # 新类别默认空内容
        ta0 = dlg_textarea_value()
        if ta0 != "":
            raise TestError("新类别弹框内容应为空：%r" % ta0)
        # 写入内容 → 点弹框【保存】→ **保存即关**
        sr = dlg_set_textarea(content)
        if sr != "ok":
            raise TestError("写入弹框内容失败：%s" % sr)
        cr2 = dlg_btn("保存")
        if cr2 != "ok":
            raise TestError("点击弹框【保存】失败：%s" % cr2)
        if not wait_gone(TE_DLG, 10):
            raise TestError("保存后内容编辑弹框未自动关闭")
        # 回读落库（内容正确 + 类别级别）
        rr = c.req("data-memory-read", {"data": {"category": name}})
        d = (rr or {}).get("data") or {}
        if d.get("content") != content:
            raise TestError("类别内容落库不符：got=%r want=%r" % ((d.get("content") or "")[:80], content[:80]))
        if d.get("level") != "project":
            raise TestError("新建类别应为 project 级：%r" % d.get("level"))
    finally:
        close_stray_dialogs()   # 先关弹框，再删类别（避免残留弹框外溢影响 E2）
        _mem_cleanup(name)
        _h.restore_prj_config(c, snap)  # 还原 prj memory.enabled（原本无该键 → 删除）


def case_e2_memory_delete_ui():
    """E2 自定义类别：行内【编辑】→ 内容编辑弹框（内容正确 + 取消不落库）+【删除】仅自定义可见
    + 二次确认弹框后消失（预置类别无删除入口）（2026-09-24 弹框口径；与 UI-7 同口径）。
    """
    name = "ui-mem-del-%d" % int(time.time())
    seed = "临时"
    snap = _h.snapshot_prj_config(c, ["memory.enabled"])
    try:
        close_stray_dialogs()
        prj_save("memory.enabled", "true")
        c.req("data-memory-save", {"data": {"category": name, "content": seed}})
        open_page("settings-project", ".project-config-panel")
        click_tab("上下文管理", ".project-config-panel")
        rows = mem_rows()
        # 预置类别：两个编辑入口齐备、无删除
        for p in PRESET_MEMORY_CATEGORIES:
            row = next((r for r in rows if r["cat"] == p), None)
            if not row:
                raise TestError("预置类别缺失：%r" % p)
            if not (row["hasPrompt"] and row["hasEdit"]):
                raise TestError("预置类别 %r 缺编辑入口：%r" % (p, row))
            if row["hasDel"]:
                raise TestError("预置类别 %r 不应有删除入口" % p)
        # 自定义类别：两个编辑入口 + 删除
        crow = next((r for r in rows if r["cat"] == name), None)
        if not crow:
            raise TestError("自定义类别 %r 未出现（清单 %r）" % (name, [r["cat"] for r in rows]))
        if not (crow["hasPrompt"] and crow["hasEdit"]):
            raise TestError("自定义类别 %r 缺编辑入口：%r" % (name, crow))
        if not crow["hasDel"]:
            raise TestError("自定义类别 %r 应有删除入口" % name)
        # 弹框口径复核：点该行【编辑内容】→ 弹框（标题 = 类别名 + 内容 = 已存内容）→【取消】不落库
        if not click_row_btn(name, "编辑内容"):
            raise TestError("类别 %r 行缺【编辑内容】按钮" % name)
        if not wait_vis(TE_DLG, 10):
            raise TestError("点【编辑内容】未弹出内容编辑弹框（TextEditDialog）")
        title = dlg_title()
        if title != name:
            raise TestError("内容编辑弹框标题=%r，期望类别名 %r" % (title, name))
        got = dlg_textarea_value()
        if (got or "").strip() != seed:
            raise TestError("弹框内容与已存内容不符：got=%r want=%r" % (got, seed))
        if dlg_btn("取消") != "ok":
            raise TestError("内容编辑弹框缺【取消】按钮")
        if not wait_gone(TE_DLG, 10):
            raise TestError("点【取消】后弹框未关闭")
        rr = c.req("data-memory-read", {"data": {"category": name}})
        if ((rr or {}).get("data") or {}).get("content", "").strip() != seed:
            raise TestError("【取消】不应落库（内容已变）")
        shot("e2-memory-delete-before.png")
        mem_delete_row(name)
        shot("e2-memory-delete-after.png")
        if any(r["cat"] == name for r in mem_rows()):
            raise TestError("删除后类别 %r 仍在清单" % name)
    finally:
        close_stray_dialogs()
        _mem_cleanup(name)
        _h.restore_prj_config(c, snap)  # 还原 prj memory.enabled（原本无该键 → 删除）


def case_e3_memory_delete_datamsg():
    """E3 data-memory-delete 直调：返回 {ok,id} + 广播 data-memory-refresh(op=delete)（贴原始 payload）。"""
    name = "ui-mem-msg-%d" % int(time.time())
    c.mq_on_capture(["data-memory-refresh"])
    c.req("data-memory-save", {"data": {"category": name, "content": "seed"}})
    time.sleep(0.5)
    c.events_of("data-memory-refresh", clear=True)  # 丢弃 save 广播，只留 delete 后事件
    r = c.req("data-memory-delete", {"data": {"category": name}})
    print("    [E3] data-memory-delete 回执: " + json.dumps(r, ensure_ascii=False))
    if not isinstance(r, dict) or r.get("ok") is not True or r.get("id") != name:
        raise TestError("data-memory-delete 回执非 {ok,id}：%r" % (r,))
    evs = c.wait_events("data-memory-refresh", 1, max_wait=10)
    print("    [E3] data-memory-refresh 原始事件: " + json.dumps(evs[0], ensure_ascii=False))
    payload = (evs[0] or {}).get("payload") or {}
    if payload.get("op") != "delete" or payload.get("id") != name:
        raise TestError("删除后广播非 op=delete/id=类别名：%r" % payload)
    # 清单已消失
    lst = c.req("data-memory-list", {})
    if any((e or {}).get("category") == name for e in (lst.get("list") or [])):
        raise TestError("删除后清单仍含 %r" % name)
    # 预置类别拒绝删除
    try:
        c.req("data-memory-delete", {"data": {"category": "项目概要"}})
        raise TestError("预置类别删除应被拒（却成功）")
    except TestError as e:
        if "preset" not in str(e):
            raise TestError("预置类别删除报错信息异常：%s" % e)


def case_e4_memory_prompt_edit():
    """E4 记忆类别沉淀提示词（2026-09-26 用户口径：每类别「提示词编辑」+「内容编辑」两个弹框）：

    行内【编辑提示词】→ 弹框（复用 TextEditDialog）
      · 未自定义 → 回填**内置默认** + 来源提示「当前为内置默认」、**无**【重置】；
      → 改内容 →【保存】→ 弹框自动关闭 → 回读 prj `memory.prompt.<类别名>` == 自定义值；
      → 再次打开 → 来源提示「当前为自定义」+【重置】+ 回填自定义值；
      →【重置】→ 弹框关闭 → **prj 键被清除**（回落内置默认）→ 再开显示「当前为内置默认」。

    「用户偏好」（唯一 user 级类别，用户口径明示「包括用户偏好」）同口径，落 **usr 自由键
    `memory_prompts`**（JSON 对象字符串）；重置 → usr 键被清除。
    """
    cat = PRESET_MEMORY_CATEGORIES[0]  # 项目概要（预置项目级类别，恒在清单）
    key = "memory.prompt." + cat
    sentinel = "L4-mem-prompt-%d" % int(time.time())
    snap = _h.snapshot_prj_config(c, ["memory.enabled", key])
    with _h.user_config_guard(c, ["memory_prompts"]):
        try:
            close_stray_dialogs()
            prj_save("memory.enabled", "true")
            open_page("settings-project", ".project-config-panel")
            click_tab("上下文管理", ".project-config-panel")
            # ① 行内两个编辑入口并存
            row = next((r for r in mem_rows() if r["cat"] == cat), None)
            if not row:
                raise TestError("预置类别 %r 未渲染" % cat)
            if not (row["hasPrompt"] and row["hasEdit"]):
                raise TestError("类别行须并存【编辑提示词】/【编辑内容】：%r" % row)
            # ② 打开提示词弹框：未自定义 → 回填内置默认 + 来源提示「内置默认」+ 无【重置】
            if not click_row_btn(cat, "编辑提示词"):
                raise TestError("点击【编辑提示词】失败（行/按钮缺失）")
            if not wait_vis(TE_DLG, 10):
                raise TestError("提示词编辑弹框未打开（TextEditDialog）")
            hint0 = dlg_hint() or ""
            if "内置默认" not in hint0:
                raise TestError("未自定义应显示「当前为内置默认」来源提示：%r" % hint0)
            if not (dlg_textarea_value() or "").strip():
                raise TestError("未自定义应回填内置默认提示词（内容为空）")
            if dlg_has_btn("重置"):
                raise TestError("未自定义不应显示【重置】（无可清除项）")
            # ③ 改内容 → 保存 → 弹框自动关闭
            if dlg_set_textarea(sentinel) != "ok":
                raise TestError("写入提示词弹框失败")
            if dlg_btn("保存") != "ok":
                raise TestError("提示词弹框缺【保存】按钮")
            if not wait_gone(TE_DLG, 10):
                raise TestError("保存后提示词弹框未自动关闭")
            # ④ 回读：prj `memory.prompt.<类别名>` == 自定义值
            got = (c.req("data-prj-config-load", {"id": key}) or {}).get("data")
            if got != sentinel:
                raise TestError("自定义提示词未落库 prj 键：got=%r want=%r" % (got, sentinel))
            print("    [E4] prj %s = %r" % (key, got))
            # ⑤ 再次打开：来源提示「自定义」+【重置】+ 内容 = 自定义值
            if not click_row_btn(cat, "编辑提示词"):
                raise TestError("再次点击【编辑提示词】失败")
            if not wait_vis(TE_DLG, 10):
                raise TestError("提示词弹框未再次打开")
            hint1 = dlg_hint() or ""
            if "自定义" not in hint1:
                raise TestError("已自定义应显示「当前为自定义」来源提示：%r" % hint1)
            if (dlg_textarea_value() or "") != sentinel:
                raise TestError("已自定义应回填自定义值：%r" % dlg_textarea_value())
            if not dlg_has_btn("重置"):
                raise TestError("已自定义须显示【重置】按钮")
            # ⑥ 重置 → 弹框关闭 → prj 键被清除（回落内置默认）
            if dlg_btn("重置") != "ok":
                raise TestError("点击【重置】失败")
            if not wait_gone(TE_DLG, 10):
                raise TestError("重置后提示词弹框未关闭")
            if not poll(lambda: ((c.req("data-prj-config-load", {"id": key}) or {}).get("data") or "") == "", 8):
                raise TestError("重置后 prj 键未清除：%r"
                                % (c.req("data-prj-config-load", {"id": key})))
            # ⑦ 再开 → 回落内置默认（来源提示 + 内容非空 + 无【重置】）
            if not click_row_btn(cat, "编辑提示词"):
                raise TestError("重置后【编辑提示词】不可用")
            if not wait_vis(TE_DLG, 10):
                raise TestError("重置后提示词弹框未打开")
            hint2 = dlg_hint() or ""
            if "内置默认" not in hint2:
                raise TestError("重置后应回落「当前为内置默认」：%r" % hint2)
            if not (dlg_textarea_value() or "").strip():
                raise TestError("重置后应回填内置默认提示词")
            shot("e4-memory-prompt.png")
            # 收尾：关闭本弹框（否则 ⑧ 的用户偏好弹框会被 dlg_* 助手取到「首个」旧弹框）
            if dlg_btn("取消") != "ok" or not wait_gone(TE_DLG, 6):
                raise TestError("关闭项目类别提示词弹框失败")

            # ⑧ 「用户偏好」（唯一 user 级）同口径 → 落 usr 自由键 `memory_prompts`
            up_key = "memory_prompts"
            c.req("data-user-config-delete", {"id": up_key})  # 归零：确保从「未自定义」起步
            time.sleep(0.8)
            if not click_userpref_btn("编辑提示词"):
                raise TestError("用户偏好行缺【编辑提示词】入口")
            if not wait_vis(TE_DLG, 10):
                raise TestError("用户偏好提示词弹框未打开")
            if "内置默认" not in (dlg_hint() or ""):
                raise TestError("用户偏好未自定义应显示「当前为内置默认」：%r" % dlg_hint())
            if dlg_set_textarea(sentinel) != "ok" or dlg_btn("保存") != "ok":
                raise TestError("用户偏好提示词保存失败")
            if not wait_gone(TE_DLG, 10):
                raise TestError("用户偏好提示词弹框未自动关闭")
            raw = ((c.req("data-user-config-load", {}) or {}).get("data") or {}).get(up_key)
            if not isinstance(raw, str) or sentinel not in raw:
                raise TestError("用户偏好提示词未落 usr 自由键：%r" % (raw,))
            print("    [E4] usr %s = %r" % (up_key, raw))
            # 重置 → usr 键被清除（回落内置默认）
            if not click_userpref_btn("编辑提示词"):
                raise TestError("用户偏好【编辑提示词】再次打开失败")
            if not wait_vis(TE_DLG, 10):
                raise TestError("用户偏好提示词弹框未再次打开")
            if not dlg_has_btn("重置"):
                raise TestError("用户偏好已自定义须显示【重置】")
            if dlg_btn("重置") != "ok":
                raise TestError("用户偏好【重置】点击失败")
            if not wait_gone(TE_DLG, 10):
                raise TestError("用户偏好重置后弹框未关闭")
            if not poll(lambda: (((c.req("data-user-config-load", {}) or {}).get("data") or {}).get(up_key) or "") == "", 8):
                raise TestError("用户偏好重置后 usr 键未清除：%r"
                                % ((c.req("data-user-config-load", {}) or {}).get("data") or {}).get(up_key))
            shot("e4-memory-userpref-prompt.png")
        finally:
            close_stray_dialogs()
            _h.restore_prj_config(c, snap)


def case_f_no_mcp_server_config_dead():
    """F 设置页不再出现「MCPServerConfig」死项（I-64 已删死结构）。"""
    for kind, root in (("settings-mcp", ".settings-page"), ("settings-llm", ".settings-page")):
        open_page(kind, root)
        txt = ev("document.body.innerText") or ""
        if isinstance(txt, str) and "MCPServerConfig" in txt:
            raise TestError("%s 页出现死项 MCPServerConfig" % kind)


# ══════════════════════════════════════════════════════════
# G. 本波新改动：恢复默认（**仅原语**；场景已摘除，2026-09-26）+ 知识库右键无「复制到项目级」
#    G1 原语面板：app 级**无**「恢复默认」；project 级**有**且点击回填为草稿（dirty）
#    G2 知识库文件右键菜单：仅 重命名/删除（**无**「复制到项目级」）
#    G3 **已摘除（2026-09-26 用户口径：场景不需要回填/恢复默认）** —— 原「场景编辑对话框
#       project 级『恢复默认』回填 user 级」用例连同辅助函数一并删除（场景 id 全局唯一 →
#       「上一级同名场景」不存在，该入口已从 ScenarioEditDialog 整体移除，见 37 SCEN-008）
# ══════════════════════════════════════════════════════════


def _kb():
    # 2026-09-29：G 用例只涉及**工具**原语（tools/core/*.tool.md）→ 切到独立「工具」页签
    # （工具已从知识库分离，位于「会话」右侧；知识库页签不再显示 tools）。
    kb = kbmod.KB(c)
    if not kb.ensure_explorer_visible():
        raise TestError("filetree 区未能挂载")
    if not kb.switch_mode("tools"):
        raise TestError("切「工具」页签失败 active=%s" % kb.mode_active())
    return kb


# 树级别词：根行标签 = 「<知识库|工具> -<级别>」（前端 loadRoot：titleKey + ' -' + 级别名）
_KB_LV_WORD = {"app": "系统", "user": "用户", "project": "项目"}


def _kb_level(kb, kind):
    c.mq_emit("kb-level-select", {"kind": kind})
    w = _KB_LV_WORD[kind]
    if not kb.poll(lambda: any(w in kb.row_label(r) for r in kb.kb_rows()), 12):
        raise TestError("知识库切级后根行不符：kind=%s rows=%r"
                        % (kind, [kb.row_label(r) for r in kb.kb_rows()]))
    time.sleep(0.5)


def _kb_root_parent(kb):
    """根行的可点击名（首 token）：标签「知识库 -<级别>」→ '知识库'、「工具 -<级别>」→ '工具'。"""
    rows = kb.kb_rows()
    if not rows:
        raise TestError("知识库树无根行")
    return kb.row_label(rows[0]).split()[0]


def _kb_ensure_child(kb, parent, child):
    """展开 parent 直到 child 行可见（child 已可见则不动，避免重复点击把目录收起）。"""
    if any(kb.row_label(r) == child for r in kb.kb_rows()):
        return
    if not kb.click_kb_dir(parent):
        raise TestError("点击目录 %s 失败（rows=%r）" % (parent, [kb.row_label(r) for r in kb.kb_rows()]))
    if not kb.poll(lambda: any(kb.row_label(r) == child for r in kb.kb_rows()), 10):
        raise TestError("%s 未展开出 %s（rows=%r）"
                        % (parent, child, [kb.row_label(r) for r in kb.kb_rows()]))


def _close_bottom_tabs_by_name(name):
    """关闭底部页签栏中指定文件名的页签（原语面板跨轮残留会让组件复用旧实例）。

    注：`preview-tab-close-all` 不关 file-open 打开的文件页签，故按名逐个点 ×。
    """
    n = ev("(function(){let n=0;document.querySelectorAll('.tb-bar.tb-bottom .tb-tab').forEach(t=>{"
           "const nm=((t.querySelector('.tb-name')||{}).textContent||'').trim();"
           "if(nm.split(/[\\s\\u22ef\\u00d7]+/)[0]!==%s)return;const c=t.querySelector('.tb-close');"
           "if(c){c.dispatchEvent(new MouseEvent('click',{bubbles:true}));n++}});return n})()"
           % json.dumps(name))
    time.sleep(0.6)
    return n or 0


def _prim_footer_btns(kb):
    return kb.js("(function(){const P=Array.from(document.querySelectorAll('.prim-panel')).find(n=>n.offsetParent!==null);"
                 "if(!P)return [];const f=P.querySelector('.prim-footer');if(!f)return [];"
                 "return Array.from(f.querySelectorAll('button')).map(b=>({t:b.textContent.trim(),dis:!!b.disabled,"
                 "vis:b.getBoundingClientRect().width>0}));})()") or []


def _wait_prim_rd_enabled(kb, max_wait=10):
    """等原语「恢复默认」按钮出现且可点击（上一级探测 resolveUpperSource 是异步的）。"""
    end = time.time() + max_wait
    while time.time() < end:
        b = next((x for x in _prim_footer_btns(kb) if x["t"] == "恢复默认"), None)
        if b and b["vis"] and not b["dis"]:
            return b
        time.sleep(0.3)
    return None


def _prim_click_restore_default(kb):
    return kb.js("(function(){const P=Array.from(document.querySelectorAll('.prim-panel')).find(n=>n.offsetParent!==null);"
                 "if(!P)return false;const f=P.querySelector('.prim-footer');if(!f)return false;"
                 "const b=Array.from(f.querySelectorAll('button')).find(x=>x.textContent.trim()==='恢复默认');"
                 "if(!b)return false;b.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true;})()")


def case_g_restore_default_and_ctxmenu():
    """G 本波新改动：**原语**「恢复默认」按钮可见性/回填 + 知识库文件右键菜单项。

    注（2026-09-26，用户口径）：**场景**的「恢复默认/回填上一级」已整体摘除（场景 id
    全局唯一 → 无上一级同名来源）→ 本用例只覆盖**原语**路径（[37 SCEN-008]）。
    """
    kb = _kb()
    made_tool = False
    try:
        # 预置：项目级同名原语（与 app 级 file_find.tool.md 配对 → 有可回填的上一级）
        os.makedirs(PROJ_TOOL_DIR, exist_ok=True)
        with open(PROJ_TOOL, "w", encoding="utf-8") as f:
            f.write("# project copy probe\n\n[meta]\nname=file_find\nkind=probe\n\n"
                    "[description]\nPROJECT-COPY-DESC-%d\n\n[parameters]\nproperties:\n    q:\n"
                    "        type: string\nrequired:\n    - q\n\n[content]\nproject copy body\n"
                    % int(time.time()))
        made_tool = True

        # ── G1a：app 级原语 → 不显示「恢复默认」 ──
        _kb_level(kb, "app")
        _kb_ensure_child(kb, _kb_root_parent(kb), "tools")
        _kb_ensure_child(kb, "tools", "core")
        _kb_ensure_child(kb, "core", "file_find.tool.md")
        _close_bottom_tabs_by_name("file_find.tool.md")  # 关掉上一轮残留页签 → 组件重新挂载
        if not kb.click_kb_file("file_find.tool.md"):
            raise TestError("单击 app 级 file_find.tool.md 失败")
        if not kb.poll(lambda: kb.prim_panel_exists()
                       and kb.prim_active_path().replace("\\", "/").endswith("file_find.tool.md"), 10):
            raise TestError("app 级原语面板未打开：%r" % kb.prim_active_path())
        time.sleep(2.0)  # 上一级探测（loadLevelRoots/resolveUpperSource）为异步：留出完成时间再断言
        btns = _prim_footer_btns(kb)
        if not btns:
            raise TestError("app 级原语 footer 无按钮")
        if any(b["t"] == "恢复默认" for b in btns):
            raise TestError("app 级（系统只读）原语不应显示「恢复默认」：%r" % btns)
        if not any(b["t"] == "保存" for b in btns):
            raise TestError("app 级原语 footer 缺「保存」按钮：%r" % btns)

        # ── G1b：project 级原语 → 显示「恢复默认」且点击回填（dirty） ──
        _kb_level(kb, "project")
        _kb_ensure_child(kb, _kb_root_parent(kb), "tools")
        _kb_ensure_child(kb, "tools", "core")
        _kb_ensure_child(kb, "core", "file_find.tool.md")
        _close_bottom_tabs_by_name("file_find.tool.md")  # 同上：确保新开面板为全新实例（初始非 dirty）
        if not kb.click_kb_file("file_find.tool.md"):
            raise TestError("单击 project 级 file_find.tool.md 失败")
        if not kb.poll(lambda: kb.prim_panel_exists()
                       and kb.prim_active_path().replace("\\", "/").endswith("file_find.tool.md"), 10):
            raise TestError("project 级原语面板未打开：%r" % kb.prim_active_path())
        if not _wait_prim_rd_enabled(kb, 10):
            raise TestError("project 级「恢复默认」未出现/不可点击（应可回填 app 级同名原语）：%r"
                            % _prim_footer_btns(kb))
        if kb.prim_dirty():
            raise TestError("原语初始不应为 dirty")
        if not _prim_click_restore_default(kb):
            raise TestError("点击 project 级原语「恢复默认」失败")
        if not kb.poll(lambda: kb.prim_dirty(), 6):
            raise TestError("点击「恢复默认」后未回填为未保存草稿（dirty 未出现）")

        # ── G2：工具文件右键菜单项 = 重命名/删除（无「复制到项目级」） ──
        if not kb.rclick_kb_row("file_find.tool.md", False):
            raise TestError("右键工具文件失败")
        if not kb.poll(lambda: len(kb.kb_menu_texts()) > 0, 6):
            raise TestError("工具文件右键菜单未弹出")
        menu = kb.kb_menu_texts()
        kb.close_menus()
        if any("复制" in x for x in menu):
            raise TestError("工具文件右键菜单仍含「复制到项目级」/复制项：%r" % menu)
        if set(menu) != {"重命名", "删除"}:
            raise TestError("工具文件右键菜单项不符（应仅 重命名/删除）：%r" % menu)

        # G3 已摘除（2026-09-26）：场景「恢复默认/回填上一级」入口整体移除 —— 见用例 docstring。
    finally:
        if made_tool:
            try:
                if os.path.exists(PROJ_TOOL):
                    os.remove(PROJ_TOOL)
                for d in (PROJ_TOOL_DIR, os.path.join(PROJ_CAP, "tools")):
                    if os.path.isdir(d) and not os.listdir(d):
                        os.rmdir(d)
            except OSError:
                pass


# ══════════════════════════════════════════════════════════
# D. 重启持久化复核（独立实例 + 独立 work-dir，含 .git）
# ══════════════════════════════════════════════════════════

def _port_owner(port):
    out = subprocess.run(
        ["powershell", "-NoProfile", "-Command",
         "(Get-NetTCPConnection -LocalPort %d -State Listen -ErrorAction SilentlyContinue).OwningProcess" % port],
        capture_output=True, text=True)
    return [t.strip() for t in out.stdout.split() if t.strip().isdigit()]


def _kill_port(port, protect_ports=()):
    """仅用于清掉「上次残留」的端口占用者；显式排除受保护端口（底座）的持有者。

    历史坑：按端口杀进程若命中无关实例（同 exe 的底座）会连带终止它，故加保护名单。
    """
    protected = set()
    for pp in protect_ports:
        protected.update(_port_owner(pp))
    for pid in _port_owner(port):
        if pid in protected:
            print("    [warn] 端口 %d 持有者 %s 属受保护实例，跳过终止" % (port, pid))
            continue
        subprocess.run(["taskkill", "/F", "/T", "/PID", pid], capture_output=True)
    time.sleep(2)


def _kill_own(proc):
    """精确终止本脚本启动的实例（harness 句柄；不按名、不按端口扫）。"""
    if proc is None:
        return
    if hasattr(proc, "stop"):
        proc.stop()
        time.sleep(2)
        return
    if proc.poll() is not None:
        return
    _h.kill_tree(proc.pid)
    try:
        proc.wait(timeout=10)
    except Exception:
        pass
    time.sleep(2)


def _spawn_hist():
    # 大写盘符（用户要求）
    ws = HIST_WS[0].upper() + HIST_WS[1:]
    if not os.path.isdir(os.path.join(HIST_WS, ".git")):
        os.makedirs(os.path.join(HIST_WS, ".git"), exist_ok=True)
    os.makedirs(HIST_DATA, exist_ok=True)
    # 起第二实例前先清掉**上次残留**占用 2347 的进程（保护底座 2345）
    _kill_port(HIST_PORT, protect_ports=(BASE_PORT,))
    # 经 harness 自起 → 登记回收（本用例 finally / 进程退出 / 信号三条路径都会清）
    proc = _h.popen_own([EXE, "--test-port=%d" % HIST_PORT, "--work-dir=" + ws,
                         "--data-dir=" + HIST_DATA],
                        name="gui:hist", cwd=os.path.dirname(EXE))
    cli = ChonkClient(base="http://127.0.0.1:%d" % HIST_PORT)
    try:
        cli.wait_ready(120)
    except Exception:
        _kill_own(proc)
        raise
    _h.ensure_locale(cli)  # 语言确定性：D 用例页签文案按 zh-CN 断言（独立实例同样受 DB ui.locale 影响）
    time.sleep(1.5)
    return cli, proc


def _hist_switch(cli):
    return cli.eval("(function(){const e=[...document.querySelectorAll('.history-toggle .b-switch')]"
                    ".find(x=>x.getBoundingClientRect().width>0);if(!e)return null;"
                    "return JSON.stringify({checked:e.classList.contains('is-checked'),"
                    "disabled:e.classList.contains('is-disabled')});})()")


def case_d_history_restart():
    """D「文件历史」开关：点击保存 history.enabled → 重启 GUI 后回读（持久化复核）。

    注：history 开关 `:disabled="!gitAvailable"`，gitAvailable = 系统可执行 git（gui.vcs.info.gitInstalled）
    && work-dir 下存在 `.git`。故本用例用独立 work-dir 并放置一个 `.git` 目录（仅做存在性判定，
    不执行任何 git 命令），且本机需已安装 git（否则开关被禁用，用例会失败）。
    """
    cli = None
    proc = None
    try:
        cli, proc = _spawn_hist()
        cli.eval("window.mq.emit('preview-tab-open', {kind:'settings-project'})")
        time.sleep(2.0)
        r = cli.eval("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
                     ".find(e=>e.getBoundingClientRect().width>0);if(!R)return 'no';"
                     "const t=[...R.querySelectorAll('.b-tabs-item')].find(x=>x.textContent.trim()==='文件历史');"
                     "if(!t)return 'no-tab';t.click();return 'ok';})()")
        if _loads(r) != "ok":
            raise TestError("独立实例未打开「文件历史」页签：%r" % _loads(r))
        time.sleep(1.0)
        st = _loads(_hist_switch(cli))
        if not st:
            raise TestError("未找到 history 开关")
        if st["disabled"]:
            raise TestError("history 开关被禁用（应因 workDir/.git 而可用）")
        before = st["checked"]
        cli.eval("(function(){const e=[...document.querySelectorAll('.history-toggle .b-switch')]"
                 ".find(x=>x.getBoundingClientRect().width>0);e.click();})()")
        time.sleep(1.0)
        # 新口径（42 §2 (125)）：开关默认关；**开启须先确认**（对用户仓库有副作用）→ 点弹窗「确定」
        if not before:
            r = cli.eval("(function(){const b=document.querySelector('.dialog-shell button.b-btn--primary')"
                         "||document.querySelector('.dialog-body button.b-btn--primary');"
                         "if(!b)return 'no-dialog';b.click();return 'ok';})()")
            if _loads(r) != "ok":
                raise TestError("开启 history 未弹确认框：%r" % _loads(r))
            time.sleep(1.0)
        v = cli.req("data-prj-config-list", {}).get("list", {}).get("history.enabled")
        want = "false" if before else "true"
        if v != want:
            raise TestError("点击后 history.enabled=%r，期望 %r" % (v, want))
        # 重启 GUI（只终止本脚本启动的实例，按 PID 精确回收）
        _kill_own(proc)
        cli, proc = _spawn_hist()
        cli.eval("window.mq.emit('preview-tab-open', {kind:'settings-project'})")
        time.sleep(2.0)
        cli.eval("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
                 ".find(e=>e.getBoundingClientRect().width>0);const t=[...R.querySelectorAll('.b-tabs-item')]"
                 ".find(x=>x.textContent.trim()==='文件历史');if(t)t.click();})()")
        time.sleep(1.0)
        st2 = _loads(_hist_switch(cli))
        if st2["checked"] != (not before):
            raise TestError("重启后开关状态=%s，期望 %s（持久化未生效）" % (st2["checked"], not before))
    finally:
        _kill_own(proc)
        shutil.rmtree(HIST_WS, ignore_errors=True)
        shutil.rmtree(HIST_DATA, ignore_errors=True)


# ══════════════════════════════════════════════════════════
# D2. 文件历史 UI 细节（独立实例）：保留策略落库/重启回读 · 只读时间轴 · 清空历史
# ══════════════════════════════════════════════════════════
# DOM 契约（views/settings/HistoryConfig.vue）：
#   * 保留策略：`input.history-num-input` ×2（labels `.history-num-label` = 保留个数 / 保留天数）
#     + `.history-actions .b-btn--primary`（文案「保存」）→ setConfig('history.checkpoint_keep'|..._ttl_days')；
#   * 只读时间轴：`[data-history-status]`（`.tl-status-key` / `.tl-status-val` 成对）+
#     `[data-history-timeline]`（Table；空态 `td.b-table-empty` 文案区分未启用 / 已启用无数据）+
#     `[data-history-clear]`（文案「清空历史」，`timeline.length===0` 时禁用）→ confirm → setConfig('history.clear')。

def _hist_open_tab(cli):
    """独立实例打开「项目设置 → 文件历史」页签（先关全部预览页签 → 重挂载 → 读最新 prj）。"""
    cli.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    cli.mq_emit("preview-tab-open", {"kind": "settings-project"})
    time.sleep(2.0)
    r = cli.eval("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
                 ".find(e=>e.getBoundingClientRect().width>0);if(!R)return 'no';"
                 "const t=[...R.querySelectorAll('.b-tabs-item')].find(x=>x.textContent.trim()==='文件历史');"
                 "if(!t)return 'no-tab';t.click();return 'ok';})()")
    if _loads(r) != "ok":
        raise TestError("未打开「文件历史」页签：%r" % _loads(r))
    time.sleep(0.8)


_HIST_DOM_JS = """(function(){
const R=[...document.querySelectorAll('.history-root')].find(e=>e.getBoundingClientRect().width>0);
if(!R)return null;
const tl=R.querySelector('[data-history-timeline]');
const clear=R.querySelector('[data-history-clear]');
const labels=[...R.querySelectorAll('.form-label')].map(x=>x.textContent.trim());
return JSON.stringify({
  labels:labels,
  numLabels:[...R.querySelectorAll('.history-num-label')].map(x=>x.textContent.trim()),
  inputs:[...R.querySelectorAll('input.history-num-input')].map(x=>x.value),
  hasStatus:!!R.querySelector('[data-history-status]'),
  statusKeys:[...R.querySelectorAll('[data-history-status] .tl-status-key')].map(x=>x.textContent.trim()),
  statusVals:[...R.querySelectorAll('[data-history-status] .tl-status-val')].map(x=>x.textContent.trim()),
  hasTimeline:!!tl,
  rows:tl?tl.querySelectorAll('tbody tr').length:0,
  emptyText:tl?(function(){const e=tl.querySelector('td.b-table-empty');return e?e.textContent.trim():'';})():null,
  clearExists:!!clear,
  clearDisabled:clear?(clear.disabled===true||clear.classList.contains('is-disabled')):null,
  clearText:clear?clear.textContent.trim():null
});})()"""


def _hist_dom(cli):
    return _loads(cli.eval(_HIST_DOM_JS))


def _hist_wait(cli, pred, desc, max_wait=8):
    end = time.time() + max_wait
    last = None
    while time.time() < end:
        last = _hist_dom(cli)
        if last and pred(last):
            return last
        time.sleep(0.4)
    raise TestError("%s 超时；末次观测=%r" % (desc, last))


_HIST_SID = "l4-hist-sess"  # 固定会话（= 链 slug）：页面按**当前会话**读会话级键（I-135）


def _hist_seed_prj(cli, status_obj, timeline_arr):
    """直写**会话级**键 `history.status.<slug>` / `history.timeline.<slug>`（真机落库；
    供只读时间轴渲染断言）。先确保该会话存在并设为**活动会话** → 页面按当前会话读取（I-135）。"""
    cli.req("data-session-ensure-session", {"session_id": _HIST_SID})
    cli.req("data-session-active-set", {"session_id": _HIST_SID})
    cli.req("data-prj-config-save",
            {"data": {"key": "history.status." + _HIST_SID,
                      "value": json.dumps(status_obj, ensure_ascii=False)}})
    cli.req("data-prj-config-save",
            {"data": {"key": "history.timeline." + _HIST_SID,
                      "value": json.dumps(timeline_arr, ensure_ascii=False)}})


def _hist_set_inputs(cli, keep, ttl):
    return _loads(cli.eval("""(function(){
const R=[...document.querySelectorAll('.history-root')].find(e=>e.getBoundingClientRect().width>0);
if(!R)return 'no-root';
const ins=[...R.querySelectorAll('input.history-num-input')];
if(ins.length!==2)return 'inputs='+ins.length;
const set=(el,v)=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,v);
el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));};
set(ins[0],%s);set(ins[1],%s);return 'ok';})()""" % (json.dumps(keep), json.dumps(ttl))))


def _hist_click_save(cli):
    r = _loads(cli.eval("(function(){const R=[...document.querySelectorAll('.history-root')]"
                        ".find(e=>e.getBoundingClientRect().width>0);if(!R)return 'no-root';"
                        "const b=[...R.querySelectorAll('.history-actions .b-btn')]"
                        ".find(x=>x.textContent.trim()==='保存');if(!b)return 'no-btn';b.click();return 'ok';})()"))
    if r != "ok":
        raise TestError("点击保留策略【保存】失败：%r" % r)


def _hist_click_clear(cli):
    r = _loads(cli.eval("(function(){const R=[...document.querySelectorAll('.history-root')]"
                        ".find(e=>e.getBoundingClientRect().width>0);if(!R)return 'no-root';"
                        "const b=R.querySelector('[data-history-clear]');if(!b)return 'no-btn';b.click();return 'ok';})()"))
    if r != "ok":
        raise TestError("点击【清空历史】失败：%r" % r)
    time.sleep(0.5)


def _hist_wait_dialog(cli, max_wait=6):
    end = time.time() + max_wait
    while time.time() < end:
        if _loads(cli.eval("!!([...document.querySelectorAll('.dialog-shell')]"
                           ".find(e=>e.getBoundingClientRect().width>0))")):
            return True
        time.sleep(0.3)
    return False


def _hist_click_dialog(cli, label):
    r = _loads(cli.eval("(function(){const R=[...document.querySelectorAll('.dialog-shell')]"
                        ".find(e=>e.getBoundingClientRect().width>0);if(!R)return 'no-dialog';"
                        "const b=[...R.querySelectorAll('button.b-btn')].find(x=>x.textContent.trim()===%s);"
                        "if(!b)return 'no-btn';b.click();return 'ok';})()" % json.dumps(label)))
    if r != "ok":
        raise TestError("确认框点击 %r 失败：%r" % (label, r))
    time.sleep(0.6)


def case_d2_history_ui_details():
    """文件历史 UI 细节（独立实例；真机 DOM / prj 落库断言）：

    ① 保留策略：改两个数字输入（保留个数/保留天数）→ 点【保存】→ prj 键
       `history.checkpoint_keep` / `history.checkpoint_ttl_days` **精确键名**落库；**重启 GUI** 后重新
       打开页面 → 两个输入回读 == 所存值（真机持久化，非"保存成功即过"）。
    ② 只读时间轴：状态条 `[data-history-status]` 的「模式/检查点数/失败次数」等字段按 prj 真值渲染
       （textContent 取值）；`[data-history-timeline]` 表格；**空态分流**：未启用 →
       「文件历史未启用（无检查点数据）。」；已启用无数据 → 「暂无检查点。」（文案不同）。
    ③ 【清空历史】`[data-history-clear]`：点击 → 出现确认框 → **取消**不写 `history.clear`；
       再次点击 → **确定** → `history.clear` 键值变化（真机落库）。
    """
    cli = None
    proc = None
    KEEP_V, TTL_V = "321", "11"
    try:
        cli, proc = _spawn_hist()
        _hist_open_tab(cli)
        dom = _hist_wait(cli, lambda d: d.get("hasStatus") and d.get("hasTimeline"),
                         "文件历史页渲染（.history-root/[data-history-status]/[data-history-timeline]）")
        # ── ①② 静态结构 + 标签（精确文案）──
        for want in ("保留策略", "检查点时间轴"):
            if want not in dom["labels"]:
                raise TestError("文件历史缺 label %r，实际=%r" % (want, dom["labels"]))
        if dom["numLabels"] != ["保留个数", "保留天数"]:
            raise TestError("保留策略输入标签应为 ['保留个数','保留天数']，实际=%r" % dom["numLabels"])
        if len(dom["inputs"]) != 2:
            raise TestError("保留策略应有 2 个数字输入，实际=%r" % dom["inputs"])
        for key in ("模式", "检查点数", "占用体积", "最近打点", "最近耗时", "失败次数"):
            if key not in dom["statusKeys"]:
                raise TestError("状态条缺字段 %r，实际=%r" % (key, dom["statusKeys"]))
        if not dom["clearExists"] or dom["clearText"] != "清空本会话历史":
            raise TestError("【清空本会话历史】按钮缺失/文案不符：exists=%s text=%r"
                            % (dom["clearExists"], dom["clearText"]))

        # ── ② 空态分流 A：未启用（status off）+ 空时间轴 → 「…未启用…」 ──
        _hist_seed_prj(cli, {"enabled": False, "mode": "off"}, [])
        _hist_open_tab(cli)
        a = _hist_wait(cli, lambda d: d.get("emptyText") == "文件历史未启用（无检查点数据）。",
                       "未启用空态文案")
        # ── ② 空态分流 B：已启用无数据（status enabled）+ 空时间轴 → 「暂无检查点。」+ 状态字段真值 ──
        _hist_seed_prj(cli, {"enabled": True, "mode": "active", "checkpointCount": 3,
                             "failCount": 2, "bytes": 2048}, [])
        _hist_open_tab(cli)
        b = _hist_wait(cli, lambda d: d.get("emptyText") == "暂无检查点。", "已启用无数据空态文案")
        if a["emptyText"] == b["emptyText"]:
            raise TestError("未启用与已启用空态文案应不同，实际均=%r" % a["emptyText"])
        sv = dict(zip(b["statusKeys"], b["statusVals"]))
        if sv.get("模式") != "正常" or sv.get("检查点数") != "3" or sv.get("失败次数") != "2":
            raise TestError("状态条字段未按 prj 真值渲染（期望 模式=正常/检查点数=3/失败次数=2）：%r" % sv)
        print("[D2] 空态分流：未启用=%r / 已启用无数据=%r · 状态条真值=%r"
              % (a["emptyText"], b["emptyText"], {k: sv.get(k) for k in ("模式", "检查点数", "失败次数")}),
              flush=True)

        # ── ③ 清空历史：取消不写 / 确定改写 history.clear ──
        _hist_seed_prj(cli, {"enabled": True, "mode": "active", "checkpointCount": 1, "failCount": 0},
                       [{"n": -1, "id": "deadbeef", "ts": "2026-09-28T00:00:00Z",
                         "tool": "session-complete", "session": "s1", "files": 2, "added": 3, "removed": 1}])
        _hist_open_tab(cli)
        c0 = _hist_wait(cli, lambda d: d.get("rows", 0) >= 1, "时间轴 seed 行渲染")
        if c0["clearDisabled"]:
            raise TestError("有时间轴数据时【清空历史】应可点（实际禁用）")
        before = cli.req("data-prj-config-list", {}).get("list", {}).get("history.clear")
        _hist_click_clear(cli)
        if not _hist_wait_dialog(cli):
            raise TestError("【清空历史】未弹出确认框")
        _hist_click_dialog(cli, "取消")
        time.sleep(0.8)
        mid = cli.req("data-prj-config-list", {}).get("list", {}).get("history.clear")
        if mid != before:
            raise TestError("取消清空不应写 history.clear：%r → %r" % (before, mid))
        _hist_click_clear(cli)
        if not _hist_wait_dialog(cli):
            raise TestError("【清空历史】第二次未弹出确认框")
        _hist_click_dialog(cli, "确定")
        deadline = time.time() + 8
        after = cli.req("data-prj-config-list", {}).get("list", {}).get("history.clear")
        while time.time() < deadline and after == before:
            time.sleep(0.4)
            after = cli.req("data-prj-config-list", {}).get("list", {}).get("history.clear")
        if not after or after == before:
            raise TestError("确定清空应改写 history.clear：%r → %r" % (before, after))
        print("[D2] 清空历史：取消不改写（=%r）· 确定改写 history.clear %r → %r"
              % (mid, before, after), flush=True)

        # ── ① 保留策略：改值 → 保存 → prj 键落库 → 重启 → 回读 ──
        r = _hist_set_inputs(cli, KEEP_V, TTL_V)
        if r != "ok":
            raise TestError("写保留策略输入失败：%r" % r)
        _hist_click_save(cli)
        time.sleep(1.2)
        lst = cli.req("data-prj-config-list", {}).get("list", {})
        if lst.get("history.checkpoint_keep") != KEEP_V:
            raise TestError("history.checkpoint_keep 落库=%r，期望 %r"
                            % (lst.get("history.checkpoint_keep"), KEEP_V))
        if lst.get("history.checkpoint_ttl_days") != TTL_V:
            raise TestError("history.checkpoint_ttl_days 落库=%r，期望 %r"
                            % (lst.get("history.checkpoint_ttl_days"), TTL_V))
        _kill_own(proc)
        cli, proc = _spawn_hist()
        _hist_open_tab(cli)
        d = _hist_wait(cli, lambda x: len(x.get("inputs") or []) == 2, "重启后保留策略输入")
        if d["inputs"] != [KEEP_V, TTL_V]:
            raise TestError("重启后保留策略回读=%r，期望 %r" % (d["inputs"], [KEEP_V, TTL_V]))
        print("[D2] 保留策略落库（键 history.checkpoint_keep=%s / history.checkpoint_ttl_days=%s）·"
              " 重启后回读=%r" % (KEEP_V, TTL_V, d["inputs"]), flush=True)
    finally:
        _kill_own(proc)
        shutil.rmtree(HIST_WS, ignore_errors=True)
        shutil.rmtree(HIST_DATA, ignore_errors=True)


def main():
    ok = 0
    total = 0
    c.wait_ready(60)
    c.console(clear=True)
    # 套件级 prj 快照-还原（51 §6-8）：本套件多处点击「保存」（A2 上下文管理会落
    # keep_full_max_turns/keep_full_max_tokens/compress_token_threshold 等表单键）与 `preview-tab-close-all`
    # （前端隐式落 opened-files）→ 整体快照、结束（含异常）整体回滚到跑前状态。
    with _h.prj_config_guard(c, None):
        for name, fn in [
            ("A1 项目设置页签文字 + 页面位置", case_a1_tabs_text_position),
            ("A2 上下文管理：文字/联动禁用(颜色)/点击保存落库", case_a2_context_memory),
            ("A3 CodeGraph 索引：默认回显 + 开关点击保存回读", case_a3_index_codegraph),
            ("A4 Vfts 索引：默认回显 + 开关点击保存回读", case_a4_index_vfts),
            ("B1 LLM 列表/参数：文字 + 弹窗位置/一行两列 + 保存落库", case_b1_llm_list_params),
            ("B2 MCP：transport 从属显示 + runtime/args 保存落库", case_b2_mcp_transport_branching),
            ("B3 路径/工具链：Chrome 已探测 + 三级归属", case_b3_paths_toolchain),
            ("C 已删除项确不存在（旧弹窗 / 提示词页签）", case_c_removed_items),
            ("E1 记忆库「新增类别」→ 出现 + 内容编辑弹框保存即关（回读）", case_e1_memory_add_and_edit),
            ("E2 行内【编辑内容】弹框（内容正确/取消不落库）+「删除类别」二次确认后消失", case_e2_memory_delete_ui),
            ("E3 data-memory-delete → {ok,id} + data-memory-refresh(op=delete) 广播", case_e3_memory_delete_datamsg),
            ("E4 记忆类别提示词编辑（内置默认回填/来源提示/保存落 prj/重置回落）", case_e4_memory_prompt_edit),
            ("F 设置页无「MCPServerConfig」死项", case_f_no_mcp_server_config_dead),
            ("G 恢复默认（原语 app 无/project 有+回填）+ 知识库右键无「复制到项目级」", case_g_restore_default_and_ctxmenu),
            ("D「文件历史」开关点击保存 → 重启 GUI 回读", case_d_history_restart),
            ("D2 文件历史 UI 细节：保留策略落库+重启回读 · 只读时间轴/空态分流 · 清空历史(取消/确定)",
             case_d2_history_ui_details),
        ]:
            total += 1
            ok += run_case(name, fn)
    errs = c.console()
    for e in (errs.get("entries") or []):
        if e.get("level") in ("error",):
            print("  [CONSOLE-ERROR] %s" % e.get("text"))
    print("\n配置 UI 保存断言：%d/%d 通过（截图存证：%s）" % (ok, total, SHOTS))
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
