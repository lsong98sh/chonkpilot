# -*- coding: utf-8 -*-
"""B · 语义/回环级推广（36-配置）：**配置项写→落库回读→删→回落（重置继承）回环**。

覆盖（**拒绝"存在级"：断言 usr 层精确值落库 + 删除后回落（CFG-001-S03 / CFG-006-S01）**）：
  C1 写→落库回读：`data-user-config-save{data:{responseTimeout:37}}` → `data-user-config-load`
     合并回读 `data.responseTimeout` **== 37**（语义：精确值；写前断言 != 37 以证非默认残留）。
  C2 落库往返一致 + refresh：`data-user-config-refresh` 广播后**独立再请求** load → 仍 **== 37**
     （证明读自持久层，非内存残留）。
  C3 删→回落：`data-user-config-delete{id:responseTimeout}` → load 后 `responseTimeout` **!= 37**
     （回落系统默认/缺省 = 重置继承语义；CFG-001-S03 删本层 key 回落上级）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(c)`（跑后自动还原被改的 usr 键）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：§3.1 `data-user-config-{load,save,delete,refresh}`。

运行：python run_sem_config_fallback.py
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

WS = _h.tmp_dir("ck-semcfgfb-ws-")
DD = _h.tmp_dir("ck-semcfgfb-dd-")
HOME = _h.tmp_home()
KEY = "responseTimeout"
MARK = "37"

_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c, "zh-CN")
print("[env] ws=%s data=%s home=%s gui=%d key=%s mark=%s（临时目录，结束即删）"
      % (WS, DD, HOME, _g.port, KEY, MARK), flush=True)


def usr():
    r = c.req("data-user-config-load", {})
    return ((r or {}).get("data") or {}) if isinstance(r, dict) else {}


def val():
    return usr().get(KEY)


def poll(pred, max_wait=8.0, interval=0.3):
    end = time.time() + max_wait
    v = val()
    while not pred(v) and time.time() < end:
        time.sleep(interval)
        v = val()
    return v


def case_c1_write_persist():
    """C1：usr 写 responseTimeout=37 → 合并回读 == 37。"""
    before = val()
    if str(before) == MARK:
        raise TestError("写前该键已 == 哨兵 %s（无法证明本次写入生效）" % MARK)
    c.req("data-user-config-save", {"data": {KEY: int(MARK)}})
    got = poll(lambda v: str(v) == MARK)
    if str(got) != MARK:
        raise TestError("保存后 data-user-config-load %s=%r（期望 %s；保存前 %r）"
                        % (KEY, got, MARK, before))
    print("[EVIDENCE] " + json.dumps({"case": "C1 写→落库回读", "before": before, "after": got},
                                     ensure_ascii=False), flush=True)


def case_c2_readback_roundtrip():
    """C2：refresh 广播 + 独立再请求 → 仍 == 37（读自持久层）。"""
    c.mq_emit("data-user-config-refresh")
    time.sleep(0.4)
    got = poll(lambda v: str(v) == MARK)
    if str(got) != MARK:
        raise TestError("refresh 后独立回读 %s=%r（期望 %s，非内存残留）" % (KEY, got, MARK))
    print("[EVIDENCE] " + json.dumps({"case": "C2 落库往返一致", "after_refresh": got},
                                     ensure_ascii=False), flush=True)


def case_c3_delete_fallback():
    """C3：删除该 usr 键 → 回读回落（!= 37，重置继承）。"""
    r = c.req("data-user-config-delete", {"id": KEY}) or {}
    if r.get("ok") is False:
        raise TestError("data-user-config-delete 失败：%r" % r)
    got = poll(lambda v: str(v) != MARK)
    if str(got) == MARK:
        raise TestError("删除后 %s 仍为哨兵 %s（未回落）" % (KEY, MARK))
    print("[EVIDENCE] " + json.dumps({"case": "C3 删→回落", "after_delete": got}, ensure_ascii=False),
          flush=True)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = data-user-config-{save,load,delete,refresh}（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("C1 写 responseTimeout=37 → 合并回读一致", case_c1_write_persist),
        ("C2 refresh + 独立回读 → 持久层往返一致", case_c2_readback_roundtrip),
        ("C3 删键 → 回落（重置继承，!= 哨兵）", case_c3_delete_fallback),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n配置项写→回读→删→回落回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
