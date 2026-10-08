# -*- coding: utf-8 -*-
"""B · 语义/回环级推广 ①：**设置页配置持久化回环**（写 → 落库 → UI 重开 → 重启回读）。

覆盖（**拒绝"存在级"：断言字段值经持久层往返一致**）：
  C1 写→落库：设置页「上下文管理」把「保留完整对话内容的最近轮数」改为唯一哨兵值 + 点【保存】
     → **消息面回读** `data-prj-config-list` 的 `keep_full_max_turns` == 哨兵值（语义：精确值）。
  C2 UI 重开回读：关闭全部页签 → 重开设置页 → 「上下文管理」→ 字段值 == 哨兵值
     （证明页面重开时从持久层回读，非内存残留）。
  C3 **跨重启回环**：同参数重启 GUI（同 work-dir/data-dir/HOME）→ 重开设置页 → 字段值 == 哨兵值
     （持久化跨进程存活）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard（callable）`（含重启后仍指向当前 client）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  `data-prj-config-list`（§3.1）· `preview-tab-{open,close-all}`（§6.1）· DOM（--test-port /eval）。

运行：python run_sem_config_loop.py
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

EXE = _h.resolve_gui_exe()
if not os.path.isfile(EXE):
    print("RESULT: True (SKIPPED: 未找到 GUI 产物 %s → 先构建 dist/desktop)" % EXE, flush=True)
    sys.exit(0)

WS = _h.tmp_dir("ck-semcfg-ws-")
DD = _h.tmp_dir("ck-semcfg-dd-")
HOME = _h.tmp_home()
MARK = "37"                       # 唯一哨兵值（默认 10；37 与默认不同）
LABEL_SUB = "保留完整对话内容的最近轮数"
ROOT = ".project-config-panel"
TAB = "上下文管理"

_STATE = {"h": None, "c": None}
c = None  # 模块级当前 client（重启后由 _boot 重赋）


def _boot():
    global c
    h = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
    _STATE["h"], _STATE["c"], c = h, h.client, h.client
    return h


_boot()
_h.suite_config_guard(lambda: c)      # callable → 重启后仍指向当前 client
_h.ensure_locale(c, "zh-CN")
print("[env] ws=%s data=%s home=%s gui=%d mark=%s（临时目录，结束即删）"
      % (WS, DD, HOME, _STATE["h"].port, MARK), flush=True)


def ev(js, timeout=6000):
    return _h._plain(c.eval(js, timeout))


def prj():
    r = c.req("data-prj-config-list", {})
    return (r.get("list") or {}) if isinstance(r, dict) else {}


def open_page(kind, root_sel, max_wait=14):
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.5)
    c.mq_emit("preview-tab-open", {"kind": kind})
    end = time.time() + max_wait
    while time.time() < end:
        if int(ev("([...document.querySelectorAll(%s)].filter(e=>e.getBoundingClientRect().width>0)).length"
                  % json.dumps(root_sel)) or 0) > 0:
            time.sleep(0.7)
            return
        time.sleep(0.3)
    raise TestError("设置页未打开: kind=%s root=%s" % (kind, root_sel))


def click_tab(label, root_sel):
    r = ev("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
           "if(!R)return 'no-root';const t=[...R.querySelectorAll('.b-tabs-item')]"
           ".find(x=>x.textContent.trim()===%s);if(!t)return 'no-tab';t.click();return 'ok';})()"
           % (json.dumps(root_sel), json.dumps(label)))
    time.sleep(1.5)   # 页签激活后配置异步 load 落定再操作（避免写被回读覆盖）
    if r != "ok":
        raise TestError("切换页签失败 %s: %s" % (label, r))


def field_value(root_sel, label_sub):
    """可见面板内、label 含 label_sub 的 form-item 的 input/textarea 值（找不到 → None）。"""
    return ev("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
              "if(!R)return null;const it=[...R.querySelectorAll('.form-item')]"
              ".find(x=>{const l=x.querySelector('.form-label');return l&&l.textContent.indexOf(%s)>=0;});"
              "if(!it)return null;const inp=it.querySelector('input,textarea');return inp?inp.value:null;})()"
              % (json.dumps(root_sel), json.dumps(label_sub)))


def set_field(root_sel, label_sub, value, max_wait=6.0):
    """写字段并**回读确认**：页面激活后配置是**异步 load**（可能晚于本次写入落地，覆盖回旧值）
    → 写后回读不为目标值时重写，直到稳定命中（只改"何时写"，不改断言）。"""
    end = time.time() + max_wait
    got = None
    while True:
        r = ev("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
               "if(!R)return 'no-root';const it=[...R.querySelectorAll('.form-item')]"
               ".find(x=>{const l=x.querySelector('.form-label');return l&&l.textContent.indexOf(%s)>=0;});"
               "if(!it)return 'no-item';const inp=it.querySelector('input,textarea');if(!inp)return 'no-input';"
               "const proto=inp instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;"
               "Object.getOwnPropertyDescriptor(proto,'value').set.call(inp,%s);"
               "inp.dispatchEvent(new Event('input',{bubbles:true}));"
               "inp.dispatchEvent(new Event('change',{bubbles:true}));return 'ok';})()"
               % (json.dumps(root_sel), json.dumps(label_sub), json.dumps(value)))
        if r != "ok":
            raise TestError("写字段失败(label≈%s): %s" % (label_sub, r))
        for _ in range(8):
            got = field_value(root_sel, label_sub)
            if got is not None and str(got) == str(value):   # #number 回读可能为 JSON 数字
                return
            time.sleep(0.15)
        if time.time() >= end:
            raise TestError("写字段后回读=%r（期望 %r）" % (got, value))
        time.sleep(0.3)


def click_save(root_sel):
    r = ev("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
           "if(!R)return 'no-root';const bs=[...R.querySelectorAll('button.b-btn--primary')]"
           ".filter(b=>b.getBoundingClientRect().width>0);const b=bs.find(x=>x.textContent.trim()==='保存')||bs[0];"
           "if(!b)return 'no-btn';b.click();return 'ok';})()" % json.dumps(root_sel))
    if r != "ok":
        raise TestError("点击保存失败: %s" % r)


def wait_toast(max_wait=6):
    end = time.time() + max_wait
    while time.time() < end:
        if int(ev("document.querySelectorAll('.b-message.b-message--success').length") or 0) > 0:
            return True
        time.sleep(0.2)
    return False


def case_c1_write_and_persist():
    """C1：UI 写 + 保存 → 消息面回读 == 哨兵值（持久化）。"""
    open_page("settings-project", ROOT)
    click_tab(TAB, ROOT)
    before = prj().get("keep_full_max_turns")
    set_field(ROOT, LABEL_SUB, MARK)
    click_save(ROOT)
    if not wait_toast():
        raise TestError("保存后未见成功提示")
    got = prj().get("keep_full_max_turns")
    if str(got) != MARK:
        raise TestError("保存后 data-prj-config-list keep_full_max_turns=%r（期望 %r；保存前 %r）"
                        % (got, MARK, before))
    print("[EVIDENCE] " + json.dumps({"case": "C1 UI 写→落库回读", "before": before, "after": got},
                                     ensure_ascii=False), flush=True)


def case_c2_reopen_readback():
    """C2：关闭页签 → 重开设置页 → 字段值 == 哨兵值（页面重开从持久层回读）。"""
    open_page("settings-project", ROOT)
    click_tab(TAB, ROOT)
    v = field_value(ROOT, LABEL_SUB)
    if v is None or str(v) != MARK:
        raise TestError("重开后字段值=%r（期望 %r）" % (v, MARK))
    print("[EVIDENCE] " + json.dumps({"case": "C2 重开页签回读", "value": v}, ensure_ascii=False), flush=True)


def case_c3_restart_readback():
    """C3：同参数重启 GUI → 重开设置页 → 字段值 == 哨兵值（跨进程持久化）。"""
    _STATE["h"].stop()
    _boot()
    _h.ensure_locale(c, "zh-CN")
    msg = prj().get("keep_full_max_turns")
    if str(msg) != MARK:
        raise TestError("重启后消息面 keep_full_max_turns=%r（期望 %r）" % (msg, MARK))
    open_page("settings-project", ROOT)
    click_tab(TAB, ROOT)
    v = field_value(ROOT, LABEL_SUB)
    if v is None or str(v) != MARK:
        raise TestError("重启后设置页字段值=%r（期望 %r）" % (v, MARK))
    print("[EVIDENCE] " + json.dumps({"case": "C3 跨重启回读", "msg": msg, "ui": v},
                                     ensure_ascii=False), flush=True)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = preview-tab-open/close-all + data-prj-config-*（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("C1 UI 写 keep_full_max_turns → 保存 → 消息面回读一致", case_c1_write_and_persist),
        ("C2 关闭页签→重开设置页 → 字段值经受持久层回读", case_c2_reopen_readback),
        ("C3 同参重启 GUI → 设置页字段值存活（跨进程回环）", case_c3_restart_readback),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n设置页配置持久化回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
