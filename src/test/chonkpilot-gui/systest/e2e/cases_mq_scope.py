# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊴：**MQ 分发出口 S2：workdir 级主题只投同 workdir 的窗口**
（迁移自 run_mq_scope ①②③；重启后 ④ 见 `R-MQ-SCOPE-01`）。

* 同 workdir 的主窗 + 两个对话窗**都收到** workdir 级事件（`filesys.changed` /
  `data-prj-config-refresh`，载荷 `instance_id:""` 与无 watch 声明者同形）；
* `work_dir` 指向别处 → 本 workdir 任何窗口都收不到（同轮正控制证明投递链路正常）；
* instance 级 `plugin-event{instance_id}` → 只目标窗口收；不存在的 id → 无人收；
* 发布方窗口不收到自己发布的事件（自发布防环）。
"""

import json
import os
import tempfile
import time

from .framework import case
from chonk_client import ChonkClient

FS_TOPIC = "filesys.changed"
PRJCFG_TOPIC = "data-prj-config-refresh"
INST_TOPIC = "plugin-event"


def _client(env, wid=""):
    return ChonkClient(base=env.client.base, timeout=env.client.timeout, window_id=wid)


def _plain(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            p = json.loads(v)
        except Exception:
            return v
        if p == v:
            return v
        v = p
    return v


def _wins(env):
    return (env.req("gui.window.list", {}) or {}).get("windows") or []


def _close_all(env):
    for _ in range(3):
        ws = _wins(env)
        if not ws:
            return
        for w in ws:
            try:
                _client(env, w["window_id"]).publish("gui.window.status",
                                                     {"command": "close"}, 10000)
            except Exception:
                pass
        env.wait(lambda: True if not _wins(env) else None, "关闭全部对话窗", timeout=20)


def _wait_ready(env, wid, timeout=90):
    cl = _client(env, wid)

    def _probe():
        try:
            v = _plain(cl.eval("!!(window.mq && typeof window.mq.on === 'function' "
                               "&& document.querySelector('.chat-only-view'))", 10000))
        except Exception:
            return False
        if isinstance(v, str):
            return v.strip().lower() in ("true", "1")
        return bool(v)
    return env.wait(lambda: True if _probe() else None, "对话窗 %s 就绪" % wid,
                    timeout=timeout)


def _open_two(env):
    _close_all(env)
    wids = []
    for i in (1, 2):
        sid = "mqscope-e2e-%s-%d-%d" % (env.run_id, int(time.time() * 1000), i)
        env.req("data-session-ensure-session", {"data": {"session_id": sid}})
        r = env.req("gui.window.open-chat", {"session_id": sid}) or {}
        if not r.get("ok"):
            raise AssertionError("open-chat 失败：%r" % (r,))
        _wait_ready(env, r["window_id"])
        wids.append(r["window_id"])
    return wids[0], wids[1]


def _capture(cl, topics):
    for i in range(4):
        try:
            cl.mq_on_capture(list(topics))
            return
        except Exception:
            if i == 3:
                raise
            time.sleep(1.0)


def _probes(cl, topic):
    out = set()
    try:
        evs = cl.events_of(topic, clear=False)
    except Exception:
        return out
    for e in evs:
        pl = (e or {}).get("payload")
        if isinstance(pl, str):
            try:
                pl = json.loads(pl)
            except Exception:
                pl = None
        if isinstance(pl, dict) and pl.get("probe"):
            out.add(str(pl["probe"]))
    return out


def _inst(env, wid):
    return str(_plain(_client(env, wid).eval("window.__chonkpilotInstanceId", 15000))
               or "").strip().strip('"')


def _pub(cl, topic, payload, strict=False):
    # workdir 级广播主题没有请求-响应方（persist 不返回）→ 信封可能报 timeout，
    # 但事件已上总线；只对 instance 级驱动（plugin-event）要求 ok。
    r = cl.publish(topic, payload, 15000)
    if strict and (not isinstance(r, dict) or r.get("ok") is False):
        raise AssertionError("%s 发布失败：%r" % (topic, r))
    return r


@case("S-MQ-SCOPE-01", "消息", "workdir 级事件只投同 workdir 窗口；instance 级精确投递；自发布防环",
      stage="seeded", order=392, fp="49 §2.22.7 S2 · run_mq_scope ①②③",
      desc="开两个同 workdir 对话窗：w2 发布 work_dir=ws 的 filesys.changed / data-prj-config-refresh"
           "（instance_id=\"\"）→ 主窗与 w1 均收到、w2 自己收不到；work_dir=别处 → 均收不到"
           "（同轮同 workdir 正控制到达）；plugin-event{instance_id=w1} → 仅 w1；幽灵实例 → 无人收。")
def s_mq_scope_01(env):
    other = tempfile.mkdtemp(prefix="e2e-mqscope-other-")
    try:
        w1, w2 = _open_two(env)
        cm, c1, c2 = env.client, _client(env, w1), _client(env, w2)
        for cl in (cm, c1, c2):
            _capture(cl, [FS_TOPIC, PRJCFG_TOPIC, INST_TOPIC])

        # ① 同 workdir 双窗都收到
        tag = "same-%d" % int(time.time() * 1000)
        pairs = [(FS_TOPIC, {"path": env.posix(env.path("scope-a.txt")), "operation": "write"}),
                 (PRJCFG_TOPIC, {"id": "mqscope.probe", "op": "save"})]
        for i, (topic, extra) in enumerate(pairs):
            probe = "%s-%d" % (tag, i)
            payload = {"instance_id": "", "work_dir": env.workspace, "probe": probe}
            payload.update(extra)
            _pub(c2, topic, payload)
            for name, cl in (("主窗", cm), ("对话窗 w1", c1)):
                env.check("① %s 收到同 workdir 的 %s" % (name, topic),
                          probe in env.wait(lambda: (lambda v: v if probe in v else None)(
                              _probes(cl, topic)), "%s %s probe" % (name, topic), timeout=20),
                          probe)
        time.sleep(0.8)
        env.check("① 发布方窗口不收到自己发布的 workdir 事件（自发布防环）",
                  not _probes(c2, FS_TOPIC) and not _probes(c2, PRJCFG_TOPIC))

        # ② work_dir 指向别处 → 不到达
        r = _wins(env)
        w1b = next((w["window_id"] for w in r if w.get("window_id") != w2), w1)
        c1b = _client(env, w1b)
        _capture(c1b, [FS_TOPIC])
        tag2 = "other-%d" % int(time.time() * 1000)
        ctrl, neg = tag2 + "-ctrl", tag2 + "-neg"
        _pub(c2, FS_TOPIC, {"instance_id": "", "work_dir": env.workspace, "probe": ctrl,
                            "path": env.posix(env.path("scope-b.txt")), "operation": "write"})
        _pub(c2, FS_TOPIC, {"instance_id": "", "work_dir": other, "probe": neg,
                            "path": os.path.join(other, "x.txt").replace("\\", "/"),
                            "operation": "write"})
        env.check("② 正控制：同 workdir 事件到达主窗",
                  ctrl in env.wait(lambda: (lambda v: v if ctrl in v else None)(
                      _probes(cm, FS_TOPIC)), "正控制 probe", timeout=20), ctrl)
        time.sleep(1.5)
        env.check("② work_dir 指向别处 → 主窗/对话窗都收不到",
                  neg not in _probes(cm, FS_TOPIC) and neg not in _probes(c1b, FS_TOPIC),
                  {"main": sorted(_probes(cm, FS_TOPIC)), "w1": sorted(_probes(c1b, FS_TOPIC))})

        # ③ instance 级精确投递
        i1 = _inst(env, w1)
        env.check("③ 取到对话窗实例 id", bool(i1), i1)
        if i1:
            _capture(cm, [INST_TOPIC])
            _capture(c1, [INST_TOPIC])
            _capture(c2, [INST_TOPIC])
            tag3 = "inst-%d" % int(time.time() * 1000)
            _pub(cm, INST_TOPIC, {"id": "mqscope-inst", "instance_id": i1,
                                  "topic": "mqscope", "payload": {"probe": tag3},
                                  "probe": tag3})
            env.check("③ 定向实例事件到达 w1",
                      tag3 in env.wait(lambda: (lambda v: v if tag3 in v else None)(
                          _probes(c1, INST_TOPIC)), "w1 定向事件", timeout=20), tag3)
            time.sleep(1.0)
            env.check("③ 定向事件不串到非目标窗口",
                      tag3 not in _probes(cm, INST_TOPIC)
                      and tag3 not in _probes(c2, INST_TOPIC),
                      {"main": sorted(_probes(cm, INST_TOPIC)),
                       "w2": sorted(_probes(c2, INST_TOPIC))})
            ghost = tag3 + "-ghost"
            _pub(c2, INST_TOPIC, {"id": "mqscope-inst", "instance_id": "mqscope-ghost",
                                  "topic": "mqscope", "payload": {"probe": ghost},
                                  "probe": ghost}, strict=True)
            time.sleep(1.5)
            env.check("③ 幽灵实例 id → 任何窗口都不收",
                      all(ghost not in _probes(cl, INST_TOPIC) for cl in (cm, c1, c2)))
    finally:
        try:
            _close_all(env)
        except Exception:
            pass
        try:
            os.rmdir(other)
        except OSError:
            pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("MQ 分发组无前端错误", len(errs) == 0, errs[:3])

@case("S-MQ-SCOPE-02", "消息", "一个窗口多 workdir：项目注册 / 会话库隔离 / 未注册路径拒绝",
      stage="seeded", order=393, fp="SESSION-SCOPE-1 · gui.project.* / data-* 显式 work_dir",
      desc="gui.project.add 注册临时项目 B（不新开窗口）→ 项目列表含 B；"
           "带 work_dir=B 建会话 → B 列表可见、默认项目 A 列表不可见；"
           "未注册绝对路径的 data-session-list 明确失败；移除项目后列表不再含 B。")
def s_mq_scope_02(env):
    import shutil
    bdir = tempfile.mkdtemp(prefix="e2e-multi-wd-b-")
    other = tempfile.mkdtemp(prefix="e2e-multi-wd-unreg-")
    sid = "mqscope-multi-%s-%d" % (env.run_id, int(time.time() * 1000))
    norm = lambda p: os.path.normcase(os.path.normpath(str(p or "")))

    def _ids(resp):
        rows = (resp or {}).get("list") or []
        return {str(r.get("session_id") or r.get("id") or "") for r in rows if r}

    try:
        r = env.req("gui.project.add", {"work_dir": bdir}) or {}
        env.check("gui.project.add 注册 B（返回项目）",
                  bool(r.get("ok")) and norm((r.get("project") or {}).get("work_dir")) == norm(bdir), r)
        projects = (env.req("gui.project.list", {}) or {}).get("projects") or []
        env.check("项目列表含 B", any(norm(p.get("work_dir")) == norm(bdir) for p in projects),
                  [p.get("work_dir") for p in projects])

        env.req("data-session-ensure-session", {"work_dir": bdir, "session_id": sid})
        la = _ids(env.req("data-session-list", {}))
        lb = _ids(env.req("data-session-list", {"work_dir": bdir}))
        env.check("B 会话在 B 库可见", sid in lb, sorted(lb))
        env.check("B 会话不污染默认项目 A 的库", sid not in la, sorted(la))

        rejected = False
        try:
            env.req("data-session-list", {"work_dir": other})
        except Exception:
            rejected = True
        env.check("未注册 workdir 的 data 请求明确拒绝", rejected)
    finally:
        try:
            env.req("data-session-delete", {"work_dir": bdir, "id": sid})
        except Exception:
            pass
        try:
            env.req("gui.project.remove", {"work_dir": bdir})
        except Exception:
            pass
        projects = (env.req("gui.project.list", {}) or {}).get("projects") or []
        env.check("移除项目后列表不再含 B",
                  all(norm(p.get("work_dir")) != norm(bdir) for p in projects))
        for d in (bdir, other):
            shutil.rmtree(d, ignore_errors=True)
