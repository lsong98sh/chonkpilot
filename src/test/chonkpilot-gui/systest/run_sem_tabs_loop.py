# -*- coding: utf-8 -*-
"""B8 · 语义/回环级推广 ③：**窗口/标签切换后状态保持**（页签切换 → 内容语义对应 → 切回保持）。

`24-多窗口模型设计方案` 为 🔵 设计定稿未实施（native 多窗口不可用测试端口驱动），
故本脚本取**可用测试端口驱动的等价面** = 预览区**底部页签（TabBar）切换**：
  T1 开两文件 → 两页签名齐现（语义：页签身份，非"容器存在"）。
  T2 切到 a.txt → 激活页签 == a.txt **且可见 `.tab-panel` 内容含 A 标记、不含 B 标记**
     （语义：激活态与内容**对应**）。
  T3 切到 b.txt → 内容含 B 标记、不含 A（切换生效）。
  T4 再切回 a.txt → 内容仍含 A、不含 B（**切换后状态保持**：v-show 面板未被破坏 / 内容回环）。
  T5 prj `opened-files` 回读含两文件（页签集经受持久层；`run_restore_tabs` 已验证跨重启，本处只做同进程语义回读）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(c)`。夹具文件落在**临时 work-dir**（随 tmp_dir 回收，零残留）。

观测渠道（**均为既有主题/持久键，零新增**）：`file-open`（前端本地）· 页签 DOM（`--test-port` /eval）·
`data-prj-config-list` 的 `opened-files`（§3.1）。

运行：python run_sem_tabs_loop.py
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

WS = _h.tmp_dir("ck-semtab-ws-")
DD = _h.tmp_dir("ck-semtab-dd-")
HOME = _h.tmp_home()
A, B = "a.txt", "b.txt"
MARK_A = "SEMTAB-alpha-9c1"
MARK_B = "SEMTAB-bravo-9c2"

with open(os.path.join(WS, A), "w", encoding="utf-8") as f:
    f.write(MARK_A + "\nline-a\n")
with open(os.path.join(WS, B), "w", encoding="utf-8") as f:
    f.write(MARK_B + "\nline-b\n")

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c, "zh-CN")
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, _g.port), flush=True)

TABS_JS = "JSON.stringify([...document.querySelectorAll('.tb-bar.tb-bottom .tb-tab .tb-name')].map(e=>e.textContent.trim()))"
ACTIVE_JS = ("(()=>{const t=document.querySelector('.tb-bar.tb-bottom .tb-tab.active .tb-name');"
             "return t?t.textContent.trim():''})()")
VISIBLE_CONTENT_JS = """(()=>{
  const panels=[...document.querySelectorAll('.tab-panel')].filter(p=>getComputedStyle(p).display!=='none');
  if(!panels.length) return '';
  const code=panels[panels.length-1].querySelector('.source-code');
  return code?code.textContent.trim():panels[panels.length-1].textContent.trim();
})()"""


def J(js):
    return _h._plain(c.eval(js))


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def wait_upto(js, ok, max_wait=15.0, interval=0.3):
    end = time.time() + max_wait
    v = J(js)
    while not ok(v) and time.time() < end:
        time.sleep(interval)
        v = J(js)
    return v


def open_file(name):
    c.mq_emit("file-open", {"path": os.path.join(WS, name).replace("\\", "/")})


def click_tab(name):
    r = J("(function(){const t=[...document.querySelectorAll('.tb-bar.tb-bottom .tb-tab')]"
          ".find(e=>{const n=e.querySelector('.tb-name');return n&&n.textContent.trim()===%s;});"
          "if(!t)return 'no-tab';t.click();return 'ok';})()" % json.dumps(name))
    if r != "ok":
        raise TestError("点击页签失败 %s: %s" % (name, r))
    time.sleep(0.6)


def prj_opened():
    lst = (c.req("data-prj-config-list", {}) or {}).get("list") or {}
    raw = lst.get("opened-files")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw)
        except Exception:
            raw = []
    return [str(x) for x in (raw or [])]


def case_t1_two_tabs():
    """T1：打开两文件 → 两页签名齐现（语义：页签身份）。"""
    open_file(A)
    open_file(B)
    tabs = wait_upto(TABS_JS, lambda v: isinstance(v, list) and A in v and B in v, max_wait=20.0) or []
    if not (A in tabs and B in tabs):
        raise TestError("两页签未齐现：%r" % tabs)
    evi("T1 页签身份", tabs=tabs)


def case_t2_switch_a_content():
    """T2：切到 a.txt → 激活态 == a；可见内容含 A 不含 B。"""
    click_tab(A)
    act = wait_upto(ACTIVE_JS, lambda v: v == A, max_wait=8.0)
    if act != A:
        raise TestError("激活页签=%r（期望 %r）" % (act, A))
    txt = wait_upto(VISIBLE_CONTENT_JS, lambda v: isinstance(v, str) and MARK_A in v, max_wait=10.0) or ""
    if MARK_A not in txt or MARK_B in txt:
        raise TestError("a.txt 可见内容不符（含A=%s 含B=%s）：%r"
                        % (MARK_A in txt, MARK_B in txt, txt[:120]))
    evi("T2 切换 a：激活+内容对应", active=act, content_has_A=True)


def case_t3_switch_b_content():
    """T3：切到 b.txt → 内容含 B 不含 A。"""
    click_tab(B)
    act = wait_upto(ACTIVE_JS, lambda v: v == B, max_wait=8.0)
    if act != B:
        raise TestError("激活页签=%r（期望 %r）" % (act, B))
    txt = wait_upto(VISIBLE_CONTENT_JS, lambda v: isinstance(v, str) and MARK_B in v, max_wait=10.0) or ""
    if MARK_B not in txt or MARK_A in txt:
        raise TestError("b.txt 可见内容不符（含B=%s 含A=%s）：%r"
                        % (MARK_B in txt, MARK_A in txt, txt[:120]))
    evi("T3 切换 b：激活+内容对应", active=act, content_has_B=True)


def case_t4_switch_back_keeps_state():
    """T4：再切回 a.txt → 内容仍含 A 不含 B（切换后状态保持）。"""
    click_tab(A)
    act = wait_upto(ACTIVE_JS, lambda v: v == A, max_wait=8.0)
    txt = wait_upto(VISIBLE_CONTENT_JS, lambda v: isinstance(v, str) and MARK_A in v, max_wait=10.0) or ""
    if act != A or MARK_A not in txt or MARK_B in txt:
        raise TestError("切回 a 后状态不符（active=%r 含A=%s 含B=%s）：%r"
                        % (act, MARK_A in txt, MARK_B in txt, txt[:120]))
    evi("T4 切回保持", active=act, content_has_A=True)


def case_t5_opened_files_readback():
    """T5：prj `opened-files` 回读含两文件（页签集经受持久层）。"""
    last = []
    end = time.time() + 10
    while time.time() < end:
        last = prj_opened()
        if all(any(n in x for x in last) for n in (A, B)):
            break
        time.sleep(0.4)
    if not all(any(n in x for x in last) for n in (A, B)):
        raise TestError("prj opened-files 未含两文件：%r" % last)
    evi("T5 opened-files 回读", opened=last)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = file-open + 页签 DOM + data-prj-config-list（既有）", flush=True)
    ok = total = 0
    for name, fn in [
        ("T1 打开两文件：两页签名齐现", case_t1_two_tabs),
        ("T2 切到 a.txt：激活态 + 可见内容对应", case_t2_switch_a_content),
        ("T3 切到 b.txt：可见内容对应", case_t3_switch_b_content),
        ("T4 切回 a.txt：内容保持（回环）", case_t4_switch_back_keeps_state),
        ("T5 prj opened-files 回读含两文件", case_t5_opened_files_readback),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n窗口/标签切换后状态保持：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
