# -*- coding: utf-8 -*-
"""B9 · 语义/回环级：**项目树展示语义 + 预览页签（临时/固定）回环**（32-文件树与文件操作）。

覆盖（**拒绝"存在级"：断言过滤/排序**语义**与内容对应**）：
  FR1 隐藏条目过滤（FT-002-S02）：树中**不出现** `.chonkpilot` / `.ide`（点前缀隐藏），
      可见夹具条目（目录/文件）**出现**。
  FR2 根级排序语义：根级节点顺序 == **先目录后文件·组内字母序**（后端 `os.ReadDir` 仅为
      名称字母序，前端首屏载入后补 `sortChildren` 分组——I-172；与展开目录 FR2b 同口径）。
  FR2b 展开目录内排序（FT-002-S01 文档口径「先目录后文件、字母序」）：展开 `aaa_dir` 后其
      子节点顺序 == [目录 字母序…][文件 字母序…]（前端 `loadDirChildren → sortChildren`）。
  FR3 单击 → 临时页签 + 内容对应（FT-003-S01）：点 `aaa.txt` → 恰有 1 个 `is-temporary` 页签，
      名 == `aaa.txt`，可见面板内容含 MARK_A；并与 `filesys.content`（§2.1 消息面）**交叉验证**。
  FR4 临时页签覆盖（FT-003-S02）：再点 `mmm.txt` → 临时页签**仍为 1 个**且名/内容切到 MARK_B。
  FR5 双击转固定页签（FT-004-S01）：双击 `zzz.txt` → 该页签**非临时**（固定），可见内容含 MARK_C。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(c)`。夹具落在**临时 work-dir**（随 tmp_dir 回收，零残留）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §2 `filesys.content`（读回交叉验证）· DOM（`--test-port` /eval：`.tree-row` / `.tb-tab` / `.tab-panel`）。

运行：python run_sem_filetree_tree.py
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

WS = _h.tmp_dir("ck-semftt-ws-")
DD = _h.tmp_dir("ck-semftt-dd-")
HOME = _h.tmp_home()
MARK_A = "SEMFTT-alpha-7a1"
MARK_B = "SEMFTT-bravo-7b2"
MARK_C = "SEMFTT-charlie-7c3"

# 夹具：两个目录（字母序 aaa_dir < zzz_dir）+ 三个文件（aaa.txt < mmm.txt < zzz.txt）
# + 一个点前缀目录 `.ide`（应被隐藏）；`.chonkpilot` 由 harness.ensure_project_spec 自动创建。
# aaa_dir 内再放 [目录 sub_b/top_a + 文件 a1.txt/b1.txt] → 验展开目录内「先目录后文件·字母序」。
for d in ("aaa_dir", "zzz_dir", ".ide", "aaa_dir/sub_b", "aaa_dir/top_a"):
    os.makedirs(os.path.join(WS, d), exist_ok=True)
for name, mark in (("aaa.txt", MARK_A), ("mmm.txt", MARK_B), ("zzz.txt", MARK_C),
                   ("aaa_dir/a1.txt", "x"), ("aaa_dir/b1.txt", "y")):
    with open(os.path.join(WS, name), "w", encoding="utf-8") as f:
        f.write(mark + "\n")

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, _g.port), flush=True)

TMP_N = "document.querySelectorAll('.code-view .tb-bar.tb-bottom .tb-tab.is-temporary').length"
TMP_NAME_JS = ("(()=>{const t=document.querySelector('.code-view .tb-bar.tb-bottom .tb-tab.is-temporary .tb-name');"
               "return t?t.textContent.trim():''})()")
VISIBLE_CONTENT_JS = """(()=>{
  const panels=[...document.querySelectorAll('.tab-panel')].filter(p=>getComputedStyle(p).display!=='none');
  if(!panels.length) return '';
  const code=panels[panels.length-1].querySelector('.source-code');
  return code?code.textContent.trim():panels[panels.length-1].textContent.trim();
})()"""
EXPECT_ROOT = ["aaa_dir", "zzz_dir", "aaa.txt", "mmm.txt", "zzz.txt"]   # 根级：先目录后文件·组内字母序
EXPECT_A_DIR = ["sub_b", "top_a", "a1.txt", "b1.txt"]                   # 展开目录内：先目录后文件·字母序


def J(js):
    return _h._plain(c.eval(js))


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def wait_upto(js, ok, max_wait=15.0, interval=0.3):
    """轮询 JS 求值直到 ok(值) 为真，返回末次 JS 值。"""
    end = time.time() + max_wait
    v = J(js)
    while not ok(v) and time.time() < end:
        time.sleep(interval)
        v = J(js)
    return v


def wait_value(fn, ok, max_wait=15.0, interval=0.3):
    """轮询 python 取值函数 fn() 直到 ok(值) 为真，返回末次值（用于需再加工的读取）。"""
    end = time.time() + max_wait
    v = fn()
    while not ok(v) and time.time() < end:
        time.sleep(interval)
        v = fn()
    return v


def row_sel(rel):
    return '.tree-row[data-path="%s"]' % os.path.join(WS, rel).replace("\\", "/")


def labels_under(rel):
    """读某目录（rel="" = 根）**直接子节点**的显示名（按 DOM 顺序）。data-path 恒正斜杠。"""
    parent = os.path.join(WS, rel).replace("\\", "/") if rel else WS.replace("\\", "/")
    js = ("JSON.stringify([...document.querySelectorAll('.filetree-panel .tree-row')].filter(r=>{"
          "const p=r.getAttribute('data-path')||'';const i=p.lastIndexOf('/');"
          "return i>=0 && p.slice(0,i)===%s}).map(r=>{"
          "const n=r.querySelector('.node-label');return n?n.textContent.trim():''}))" % json.dumps(parent))
    return J(js) or []


def row_labels():
    """根级显示名（有界等待根树加载完成）。"""
    return wait_value(lambda: labels_under(""), lambda v: isinstance(v, list) and len(v) >= 5,
                      max_wait=20.0) or []


def expand_dir(rel):
    r = J("(function(){const r=document.querySelector(%s);if(!r)return 'no-row';"
         "const a=r.querySelector('.arrow');if(!a)return 'no-arrow';a.click();return 'ok';})()"
         % json.dumps(row_sel(rel)))
    if r != "ok":
        raise TestError("展开目录失败 %s: %s" % (rel, r))
    time.sleep(0.8)


def click_row(rel, dbl=False):
    evt = "dblclick" if dbl else "click"
    r = J("(function(){const r=document.querySelector(%s);if(!r)return 'no-row';"
         "r.dispatchEvent(new MouseEvent('%s',{bubbles:true,cancelable:true}));return 'ok';})()"
         % (json.dumps(row_sel(rel)), evt))
    if r != "ok":
        raise TestError("点击树行失败 %s: %s" % (rel, r))
    time.sleep(0.8)


def pinned(name):
    return J("(()=>{for(const t of document.querySelectorAll('.code-view .tb-bar.tb-bottom .tb-tab')){"
             "const n=t.querySelector('.tb-name');if(n&&n.textContent.trim()===%s"
             "&&!t.classList.contains('is-temporary'))return true}return false})()" % json.dumps(name))


def fs_content(rel):
    r = c.req("filesys.content", {"work_dir": WS, "path": os.path.join(WS, rel)}) or {}
    return r.get("content") or ""


def wait_tab(name, mark):
    """等临时页签名 == name 且可见内容含 mark。"""
    n = wait_upto(TMP_N, lambda v: int(v or 0) == 1, max_wait=15.0)
    nm = wait_upto(TMP_NAME_JS, lambda v: v == name, max_wait=15.0)
    txt = wait_upto(VISIBLE_CONTENT_JS, lambda v: isinstance(v, str) and mark in v, max_wait=15.0) or ""
    return n, nm, txt


def case_fr1_hidden_filtered():
    """FR1：`.chonkpilot` / `.ide` 不出现在树中；可见夹具条目出现。"""
    labels = row_labels()
    hidden = [n for n in labels if n in (".chonkpilot", ".ide")]
    if hidden:
        raise TestError("隐藏条目泄漏进树：%r（labels=%r）" % (hidden, labels))
    missing = [n for n in EXPECT_ROOT if n not in labels]
    if missing:
        raise TestError("可见夹具条目未出现：%r（labels=%r）" % (missing, labels))
    evi("FR1 隐藏条目过滤", labels=labels, hidden=[])


def case_fr2_root_order():
    """FR2：根级顺序 == 先目录后文件·组内字母序（与期望集完全一致）。"""
    labels = row_labels()
    if labels != EXPECT_ROOT:
        raise TestError("根级顺序不符：%r（期望 %r）" % (labels, EXPECT_ROOT))
    if not (labels[:2] == ["aaa_dir", "zzz_dir"] and labels[2:] == ["aaa.txt", "mmm.txt", "zzz.txt"]):
        raise TestError("根级目录/文件分组不符：%r" % (labels,))
    evi("FR2 根级排序语义（先目录后文件·字母序）", order=labels)


def case_fr2b_expanded_dir_order():
    """FR2b：展开 aaa_dir → 子节点顺序 == [目录字母序][文件字母序]（先目录后文件）。"""
    expand_dir("aaa_dir")
    got = wait_value(lambda: labels_under("aaa_dir"), lambda v: v == EXPECT_A_DIR, max_wait=15.0) or []
    if got != EXPECT_A_DIR:
        raise TestError("展开目录内顺序不符：%r（期望 %r）" % (got, EXPECT_A_DIR))
    if not (got[:2] == ["sub_b", "top_a"] and got[2:] == ["a1.txt", "b1.txt"]):
        raise TestError("目录/文件分组不符：%r" % (got,))
    evi("FR2b 展开目录内排序语义（先目录后文件·字母序）", order=got)


def case_fr3_single_click_temp_tab():
    """FR3：单击 aaa.txt → 临时页签名/内容对应 + filesys.content 交叉验证。"""
    click_row("aaa.txt")
    n, nm, txt = wait_tab("aaa.txt", MARK_A)
    if int(n or 0) != 1 or nm != "aaa.txt" or MARK_A not in txt:
        raise TestError("临时页签不符：n=%r name=%r content=%r" % (n, nm, txt[:120]))
    fc = fs_content("aaa.txt")
    if MARK_A not in fc:
        raise TestError("filesys.content 交叉验证不符：%r" % fc)
    evi("FR3 单击临时页签+内容对应", name=nm, content_has_mark=True, fs_content_ok=MARK_A in fc)


def case_fr4_temp_tab_overwrite():
    """FR4：再点 mmm.txt → 临时页签仍 1 个，名/内容切到 MARK_B（覆盖语义）。"""
    click_row("mmm.txt")
    n, nm, txt = wait_tab("mmm.txt", MARK_B)
    if int(n or 0) != 1 or nm != "mmm.txt" or MARK_B not in txt or MARK_A in txt:
        raise TestError("临时页签覆盖不符：n=%r name=%r（含A=%s 含B=%s）"
                        % (n, nm, MARK_A in txt, MARK_B in txt))
    evi("FR4 临时页签覆盖", n=n, name=nm, content_has_B=True, content_has_A=False)


def case_fr5_double_click_pin():
    """FR5：双击 zzz.txt → 该页签固定（非临时）+ 可见内容含 MARK_C。"""
    click_row("zzz.txt", dbl=True)
    wait_value(lambda: pinned("zzz.txt"), lambda v: bool(v), max_wait=15.0)
    txt = wait_upto(VISIBLE_CONTENT_JS, lambda v: isinstance(v, str) and MARK_C in v, max_wait=15.0) or ""
    if not pinned("zzz.txt") or MARK_C not in txt:
        raise TestError("双击未转固定页签：pinned=%r content=%r" % (pinned("zzz.txt"), txt[:120]))
    evi("FR5 双击转固定页签", pinned_zzz=True, content_has_C=True)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = 树行点击/双击 + filesys.content（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("FR1 隐藏条目过滤：.chonkpilot/.ide 不出现、可见条目出现", case_fr1_hidden_filtered),
        ("FR2 根级排序语义：先目录后文件·组内字母序", case_fr2_root_order),
        ("FR2b 展开目录内排序：先目录后文件·组内字母序", case_fr2b_expanded_dir_order),
        ("FR3 单击→临时页签 + 内容对应（含 filesys.content 交叉验证）", case_fr3_single_click_temp_tab),
        ("FR4 单击另一文件→临时页签覆盖（仍 1 个，内容切换）", case_fr4_temp_tab_overwrite),
        ("FR5 双击→转固定页签 + 内容对应", case_fr5_double_click_pin),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n项目树展示语义 + 预览页签回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
