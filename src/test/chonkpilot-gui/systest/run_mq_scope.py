# -*- coding: utf-8 -*-
"""MQ 分发出口 S2：**workdir 级主题只投同 workdir 的窗口**（49 §2.22.7 S2 · 11-MQ §5.1）。

唯一准则 = `docs/spec/60-reference/61-消息一览.md`：驱动与断言一律走 **mq 消息**
（50-测试体系 §1）。本套件的验收口径（49 §2.22.7 S2 表）：

  ① 同 workdir 双窗**都收到** workdir 级事件（当前进程内的主窗 + 两个对话窗）；
  ② `work_dir` 指向**别处**的 workdir 级事件**不进**本 workdir 的任何窗口
     （= 收紧前后的行为差异：S1 是「无 instance_id → 全投」）；
  ③ instance 级（载荷带 `instance_id`）仍是**精确投该实例**（S1 规则回归）；
  ④ `--test-port` 重启后上述行为仍成立（策略无跨重启内存态）。

**口径说明（为何用 `instance_id: ""` 驱动）**：
`filesys.changed` / `filesys.watch-error` 的**真实**载荷带 `work_dir`，`instance_id` = watch 声明者；
`filesys` 的 dirWatcher 在**无声明者**时**不带 `instance_id`** 直接广播（`src/lib/filesys/watcher.go`
的 `emit`）—— 这类「无 instance 归属」的 workdir 级事件正是 S2 要按 workdir 收口的那一类。
测试通道 `/publish` 经桥 `injectInstance` 会**幂等兜底补** `instance_id`（前端同款），故本套件
显式给 `instance_id: ""`（键存在 → 不覆盖）来构造与「无声明者」**同形**的载荷，不新增/修改消息面。

**为何不做「同进程另一 workdir 的第二主窗」**：多项目（`gui.dir.open`）虽可在同进程开第二个
workdir 的主窗，但 `--test-port` 只为**首个**项目主窗 + 对话窗登记测试目标（`AttachTestServer`
的 `HasTestServer()` 门控），第二主窗**不可观测**；而跨进程 = 两条独立总线，无法验证同一条总线上的
过滤。故「别 workdir」一侧以 `work_dir` 指向真实临时目录（`OTHER_WS`）的方式断言 —— 断言点相同
（策略按 workdir 收口），且完全是 mq 驱动 + mq 断言。

运行：python src\\test\\chonkpilot-gui\\systest\\run_mq_scope.py
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case  # noqa: E402
import harness as _h  # noqa: E402  按需加载 + 结束即回收（51-FP与测试映射 §测试资源规范）

# 本进程 workdir（窗内） + 「别处」workdir（真实临时目录，但不属于本进程任何窗口）。
WS = _h.tmp_dir("ck-mqscope-ws-")
OTHER_WS = _h.tmp_dir("ck-mqscope-other-")
HOME = _h.tmp_home()
for _n in ("scope-a.txt", "scope-b.txt"):
    with open(os.path.join(WS, _n), "w", encoding="utf-8") as _f:
        _f.write("scope\n")

_g = None    # GUIHandle
_m = None    # 主窗口客户端

FS_TOPIC = "filesys.changed"          # workdir 级（61 §2.4）
PRJCFG_TOPIC = "data-prj-config-refresh"  # workdir 级（61 §3.1 项目级）
INST_TOPIC = "plugin-ui-event"        # instance 级（61 §7.2：载荷可带 instance_id，桥按精确相等过滤）


# ══════════════════════════════════════════════════════════════════
# 基础工具（与 run_mw.py 同口径的最小集）
# ══════════════════════════════════════════════════════════════════

def cl(wid=""):
    return ChonkClient(base=_m.base, timeout=60, window_id=wid)


def inst(wid=""):
    """该窗口的前端实例 id（`window.__chonkpilotInstanceId`）。

    EvalWithResult 可能双重编码（返回值带引号）→ 与 run_mw.py 同口径先 `_plain` 解包，
    否则拿到的 `"abc"` 与 sink id `abc` 不相等（实例规则会判为不匹配）。
    """
    try:
        v = _h._plain(cl(wid).eval("window.__chonkpilotInstanceId", 15000))
    except Exception:
        return ""
    return str(v or "").strip().strip('"')


def req(wid, typ, payload=None, timeout=30000):
    r = cl(wid).publish(typ, dict(payload or {}), timeout)
    if not isinstance(r, dict) or r.get("ok") is False:
        raise TestError("%s 失败: %s" % (typ, (r or {}).get("errors")))
    res = r.get("result")
    if isinstance(res, dict) and res.get("ok") is False:
        raise TestError("%s 失败: %s" % (typ, res.get("error")))
    return res


def wins():
    return (_m.req("gui.window.list", {}) or {}).get("windows") or []


def poll(fn, ok, timeout=25.0, interval=0.3):
    deadline = time.time() + timeout
    v = fn()
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = fn()
    return v


def sid(tag):
    return "mqscope-%s-%d" % (tag, int(time.time() * 1000))


def close_all_chats(rounds=3):
    for _ in range(max(1, int(rounds))):
        left = wins()
        if not left:
            return
        for w in left:
            cl(w["window_id"]).publish("gui.window.status", {"command": "close"}, 10000)
        poll(wins, lambda v: not v, timeout=20.0)


def wait_win_ready(wid, timeout=120.0):
    """等对话窗**前端就绪**（测试通道 + `window.mq` 可用）。

    只看 `1+1` 不够：页面刚导航完时 `window.mq` 可能尚未注入 → 随后的 `mq.on` 注册会
    撞上 `ExecuteScript timed out`（UI 线程忙）。两步都过才算就绪。
    """
    js = ("!!(window.mq && typeof window.mq.on === 'function' "
          "&& document.querySelector('.chat-only-view'))")

    def probe():
        try:
            v = _h._plain(cl(wid).eval(js, 10000))
        except Exception:
            return False
        # EvalWithResult 可能把布尔值当字符串返回（`"true"`）→ 与 `_h._plain` 同口径兼容
        if isinstance(v, str):
            return v.strip().lower() in ("true", "1")
        return bool(v)
    if not poll(probe, lambda v: v, timeout=timeout, interval=0.5):
        raise TestError("对话窗口 %s 前端未就绪（%ss）" % (wid, timeout))
    return True


def capture(wid, topics, tries=4):
    """注册 mq 监听（瞬态超时有界重试；断言口径不变）。"""
    for i in range(tries):
        try:
            cl(wid).mq_on_capture(topics)
            return
        except Exception:
            if i == tries - 1:
                raise
            time.sleep(1.0)


def open_two_chats():
    """开两个对话窗（同 workdir），返回 (window_id1, window_id2)。"""
    close_all_chats()
    wids = []
    for i in (1, 2):
        s = sid("w%d" % i)
        req("", "data-session-ensure-session", {"session_id": s})
        r = req("", "gui.window.open-chat", {"session_id": s})
        if not r.get("ok"):
            raise TestError("open-chat 失败: %s" % (r,))
        wids.append(r["window_id"])
        wait_win_ready(r["window_id"])
    if wids[0] == wids[1]:
        raise TestError("两个对话窗 window_id 应不同: %s" % wids)
    return wids[0], wids[1]


def pub_workdir_event(wid, topic, work_dir, probe, extra=None):
    """以某窗口身份发布一条**无 instance 归属**的 workdir 级事件（载荷与「无 watch 声明者」同形）。

    `instance_id: ""` 显式置空 → 桥 `injectInstance` 幂等兜底不覆盖（键存在）→ 与 filesys 的
    dirWatcher 无声明者广播逐字同形（见模块 docstring「口径说明」）。
    """
    payload = {"instance_id": "", "work_dir": work_dir, "probe": probe}
    payload.update(extra or {})
    return cl(wid).publish(topic, payload)


def probes(cli, topic):
    """该窗口捕获到的某 topic 事件中的 probe 标记集合（读取失败 = 暂无）。"""
    out = set()
    try:
        evs = cli.events_of(topic, clear=False)
    except Exception:
        return out
    for e in evs:
        pl = e.get("payload") if isinstance(e, dict) else None
        if isinstance(pl, str):
            try:
                pl = json.loads(pl)
            except Exception:
                pl = None
        if isinstance(pl, dict) and pl.get("probe"):
            out.add(str(pl["probe"]))
    return out


# ══════════════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════════════

def case_same_workdir_both_windows():
    """S2-① 同 workdir 双窗**都收到** workdir 级事件（主窗 + 另一对话窗）。

    驱动：对话窗 w2 发布 `instance_id:""` + `work_dir=WS` 的 `filesys.changed` /
    `data-prj-config-refresh`；断言：主窗与 w1 **都收到**（w2 因自发布防环被吞，不参与断言）。
    """
    w1, w2 = open_two_chats()
    cm, c1, c2 = cl(""), cl(w1), cl(w2)
    for wid in ("", w1, w2):  # c2 也订阅：用于断言「发布方自己那条被自发布防环吞掉」
        capture(wid, [FS_TOPIC, PRJCFG_TOPIC])
    tag = "same-%d" % int(time.time() * 1000)
    pairs = [
        (FS_TOPIC, {"path": os.path.join(WS, "scope-a.txt").replace("\\", "/"), "operation": "write"}),
        (PRJCFG_TOPIC, {"id": "mqscope.probe", "op": "save"}),
    ]
    for i, (topic, extra) in enumerate(pairs):
        probe = "%s-%d" % (tag, i)
        pub_workdir_event(w2, topic, WS, probe, extra)
        for name, c in (("主窗", cm), ("对话窗w1", c1)):
            got = poll(lambda: probes(c, topic), lambda v: probe in v, timeout=15.0)
            if probe not in got:
                raise TestError("%s 未收到同 workdir 的 %s（probe=%s，实际 %v）" % (name, topic, probe, got))
    # 自发布防环：发布方 w2 不应收到自己那条（S1 规则不变）。
    time.sleep(0.8)
    if c2.events_of(FS_TOPIC, clear=False) or c2.events_of(PRJCFG_TOPIC, clear=False):
        raise TestError("发布方窗口不该收到自己发布的事件（自发布防环失效）")
    close_all_chats()


def case_other_workdir_not_delivered():
    """S2-② `work_dir` 指向**别处** → 本 workdir 的窗口**都收不到**（收紧前后的行为差异）。

    对照：同一轮里再发一条 `work_dir=WS` 的**同形**事件（正控制）—— 证明投递链路正常，
    排除「什么都没到」造成的假绿。
    """
    w1, w2 = open_two_chats()
    cm, c1 = cl(""), cl(w1)
    for wid in ("", w1):
        capture(wid, [FS_TOPIC])
    tag = "other-%d" % int(time.time() * 1000)
    ctrl, neg = tag + "-ctrl", tag + "-neg"
    # 正控制：同 workdir → 应到。
    pub_workdir_event(w2, FS_TOPIC, WS, ctrl,
                      {"path": os.path.join(WS, "scope-b.txt").replace("\\", "/"), "operation": "write"})
    # 反例：work_dir = 别处（真实临时目录）→ 不应到任何本 workdir 窗口。
    pub_workdir_event(w2, FS_TOPIC, OTHER_WS, neg,
                      {"path": os.path.join(OTHER_WS, "x.txt").replace("\\", "/"), "operation": "write"})
    got = poll(lambda: probes(cm, FS_TOPIC), lambda v: ctrl in v, timeout=15.0)
    if ctrl not in got:
        raise TestError("正控制失败：同 workdir 事件未到达主窗（实际 %v）" % (got,))
    time.sleep(1.5)  # 给「若未收紧」足够到达
    leak_m = [p for p in probes(cm, FS_TOPIC) if p == neg]
    leak_1 = [p for p in probes(c1, FS_TOPIC) if p == neg]
    if leak_m or leak_1:
        raise TestError("work_dir 指向别处的事件串到了本 workdir 窗口（主窗=%s w1=%s）" % (leak_m, leak_1))
    close_all_chats()


def case_instance_scope_unchanged():
    """S2-③ instance 级（载荷带 `instance_id`）回归：命中 → 只该窗；不命中 → 都不收。

    驱动 = `plugin-ui-event`（61 §7.2：载荷**可带** `instance_id`，桥按精确相等过滤；
    有现成生产者路径 `plugin-` 前缀 → 桥直通总线，测试通道可驱动）。
    """
    w1, w2 = open_two_chats()
    i1 = inst(w1)
    if not i1:
        raise TestError("取不到对话窗实例 id")
    cm, c1, c2 = cl(""), cl(w1), cl(w2)
    for wid in ("", w1, w2):
        capture(wid, [INST_TOPIC])
    tag = "inst-%d" % int(time.time() * 1000)
    # ① 定向给 w1 → 只 w1 收（发布方 = 主窗：主窗自身被自发布防环吞掉，w2 同 workdir 也**不该**收到）
    env = cl("").publish(INST_TOPIC, {"id": "mqscope-inst", "instance_id": i1, "topic": "mqscope",
                                      "payload": {"probe": tag}, "probe": tag}, 15000)
    if not isinstance(env, dict) or env.get("ok") is False:
        raise TestError("plugin-ui-event 发布失败: %r" % (env,))
    if not poll(lambda: probes(c1, INST_TOPIC), lambda v: tag in v, timeout=15.0):
        raise TestError("instance 定向事件未到目标窗口 w1（主窗捕获=%s w2 捕获=%s）"
                        % (probes(cm, INST_TOPIC), probes(c2, INST_TOPIC)))
    time.sleep(1.2)
    if probes(cm, INST_TOPIC) or probes(c2, INST_TOPIC):
        raise TestError("instance 定向事件串到非目标窗口（主窗=%s w2=%s）"
                        % (probes(cm, INST_TOPIC), probes(c2, INST_TOPIC)))
    # ② 不存在的实例 → 谁都不收
    ghost = tag + "-ghost"
    cl(w2).publish(INST_TOPIC, {"id": "mqscope-inst", "instance_id": "mqscope-ghost",
                                "topic": "mqscope", "payload": {"probe": ghost}, "probe": ghost}, 15000)
    time.sleep(1.5)
    for name, c in (("主窗", cm), ("w1", c1), ("w2", c2)):
        if ghost in probes(c, INST_TOPIC):
            raise TestError("不存在的实例 id 不应投给任何窗口（%s 收到）" % name)
    close_all_chats()


def case_restart_still_holds():
    """S2-④ 重启后仍成立：同 workdir 双窗仍收到；`work_dir` 指向别处仍不进窗。

    策略无跨重启内存态（反查视图 = framework instance.Manager，随进程重建）→ 重启后行为应一致。
    """
    global _g, _m
    try:
        _g.stop()
    except Exception:
        pass
    _g = _h.start_gui(port=_h.free_port(), work_dir=WS, home=HOME, ready_timeout=90)
    _m = _g.client
    # 新实例首屏稳定化（`/eval` 在 UI 线程饥饿期会 ExecuteScript timed out；与 harness 同口径）
    if hasattr(_h, "wait_probe"):
        _h.wait_probe(_m, ["!!document.querySelector('.panel-inner')"], timeout=60.0)
    if hasattr(_h, "wait_idle"):
        _h.wait_idle(_m, max_wait=30.0)
    case_same_workdir_both_windows()
    case_other_workdir_not_delivered()


# ══════════════════════════════════════════════════════════════════
# 主流程
# ══════════════════════════════════════════════════════════════════

CASES = [
    ("S2-① 同 workdir 双窗都收到 workdir 级事件", case_same_workdir_both_windows),
    ("S2-② work_dir 指向别处 → 不进本 workdir 任何窗口", case_other_workdir_not_delivered),
    ("S2-③ instance 级定向投递回归（只投该实例）", case_instance_scope_unchanged),
    ("S2-④ 重启后行为仍成立", case_restart_still_holds),
]


def main():
    global _g, _m
    ok = 0
    try:
        _g = _h.start_gui(port=_h.free_port(), work_dir=WS, home=HOME, ready_timeout=90)
        _m = _g.client
        for name, fn in CASES:
            ok += 1 if run_case(name, fn) else 0
    finally:
        try:
            if _g is not None:
                _g.stop()
        except Exception:
            pass
    total = len(CASES)
    print("\n== MQ 分发出口 S2（workdir 级只投同 workdir）：%d/%d 通过 ==" % (ok, total))
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
