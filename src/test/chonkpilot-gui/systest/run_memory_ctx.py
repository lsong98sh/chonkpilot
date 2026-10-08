# -*- coding: utf-8 -*-
"""P5 记忆/上下文/压缩配置「A 落库确认 + B 效果确认」端到端套件。

覆盖键（每条 **A + B**，B 必须有可观测证据；A 一律 `data-prj-config-list` 回读）：

  A/B-1 `memory.enabled`
        A: 缺键 → 非 true；写 `"false"` → 回读 `"false"`。
        B: 关闭态 → 送给 LLM 的 system **无【记忆库】段**，且 work-dir 内
           `.chonkpilot/memory` **不落盘预置文件**（spec 61 §3.1「关闭态不落盘创建」）；
           开启 → system **含【记忆库】段** + 8 个预置 `.md` 落盘（构建端到端对照）。
  A/B-2 `memory.min-turn-tokens`
        A: 写 `"999999"` → 回读；写 `"1"` → 回读。
        B: 极大阈值 → 观测窗内**无记忆写回**（记忆文件内容逐字节不变 + 无 `data-memory-refresh`）；
           小阈值 → **发生沉淀**（文件内容变为沉淀 LLM 产物 + `data-memory-refresh`(op=save)）+
           **类别 → 内容一致性**（I-78 回归：逐类别断言内容含本类别【类别】标记与**专属哨兵**
           且不含任何其它类别哨兵；反例 = 两类别文件内容完全相同，即原串味实证）。
           类别数按 **quorum = 全部-2** 断言（插件「失败静默、跳过该类」为既定行为，
           `memory.go:16`；串味判据则对**所有**类别无条件断言）。
  A/B-3 `memory.category.<类别名>`
        A: 写 `"false"` / `"true"` → 逐个回读。
        B: 关闭 `项目概要` → system 记忆指引**不含该类**（仍含其它类）；开启 → **含**；
           写入效果：关闭 → 该类文件**不被改写**（保持夹具哨兵）且无 `data-memory-refresh(id=该类)`；
           开启 → 该类**被写入**（内容变化 + `data-memory-refresh(id=该类)`）；写入后同类一致性判据。
  A/B-4 `keep_full_max_turns` + `keep_full_max_tokens` + `compress_token_threshold`
        A: 写 `"1"` / `"24000"` / `"999999"` → 回读；再写阈值 `"1"` → 回读。
        B: 阈值极大 → 快照**无** `[已压缩早前对话]` 且 mock 请求数 == 轮数（未发生摘要调用）；
           阈值极小 → 快照**出现** `[已压缩早前对话]`（压缩已回写快照）+ mock 收到**摘要请求**
           （n_requests = 轮数+1，`/last` 的 system == 摘要提示词）。
  A/B-5 `capability/system/summary.md`（data-prompt-{save,load,delete}，key=summary_prompt）
        A: 写哨兵 → `data-prompt-load` 回读 == 哨兵 + 项目级文件落盘含哨兵；
        B: 触发压缩 → 摘要请求 system **含哨兵**；
        回落: 删除该键 → A 回读 == 内置默认（embed 内置 `data.SystemDoc("summary")`，
              = 出厂文件 `src/initdata/capability/system/summary.md`）；
              B 再触发一次压缩 → 摘要请求 system == 内置默认（回落生效）。
  UI-6 `memory.category-max-tokens`
        **仅 UI 效果断言**（超阈值行标红 + 「建议细分记忆」提示文案）：该键后端**不消费**
        （plugin-memory / llm 侧均无该键；spec 61 §3.1 明示"仅前端标红提醒、不截断"）
        ——故**不写任何后端效果断言**。

观测面（全部既有，零新增 MQ 主题）
  * 注（2026-10-06，OP-05/06）：记忆提取进度已由 prjusr config 键 `memory-extract.<会话>.<类别>`
    迁至 **prjusr 专用表 `memory_extract`**（经 `data-memory-extract-{load,save,delete}`）——
    本套件不观测进度键，A/B 功能断言（落盘 / refresh / 阈值门控）不受影响。
  * mock LLM `GET /last`（**已有** `system` 原文 / `n_requests` 累计计数，P2 批次既有能力）；
  * `data-memory-refresh` 广播（61 §3.1 既有主题，save 后广播，op=save）；
  * 落盘文件：`<work-dir>/.chonkpilot/memory/<类别>.md`（隔离 work-dir 内，跑完随 tmp 清理）；
  * 类别专属哨兵：经**既有** `data-memory-save` 把 `CK-MEM-SENT-<nn>` 预置为各类别「现有全文」
    （沉淀 LLM 回显 prompt → 各类别文件内容应含**自己**的哨兵；`【类别】<类>` 同判据）→
    I-78（并行类别读取串味）的端到端归属断言；
  * `data-session-context{include_snapshot:true}`（61 §3.2a 既有面）→ 读**压缩后快照**；
  * 前端 DOM（`.b-table tr.is-over` / `.over-hint`，仅 UI 用例）。

隔离与还原
  * 自起隔离实例（独立 work-dir / data-dir / HOME，见 `run_llm_fields.py` 先例），
    不读写本机 `~/.chonkpilot`；mock LLM 自起（动态端口）；
  * 配置写入/还原走 `harness.suite_config_guard`（套件级 usr+prj 全量兜底）+
    逐用例 `_h.prj_config_guard(c, [...])`（原本缺省 → 删除；原本有值 → 写回），见 51 §6-8。

运行：python run_memory_ctx.py
"""

import json
import os
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

SENT = "MEMCTX-SUMMARY-SENTINEL-%d" % int(time.time())
# 内置默认摘要提示词前缀（出厂文件 src/initdata/capability/system/summary.md → data.SystemDoc("summary")）
DEFAULT_SUMMARY = "你是对话历史摘要器"
# 压缩插件回写快照的摘要前缀（chonkpilot-plugin-compress/compress.go:DoCompress）
COMPRESS_MARK = "[已压缩早前对话]"
# 「记忆库」指引段的**专属判据** = memory_guide.go:126 的段首句。
# 不能用「【记忆库】」三字：assetGuide（memory_guide.go:160，【知识库资产】指引）正文含
# 「与【记忆库】的区别：…」交叉引用句 → 关闭态会被误判为「已注入记忆指引」（2026-09-24 定位）。
GUIDE_MARK = "以下是本机沉淀的项目/用户记忆文件"

CAT_A = "项目概要"   # 门控用例关闭/开启的类别
CAT_B = "共同库"     # 对照类别（须始终在指引内）

# ── I-78 回归（类别 → 内容一致性）──
# 8 个预置项目级类别（chonkpilot-data/persist/persist_memory.go:45-54 memoryCategorySpecs）
# + 唯一用户级 用户偏好（落盘于 HOME 下）。
PROJ_CATS = ["项目概要", "共同库", "开发规范", "构建发布规则",
             "接口库", "测试规范", "典型参照", "用户决策"]
# 每类别专属哨兵：沉淀前预置为「现有全文」→ 沉淀 LLM 回显 prompt → 各类别文件只应含自己的哨兵。
MEM_SENT = {c: "CK-MEM-SENT-%02d" % (i + 1) for i, c in enumerate(PROJ_CATS + ["用户偏好"])}


def _plain(v):
    """解包 eval 结果的 JSON 字符串（测试通道可能双重编码）。"""
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


def _evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def main():
    # 自起 mock LLM（动态端口；既作 provider baseUrl，也作 exe flag -llm-base → llm-simple 摘要/沉淀）
    m = _h.start_mock_llm(_h.free_port())
    home = _h.tmp_home()
    g = _h.acquire_gui(_h.free_port(),
                       work_dir=_h.tmp_dir("memctx-ws-"),
                       data_dir=_h.tmp_dir("memctx-dd-"),
                       home=home,
                       extra_args=("-llm-base=http://127.0.0.1:%d/v1" % m.port,
                                   "-llm-model=m-memctx"))
    c = g.client
    _h.suite_config_guard(c)  # 套件级快照-还原（51 §6-8）
    _h.ensure_locale(c)
    c.wait_ready(60)
    print("[setup] mock=%d gui=%d work_dir=%s home=%s" % (m.port, g.port, g.work_dir, home), flush=True)

    MEM_DIR = os.path.join(g.work_dir, ".chonkpilot", "memory")
    PREF_FILE = os.path.join(home, ".chonkpilot", "用户偏好.md")
    SUMMARY_FILE = os.path.join(g.work_dir, ".chonkpilot", "capability", "system", "summary.md")

    # ── mock / 数据面 / 会话 助手 ──────────────────────────────

    def last():
        """mock 最近一次请求证据（无请求 → {}）。"""
        try:
            with urllib.request.urlopen("http://127.0.0.1:%d/last" % m.port, timeout=5) as r:
                return json.loads(r.read().decode("utf-8")) or {}
        except Exception:
            return {}

    def reset_mock():
        try:
            urllib.request.urlopen("http://127.0.0.1:%d/reset" % m.port, timeout=5).read()
        except Exception:
            pass
        time.sleep(0.2)

    def nreq():
        return int(last().get("n_requests") or 0)

    def prj():
        r = c.req("data-prj-config-list", {})
        return (r.get("list") or {}) if isinstance(r, dict) else {}

    def prj_save(k, v):
        c.req("data-prj-config-save", {"data": {"key": k, "value": v}})

    def mem_files():
        """项目记忆目录 {文件名: 全文}（不存在 → {}）。"""
        out = {}
        if os.path.isdir(MEM_DIR):
            for n in sorted(os.listdir(MEM_DIR)):
                if n.endswith(".md"):
                    with open(os.path.join(MEM_DIR, n), "r", encoding="utf-8", errors="replace") as f:
                        out[n] = f.read()
        return out

    def read_file(p):
        try:
            with open(p, "r", encoding="utf-8", errors="replace") as f:
                return f.read()
        except Exception:
            return ""

    # ── I-78 回归：类别 → 内容一致性助手 ───────────────────────

    def read_all_mem():
        """类别 → 全文（项目级 `<work-dir>/.chonkpilot/memory/*.md` + 用户级 HOME 下 用户偏好.md）。"""
        out = dict(mem_files())
        out["用户偏好.md"] = read_file(PREF_FILE)
        return out

    def seed_mem_sentinels():
        """经既有 `data-memory-save` 把各类别专属哨兵预置为「现有全文」（沉淀的输入侧夹具）。

        沉淀 prompt = `【类别】<类>\\n【现有全文】<旧全文>\\n【本轮新增信息】<本轮>`，mock 回显
        该 prompt → 每个类别文件内容必含**自己**的哨兵；若并发请求应答串味（I-78），某类会拿到
        他人旧全文 → 该文件出现**他人的哨兵**、缺自己的哨兵。
        """
        for cat in MEM_SENT:
            c.req("data-memory-save", {"data": {"category": cat, "content": MEM_SENT[cat]}})
        time.sleep(0.3)

    def quorum_of(cats):
        """批次 quorum：要求**全部**类别完成沉淀。

        原「允许至多 3 类跳过」的口径已废除：根因不是插件设计，而是 **mock LLM 的 listen
        backlog=5** —— 并行沉淀一轮打 8~9 个请求，溢出连接在 connect 阶段被拒
        （ECONNREFUSED），插件按「失败静默」跳过该类。mock 已改为 backlog=128（mock_llm.py
        MockLLMServer），故此处收紧为全量；串味判据（他人哨兵/他人【类别】标记/两文件全等）
        对所有类别无条件断言。
        """
        return len(cats)

    def wait_quorum(before, cats, min_ok, max_wait=40):
        """等沉淀收敛：cats 中「文件内容相对 before 变化」的类别数 ≥ min_ok（False = 超时未达）。

        取 quorum 而非「全部」的原因：插件**失败静默**是既定行为（`memory.go:16`「任一环节失败
        只记日志、跳过该类」）→ 并行扇出中偶有单类别被跳过属设计内，不应据此判失败。
        """
        deadline = time.time() + max_wait
        while time.time() < deadline:
            now = read_all_mem()
            n = sum(1 for k in cats if now.get(k + ".md") != before.get(k + ".md"))
            if n >= min_ok:
                return True
            time.sleep(0.6)
        return False

    def diag_heads(tag="memory", n=8):
        """失败路径诊断：各类别文件头 + 隔离实例 GUI 日志中含 tag 的尾行。"""
        heads = {k: v.replace("\n", "\\n")[:40] for k, v in sorted(read_all_mem().items())}
        p = os.path.join(g.work_dir, ".chonkpilot", "logs", "app.log")
        try:
            with open(p, "r", encoding="utf-8", errors="replace") as f:
                lines = [ln.rstrip() for ln in f if tag in ln]
            log = lines[-n:]
        except Exception as e:  # 日志缺失不掩盖主判据
            log = ["no-log:%s" % e]
        return {"heads": heads, "plugin_log": log}

    def check_cat_consistency(files, before, skipped_ok=()):
        """逐类别断言「内容与自身类别对应、无跨类别串味」（I-78 回归判据）。

        无条件判据（对本轮**所有**类别文件生效，与被改写或被跳过无关）：
          * 不得出现**他人**类别哨兵（`cross-talk`）——I-78 串味的直接签名（A 类别的应答被
            B 请求收下 → B 的 prompt 携带 A 的旧全文 → B 文件写入 A 的哨兵）；
          * 不得出现**他人**类别的 `【类别】<类>` 标记（沉淀 prompt 的类别标记恒为自身类别）；
          * 任意两个类别文件内容不得**完全相同**（I-78 实测症状：两文件读到同一份内容）。
        本轮**被改写**（内容 ≠ 夹具哨兵）的类别还须：含**本类** `【类别】<类>` + **本类**哨兵。
        `skipped_ok` = 本轮刻意关闭的类别（须保持夹具哨兵不动）。

        返回 (bad, rewritten, untouched, identical)。
        """
        bad, rewritten, untouched, by_text = {}, [], [], {}
        for cat, sent in MEM_SENT.items():
            body = files.get(cat + ".md")
            if body is None:
                bad[cat] = "missing"
                continue
            probs = []
            cross = [c2 for c2, s2 in MEM_SENT.items() if c2 != cat and s2 in body]
            if cross:
                probs.append("cross-talk:" + ",".join(cross))
            cross_mark = [c2 for c2 in MEM_SENT
                          if c2 != cat and ("【类别】" + c2) in body]
            if cross_mark:
                probs.append("cross-category-mark:" + ",".join(cross_mark))
            if cat in skipped_ok:
                if body != before.get(cat + ".md") or sent not in body:
                    probs.append("closed-category-rewritten")
            elif body == before.get(cat + ".md"):
                untouched.append(cat)          # 本轮被跳过（失败静默，设计内）
            else:
                rewritten.append(cat)
                if ("【类别】" + cat) not in body:
                    probs.append("no-own-category-mark")
                if sent not in body:
                    probs.append("no-own-sentinel")
            if probs:
                bad[cat] = ";".join(probs)
            if body:
                by_text.setdefault(body, []).append(cat)
        identical = [v for v in by_text.values() if len(v) > 1]
        return bad, rewritten, untouched, identical

    _SEQ = [0]

    def new_session():
        _SEQ[0] += 1
        return "memctx-%d-%03d" % (int(time.time() * 1000), _SEQ[0])

    def arm(topics=("turn-start", "llm-complete", "llm-error")):
        """注册事件捕获（mq_on_capture 会先清空旧事件 + 注销上一轮 handler）。"""
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(list(topics))

    def wait_turn_start(sid, max_wait=60):
        deadline = time.time() + max_wait
        while time.time() < deadline:
            for e in c.events_of("turn-start", clear=False):
                p = e.get("payload") or {}
                if p.get("session") == sid and not p.get("parents"):
                    return p
            time.sleep(0.3)
        raise TestError("未收到主会话 turn-start（session=%s）" % sid)

    def wait_complete(sid, max_wait=90):
        deadline = time.time() + max_wait
        while time.time() < deadline:
            ours = [e.get("payload") or {} for e in c.events_of("llm-complete", clear=False)
                    if (e.get("payload") or {}).get("session") == sid]
            if ours:
                c.events_of("llm-complete", clear=True)
                return ours[-1]
            time.sleep(0.4)
        raise TestError("等待 llm-complete 超时（session=%s）" % sid)

    def run_turn(q, sid=None, idx=1):
        """mq 驱动一轮（llm-start）并等终态；返回 session id（注册捕获在调用前 arm）。"""
        sid = sid or new_session()
        c.mq_emit("llm-start", {"session_id": sid, "turn": "t-%s-%d" % (sid, idx), "q": q,
                                "llm": "mock", "think": "", "effort": "", "scenario_id": ""})
        wait_turn_start(sid)
        wait_complete(sid)
        return sid

    def turn_system(q, topics=("turn-start", "llm-complete", "llm-error")):
        """跑一轮并返回 (该轮 LLM 请求的 system 原文, 本轮 mock 请求数)。

        请求数一并返回：`system` 为空串时须能区分「真的没有 system 消息」与「本轮根本没打到 mock」
        （否则"不含【记忆库】"类断言会因 /last 为空而空转通过）。
        """
        reset_mock()
        arm(topics)
        run_turn(q)
        n = nreq()
        if n < 1:
            raise TestError("本轮未打到 mock LLM（n_requests=%d）→ system 断言无效" % n)
        return (last().get("system") or ""), n

    def snapshot_msgs(sid):
        r = c.req("data-session-context", {"data": {"session_id": sid, "include_snapshot": "true"}})
        return (r.get("messages") or []) if isinstance(r, dict) else []

    def compress_mark(sid):
        for x in snapshot_msgs(sid):
            if isinstance(x, dict) and x.get("role") == "system" \
                    and str(x.get("content") or "").startswith(COMPRESS_MARK):
                return str(x.get("content"))
        return ""

    def wait_compress(sid, max_wait=25):
        deadline = time.time() + max_wait
        while time.time() < deadline:
            mk = compress_mark(sid)
            if mk:
                return mk
            time.sleep(0.6)
        return ""

    # ── 前端 DOM 助手（仅 UI-6 用例）────────────────────────────

    def vcount(sel):
        return int(_plain(c.eval("([...document.querySelectorAll(%s)].filter(e=>e.getBoundingClientRect().width>0)).length"
                                 % json.dumps(sel))) or 0)

    def ev(js, timeout=6000):
        return _plain(c.eval(js, timeout))

    def click_tab(label):
        ev("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
           ".find(e=>e.getBoundingClientRect().width>0);if(!R)return 'no-root';"
           "const t=[...R.querySelectorAll('.b-tabs-item')].find(x=>x.textContent.trim()===%s);"
           "if(!t)return 'no-tab';t.click();return 'ok';})()" % json.dumps(label), 5000)
        time.sleep(1.0)

    def wait_vcount(sel, max_wait=10.0):
        """有界轮询：可见元素数 > 0（等价 vcount + 轮询）。"""
        deadline = time.time() + max_wait
        while time.time() < deadline:
            if vcount(sel):
                return True
            time.sleep(0.3)
        return False

    def wait_vcount_gone(sel, max_wait=10.0):
        """有界轮询：可见元素数 == 0。"""
        deadline = time.time() + max_wait
        while time.time() < deadline:
            if not vcount(sel):
                return True
            time.sleep(0.3)
        return False

    # ── 用例 ─────────────────────────────────────────────────

    def case_memory_enabled():
        """A/B-1 memory.enabled：A 回读；B 关闭态 system 无【记忆库】+ 不落盘预置；开启态对照。"""
        with _h.prj_config_guard(c, ["memory.enabled", "memory.min-turn-tokens"]):
            # 隔离干扰：沉淀阈值极大 → 本用例各轮**不触发**沉淀 llm-simple，/last 恒为主轮请求
            prj_save("memory.min-turn-tokens", "999999")
            v0 = prj().get("memory.enabled")
            if v0 == "true":
                raise TestError("隔离实例 memory.enabled 初始应为缺省/非 true，实际 %r" % v0)
            prj_save("memory.enabled", "false")
            v1 = prj().get("memory.enabled")
            if v1 != "false":
                raise TestError("A 落库确认失败：memory.enabled 回读 %r，期望 'false'" % v1)
            _evi("memory.enabled-A", readback=v1)
            # B（关闭）：system 无【记忆库】段 + 记忆目录不落盘
            sys_off, n_off = turn_system("记忆开关验证-关闭态")
            if GUIDE_MARK in sys_off:
                raise TestError("memory.enabled=false 时 system 仍含【记忆库】段")
            if os.path.isdir(MEM_DIR):
                raise TestError("memory.enabled=false 时仍落盘了记忆目录/预置文件：%r"
                                % sorted(os.listdir(MEM_DIR)))
            _evi("memory.enabled-B-false", requests=n_off, system_head=sys_off[:120],
                 memory_dir_exists=False)
            # B（开启）：system 含【记忆库】段 + 8 预置项目类别落盘
            prj_save("memory.enabled", "true")
            v2 = prj().get("memory.enabled")
            if v2 != "true":
                raise TestError("A 落库确认失败：memory.enabled 回读 %r，期望 'true'" % v2)
            sys_on, n_on = turn_system("记忆开关验证-开启态")
            if GUIDE_MARK not in sys_on:
                raise TestError("memory.enabled=true 时 system 未注入【记忆库】指引：%r" % sys_on[:200])
            if not os.path.isdir(MEM_DIR):
                raise TestError("memory.enabled=true 后记忆目录仍未落盘")
            files = sorted(mem_files().keys())
            if len(files) < 8:
                raise TestError("启用后预置类别文件不足 8 个：%r" % files)
            _evi("memory.enabled-B-true", requests=n_on, guide_tail=sys_on[:260], preset_files=files)

    def case_min_turn_tokens():
        """A/B-2 memory.min-turn-tokens：A 回读；B 极大 → 无写回；小 → 发生沉淀（文件+refresh）。"""
        with _h.prj_config_guard(c, ["memory.enabled", "memory.min-turn-tokens"]):
            prj_save("memory.enabled", "true")
            # A：极大阈值落库回读
            prj_save("memory.min-turn-tokens", "999999")
            va = prj().get("memory.min-turn-tokens")
            if va != "999999":
                raise TestError("A 落库确认失败：memory.min-turn-tokens 回读 %r，期望 '999999'" % va)
            _evi("min-turn-tokens-A", readback=va)
            # 预置文件先落盘（首轮触发 data-memory-list）→ 本轮不参与"无写回"判定
            turn_system("记忆阈值-预热轮")
            # I-78 回归夹具：给每个类别预置**专属哨兵**作为「现有全文」（沉淀后逐类别校验内容归属）
            seed_mem_sentinels()
            before = read_all_mem()
            if len(before) < len(MEM_SENT):
                raise TestError("预置文件未落盘，无法进行写回判定：%r" % sorted(before.keys()))
            missing_sent = [k for k, s in MEM_SENT.items() if s not in (before.get(k + ".md") or "")]
            if missing_sent:
                raise TestError("类别哨兵预置失败（data-memory-save 未落盘）：%r" % missing_sent)
            # B（极大阈值）：观测窗内无写回
            reset_mock()
            arm(("turn-start", "llm-complete", "llm-error", "data-memory-refresh"))
            run_turn("记忆阈值-极大-本轮不得沉淀")
            sys_big = last().get("system") or ""
            if nreq() < 1:
                raise TestError("极大阈值轮未打到 mock LLM → 判定无效")
            if GUIDE_MARK not in sys_big:
                raise TestError("极大阈值轮 system 未含【记忆库】（记忆库未生效，判定无效）")
            time.sleep(10)
            after = read_all_mem()
            refs = c.events_of("data-memory-refresh", clear=False)
            if after != before:
                diff = [k for k in after if before.get(k) != after.get(k)]
                raise TestError("min-turn-tokens=999999 仍发生记忆写回：%r" % diff)
            if refs:
                raise TestError("min-turn-tokens=999999 仍收到 data-memory-refresh %d 条" % len(refs))
            _evi("min-turn-tokens-B-blocked", threshold=va, files=sorted(after.keys()),
                 content_len={k: len(v) for k, v in sorted(after.items())}, refresh_events=0)
            # A/B（小阈值）：写入 → 回读 → 发生沉淀
            prj_save("memory.min-turn-tokens", "1")
            vb = prj().get("memory.min-turn-tokens")
            if vb != "1":
                raise TestError("A 落库确认失败：memory.min-turn-tokens 回读 %r，期望 '1'" % vb)
            arm(("turn-start", "llm-complete", "llm-error", "data-memory-refresh"))
            run_turn("记忆阈值-极小-本轮应当沉淀")
            # 等沉淀收敛（9 个类别并行重写）：quorum = 全部-3（容忍设计内的单类别失败静默）
            cats_all = list(MEM_SENT.keys())
            quorum = quorum_of(cats_all)
            if not wait_quorum(before, cats_all, quorum, max_wait=40):
                raise TestError("min-turn-tokens=1 后沉淀类别数未达 quorum=%d：diag=%r"
                                % (quorum, diag_heads()))
            now = read_all_mem()
            changed = [k for k in now if now.get(k) != before.get(k)]
            refs2 = c.events_of("data-memory-refresh", clear=False)
            if not changed:
                raise TestError("min-turn-tokens=1 未发生记忆写回（文件内容未变）")
            if not refs2:
                raise TestError("min-turn-tokens=1 写回后未收到 data-memory-refresh 广播")
            ops = sorted({(e.get("payload") or {}).get("op") for e in refs2})
            heads = {k: v.replace("\n", "\\n")[:56] for k, v in sorted(now.items())}
            sample = (now.get(changed[0]) or "")
            if "mock-reply:" not in sample:
                raise TestError("沉淀文件内容非沉淀 LLM 产物：%r" % sample[:200])
            # I-78 回归：**类别 → 内容一致性**（逐类别归属 + 无跨类别串味，无条件判据）
            bad, rewritten, untouched, identical = check_cat_consistency(now, before)
            if bad or identical:
                raise TestError("类别→内容一致性失败（I-78 串味回归）：bad=%r identical=%r" % (bad, identical))
            if len(rewritten) < quorum:
                raise TestError("被改写类别数 %d < quorum=%d：%r" % (len(rewritten), quorum, diag_heads()))
            _evi("min-turn-tokens-B-distilled", threshold=vb, changed_files=changed,
                 refresh_events=len(refs2), refresh_ops=ops, heads=heads,
                 pref_file_changed="mock-reply:" in read_file(PREF_FILE))
            _evi("category-content-consistency", categories=len(MEM_SENT), rewritten=rewritten,
                 untouched=untouched, bad=bad, identical_pairs=identical, sentinels=MEM_SENT,
                 own_mark=[k for k in rewritten if ("【类别】" + k) in (now.get(k + ".md") or "")])

    def case_category_gate():
        """A/B-3 memory.category.<类别>：A 回读；B 指引含/不含该类 + 该类是否**被写入**（I-78 回归）。"""
        with _h.prj_config_guard(c, ["memory.enabled", "memory.min-turn-tokens",
                                     "memory.category." + CAT_A]):
            prj_save("memory.enabled", "true")
            prj_save("memory.min-turn-tokens", "999999")  # 隔离沉淀请求 → /last 恒为主轮请求
            key = "memory.category." + CAT_A
            # A：关闭 → 回读 false
            prj_save(key, "false")
            vf = prj().get(key)
            if vf != "false":
                raise TestError("A 落库确认失败：%s 回读 %r，期望 'false'" % (key, vf))
            sys_off, n_cat_off = turn_system("记忆类别门控-关闭" + CAT_A)
            if GUIDE_MARK not in sys_off:
                raise TestError("关闭单个类别后【记忆库】指引整体消失（应仅该类不列）")
            if CAT_A in sys_off:
                raise TestError("%s=false 后 system 指引仍含该类：%r" % (CAT_A, sys_off[:300]))
            if CAT_B not in sys_off:
                raise TestError("对照类别 %s 未出现在指引内（判定无效）：%r" % (CAT_B, sys_off[:300]))
            _evi("category-A-false", readback=vf, requests=n_cat_off, guide=sys_off[:300])
            # A：开启 → 回读 true
            prj_save(key, "true")
            vt = prj().get(key)
            if vt != "true":
                raise TestError("A 落库确认失败：%s 回读 %r，期望 'true'" % (key, vt))
            sys_on, n_cat_on = turn_system("记忆类别门控-开启" + CAT_A)
            if CAT_A not in sys_on:
                raise TestError("%s=true 后 system 指引未含该类：%r" % (CAT_A, sys_on[:300]))
            _evi("category-B-true", readback=vt, requests=n_cat_on, guide=sys_on[:300])
            # B（写入效果，I-78 回归）：关闭 → 该类**不被改写**（保持夹具哨兵 + 无 refresh(id=该类)）；
            # 开启 → 该类**被写入**（内容变化 + refresh(id=该类)）；写入后仍须满足类别归属一致。
            seed_mem_sentinels()
            prj_save("memory.min-turn-tokens", "1")   # 放开沉淀 → 本轮真发生并行分类别重写
            prj_save(key, "false")
            if prj().get(key) != "false":
                raise TestError("A 落库确认失败：%s 回读 %r，期望 'false'" % (key, prj().get(key)))
            pre_w = read_all_mem()
            others = [k for k in MEM_SENT if k != CAT_A]
            quorum_w = quorum_of(others)   # 容忍设计内的单类别失败静默（memory.go:16）
            n0 = nreq()
            arm(("turn-start", "llm-complete", "llm-error", "data-memory-refresh"))
            run_turn("记忆类别写入-关闭" + CAT_A)
            if not wait_quorum(pre_w, others, quorum_w, max_wait=40):
                raise TestError("关闭 %s 的沉淀轮写回类别数未达 quorum=%d：nreq=%d→%d diag=%r"
                                % (CAT_A, quorum_w, n0, nreq(), diag_heads()))
            mid = read_all_mem()
            bad_c, rewritten_w, untouched_w, identical_w = check_cat_consistency(
                mid, pre_w, skipped_ok=(CAT_A,))
            if bad_c or identical_w:
                raise TestError("关闭 %s 后一致性失败（I-78 串味回归）：bad=%r identical=%r"
                                % (CAT_A, bad_c, identical_w))
            if (mid.get(CAT_A + ".md") or "") != MEM_SENT[CAT_A]:
                raise TestError("%s=false 后该类仍被改写（应保持夹具哨兵）：%r"
                                % (CAT_A, (mid.get(CAT_A + ".md") or "")[:160]))
            ids_off = sorted({(e.get("payload") or {}).get("id")
                              for e in c.events_of("data-memory-refresh", clear=False)} - {None})
            if CAT_A in ids_off:
                raise TestError("%s=false 仍收到 data-memory-refresh(id=%s)" % (CAT_A, CAT_A))
            _evi("category-B-write-skipped", closed=CAT_A, file_kept_sentinel=True,
                 refresh_ids=ids_off, rewritten=rewritten_w, untouched=untouched_w)
            prj_save(key, "true")
            if prj().get(key) != "true":
                raise TestError("A 落库确认失败：%s 回读 %r，期望 'true'" % (key, prj().get(key)))
            n1 = nreq()
            arm(("turn-start", "llm-complete", "llm-error", "data-memory-refresh"))
            run_turn("记忆类别写入-开启" + CAT_A)
            if not wait_quorum(mid, [CAT_A], 1, max_wait=25):
                # 单类别失败静默属既定行为 → 补一轮（仍失败即判产品未写入）
                arm(("turn-start", "llm-complete", "llm-error", "data-memory-refresh"))
                run_turn("记忆类别写入-开启" + CAT_A + "-补轮")
            if not wait_quorum(mid, [CAT_A], 1, max_wait=30):
                raise TestError("%s=true 后该类未被写入（文件内容未变）：nreq=%d→%d diag=%r"
                                % (CAT_A, n1, nreq(), diag_heads()))
            aft = read_all_mem()
            ids_on = sorted({(e.get("payload") or {}).get("id")
                             for e in c.events_of("data-memory-refresh", clear=False)} - {None})
            if CAT_A not in ids_on:
                raise TestError("%s=true 写回后未收到 data-memory-refresh(id=%s)" % (CAT_A, CAT_A))
            bad_o, rewritten_o, untouched_o, identical_o = check_cat_consistency(aft, mid)
            if bad_o or identical_o:
                raise TestError("类别开关写入后一致性失败（I-78 串味回归）：bad=%r identical=%r"
                                % (bad_o, identical_o))
            if CAT_A not in rewritten_o:
                raise TestError("%s=true 后该类未被改写：%r" % (CAT_A, diag_heads()))
            _evi("category-B-write-applied", opened=CAT_A, refresh_ids=ids_on,
                 rewritten=rewritten_o, untouched=untouched_o, identical_pairs=identical_o,
                 bad=bad_o, file_head=(aft.get(CAT_A + ".md") or "").replace("\n", "\\n")[:120])

    def case_compress_threshold():
        """A/B-4 keep_full_max_turns + keep_full_max_tokens + compress_token_threshold：A 回读；B 压缩是否真发生（快照+摘要请求）。"""
        with _h.prj_config_guard(c, ["memory.enabled", "keep_full_max_turns", "keep_full_max_tokens", "compress_token_threshold"]):
            prj_save("memory.enabled", "false")  # 关闭沉淀 → mock 请求数 = 轮数(+摘要)
            # A：三项阈值落库回读（D1：keep_full_max_turns 为原 keep_full_turns 改名；keep_full_max_tokens 新增）
            prj_save("keep_full_max_turns", "1")
            prj_save("keep_full_max_tokens", "24000")
            prj_save("compress_token_threshold", "999999")
            m1 = prj()
            keys = ("keep_full_max_turns", "keep_full_max_tokens", "compress_token_threshold")
            if (m1.get("keep_full_max_turns") != "1" or m1.get("keep_full_max_tokens") != "24000"
                    or m1.get("compress_token_threshold") != "999999"):
                raise TestError("A 落库确认失败：%r" % {k: m1.get(k) for k in keys})
            _evi("compress-A", keep_full_max_turns=m1.get("keep_full_max_turns"),
                 keep_full_max_tokens=m1.get("keep_full_max_tokens"),
                 compress_token_threshold=m1.get("compress_token_threshold"))
            # B（不压缩）：2 轮 → 快照无压缩标记 + mock 恰 2 次（无摘要请求）
            reset_mock()
            arm(("turn-start", "llm-complete", "llm-error"))
            sid_no = new_session()
            run_turn("压缩阈值-极大-第1轮", sid=sid_no, idx=1)
            run_turn("压缩阈值-极大-第2轮", sid=sid_no, idx=2)
            mknone = wait_compress(sid_no, max_wait=8)
            n_no = nreq()
            if mknone:
                raise TestError("阈值 999999 仍发生压缩：%r" % mknone[:160])
            if n_no != 2:
                raise TestError("阈值 999999 时 mock 请求数=%d，期望 2（2 轮，无摘要调用）" % n_no)
            _evi("compress-B-not-triggered", requests=n_no, snapshot_head=str(snapshot_msgs(sid_no)[:1])[:160])
            # A/B（极小阈值 → 真压缩）
            prj_save("compress_token_threshold", "1")
            if prj().get("compress_token_threshold") != "1":
                raise TestError("A 落库确认失败：compress_token_threshold 回读 %r" % prj().get("compress_token_threshold"))
            reset_mock()
            arm(("turn-start", "llm-complete", "llm-error"))
            sid_yes = new_session()
            run_turn("压缩阈值-极小-第1轮", sid=sid_yes, idx=1)
            run_turn("压缩阈值-极小-第2轮", sid=sid_yes, idx=2)
            mk = wait_compress(sid_yes, max_wait=25)
            n_yes = nreq()
            if not mk:
                raise TestError("阈值 1 + keep_full_max_turns 1 未发生压缩（快照无 %s）" % COMPRESS_MARK)
            if n_yes != 3:
                raise TestError("压缩后 mock 请求数=%d，期望 3（2 轮 + 1 次摘要 llm-simple）" % n_yes)
            sys = last().get("system") or ""
            if DEFAULT_SUMMARY not in sys:
                raise TestError("最后一次（摘要）请求 system 非摘要提示词：%r" % sys[:200])
            _evi("compress-B-triggered", requests=n_yes, snapshot_mark=mk[:200],
                 summary_request_system=sys[:120])

    def case_summary_prompt():
        """A/B-5 capability/system/summary.md：A 保存/回读；B 压缩摘要请求 system 含哨兵；删除 → 回落默认。"""
        with _h.prj_config_guard(c, ["memory.enabled", "keep_full_max_turns", "keep_full_max_tokens", "compress_token_threshold"]):
            prj_save("memory.enabled", "false")
            prj_save("keep_full_max_turns", "1")
            prj_save("compress_token_threshold", "1")
            try:
                # A：写哨兵 → 回读 + 落盘
                c.req("data-prompt-save", {"data": {"key": "summary_prompt", "value": SENT}})
                got = (c.req("data-prompt-load", {"id": "summary_prompt"}) or {}).get("data")
                if got != SENT:
                    raise TestError("A 落库确认失败：data-prompt-load 回读 %r，期望 %r" % (got, SENT))
                disk = read_file(SUMMARY_FILE)
                if SENT not in disk:
                    raise TestError("项目级 capability/system/summary.md 未落盘哨兵：%r" % disk[:200])
                _evi("summary-prompt-A", load=got, file=SUMMARY_FILE, file_len=len(disk))
                # B：触发一次压缩 → 摘要请求 system == 哨兵
                reset_mock()
                arm(("turn-start", "llm-complete", "llm-error"))
                sid1 = new_session()
                run_turn("总结提示词哨兵-第1轮", sid=sid1, idx=1)
                run_turn("总结提示词哨兵-第2轮", sid=sid1, idx=2)
                if not wait_compress(sid1, max_wait=25):
                    raise TestError("哨兵用例未触发压缩（无法验证摘要请求 system）")
                sys1 = last().get("system") or ""
                if SENT not in sys1:
                    raise TestError("摘要请求 system 不含哨兵：%r" % sys1[:200])
                _evi("summary-prompt-B", requests=nreq(), system=sys1[:160])
                # A/B（回落）：删除该键 → 回读默认；再压缩 → system == 默认
                c.req("data-prompt-delete", {"id": "summary_prompt"})
                got2 = (c.req("data-prompt-load", {"id": "summary_prompt"}) or {}).get("data") or ""
                if SENT in got2 or DEFAULT_SUMMARY not in got2:
                    raise TestError("删除后 A 回读未回落内置默认：%r" % got2[:200])
                if os.path.exists(SUMMARY_FILE):
                    raise TestError("删除后项目级 capability/system/summary.md 仍存在")
                _evi("summary-prompt-A-fallback", load=got2[:120], file_exists=False)
                reset_mock()
                arm(("turn-start", "llm-complete", "llm-error"))
                sid2 = new_session()
                run_turn("总结提示词回落-第1轮", sid=sid2, idx=1)
                run_turn("总结提示词回落-第2轮", sid=sid2, idx=2)
                if not wait_compress(sid2, max_wait=25):
                    raise TestError("回落用例未触发压缩（无法验证摘要请求 system）")
                sys2 = last().get("system") or ""
                if DEFAULT_SUMMARY not in sys2 or SENT in sys2:
                    raise TestError("删除后摘要请求 system 未回落内置默认：%r" % sys2[:200])
                _evi("summary-prompt-B-fallback", requests=nreq(), system=sys2[:160])
            finally:
                # 清理项目级覆盖文件（落盘在隔离 work-dir 内，双保险）
                try:
                    if os.path.exists(SUMMARY_FILE):
                        os.remove(SUMMARY_FILE)
                except Exception:
                    pass

    def case_memory_edit_dialog_ui():
        """UI-7 记忆内容编辑弹框（A1/A4，2026-09-24 用户口径；2026-09-26 双编辑入口）：

        - 行内两个编辑入口并存：【编辑提示词】（沉淀提示词，见 E4）与【编辑内容】（本用例）；
        - 行内【编辑内容】→ 弹框（bodyClass = `text-edit-dialog-body`，内容**撑满、底部不留白**）
          + 弹框内自带【优化】按钮；
        - 改内容 → 【保存】→ **弹框自动关闭** + 后端内容已更新（`data-memory-read` 回读）；
        - **状态栏底部**「记忆总 token 数」可点（A4 主入口，2026-09-24 由上下文管理页迁入）
          → 弹出**记忆分类列表** → 选中 → 内容弹框（可编辑、可保存）；上下文管理页仅保留只读总量。
        """

        def open_project_ctx():
            c.mq_emit("preview-tab-close-all")
            time.sleep(0.4)
            c.mq_emit("preview-tab-open", {"kind": "settings-project"})
            deadline = time.time() + 15
            while time.time() < deadline and vcount(".project-config-panel") == 0:
                time.sleep(0.4)
            if vcount(".project-config-panel") == 0:
                raise TestError("项目设置页未打开")
            click_tab("上下文管理")

        def row0_btn(label):
            return ev("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
                      ".find(e=>e.getBoundingClientRect().width>0);if(!R)return false;"
                      "const tr=R.querySelector('.b-table tbody tr');if(!tr)return false;"
                      "const t=[...tr.querySelectorAll('button')].find(x=>x.textContent.trim()===%s);"
                      "if(t)t.click();return !!t;})()" % json.dumps(label))

        def row0_btns():
            return ev("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
                      ".find(e=>e.getBoundingClientRect().width>0);if(!R)return [];"
                      "const tr=R.querySelector('.b-table tbody tr');if(!tr)return [];"
                      "return [...tr.querySelectorAll('button')].map(x=>x.textContent.trim());})()") or []

        def dialog_btn(label):
            return ev("(function(){const d=document.querySelector('.dialog-shell');if(!d)return false;"
                      "const t=[...d.querySelectorAll('button')].find(x=>x.textContent.trim()===%s);"
                      "if(t)t.click();return !!t;})()" % json.dumps(label))

        with _h.prj_config_guard(c, ["memory.enabled"]):
            prj_save("memory.enabled", "true")
            time.sleep(1.2)
            open_project_ctx()
            cat = None
            deadline = time.time() + 15
            while time.time() < deadline:
                cat = ev("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
                         ".find(e=>e.getBoundingClientRect().width>0);if(!R)return null;"
                         "const tr=R.querySelector('.b-table tbody tr');if(!tr)return null;"
                         "return (tr.querySelector('td')?.textContent||'').trim()||null;})()")
                if cat:
                    break
                time.sleep(0.4)
            if not cat:
                raise TestError("记忆类别表未渲染")

            # ① 行内两个编辑入口并存（提示词 / 内容）→ 点【编辑内容】开内容弹框
            btns0 = row0_btns()
            if "编辑提示词" not in btns0 or "编辑内容" not in btns0:
                raise TestError("类别行须并存【编辑提示词】/【编辑内容】：%r" % (btns0,))
            if not row0_btn("编辑内容"):
                raise TestError("类别行缺【编辑内容】按钮")
            if not wait_vcount(".text-edit-body", 10):
                raise TestError("内容编辑弹框未打开")
            geo = ev("(function(){const d=document.querySelector('.dialog-shell');if(!d)return null;"
                     "const body=d.querySelector('.dialog-body.text-edit-dialog-body');if(!body)return {body:false};"
                     "const ta=body.querySelector('textarea');const f=body.querySelector('.text-edit-footer');"
                     "const br=body.getBoundingClientRect();const btns=[...body.querySelectorAll('button')].map(x=>x.textContent.trim());"
                     "const tr=ta?ta.getBoundingClientRect():null;const fr=f?f.getBoundingClientRect():null;"
                     "return {body:true,bodyH:Math.round(br.height),taH:tr?Math.round(tr.height):0,"
                     "tail:fr?Math.round(br.bottom-fr.bottom):-1,btns:btns};})()")
            if not geo or not geo.get("body"):
                raise TestError("弹框未走 text-edit-dialog-body（留白风险）：%r" % geo)
            if "优化" not in (geo.get("btns") or []):
                raise TestError("弹框缺内嵌【优化】按钮：%r" % geo.get("btns"))
            # 撑满：编辑区高度 ≥ 弹框体 60%；footer 底边贴合弹框体底边（≤8px 公差）→ 底部不留白
            if geo.get("taH", 0) < geo.get("bodyH", 0) * 0.6:
                raise TestError("编辑区未撑满：%r" % geo)
            if geo.get("tail", 999) > 8:
                raise TestError("弹框底部留白：%r" % geo)

            # ② 改内容 → 保存 → 弹框自动关闭 + 内容落库
            sentinel = "L4-mem-edit-" + str(int(time.time()))
            ev("(function(){const ta=document.querySelector('.dialog-shell .text-edit-dialog-body textarea');"
               "if(!ta)return false;const s=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;"
               "s.call(ta,%s);ta.dispatchEvent(new Event('input',{bubbles:true}));return true;})()" % json.dumps(sentinel))
            if not dialog_btn("保存"):
                raise TestError("弹框缺【保存】按钮")
            if not wait_vcount_gone(".text-edit-body", 10):
                raise TestError("保存后弹框未自动关闭")
            got = (c.req("data-memory-read", {"data": {"category": cat}}) or {}).get("data", {}).get("content", "")
            if sentinel not in got:
                raise TestError("保存后内容未落库：%r" % got[:120])
            _evi("mem-edit-dialog", category=cat, geo=geo, saved=got[:80])

            # ③ 「记忆总 token 数」主入口（A4，2026-09-24 迁至**状态栏底部**）→ 分类列表 →
            #    选中 → 内容弹框（可编辑、可保存）；上下文管理页仅保留**只读**总量展示。
            readonly_txt = ev("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
                              ".find(e=>e.getBoundingClientRect().width>0);if(!R)return null;"
                              "const t=R.querySelector('.mem-total .mem-total-value');return t?(t.textContent||'').trim():null;})()")
            if readonly_txt is None:
                raise TestError("上下文管理页缺只读「记忆总 token 数」展示")
            total_txt = None
            deadline = time.time() + 12
            while time.time() < deadline:
                total_txt = ev("(function(){const e=document.querySelector('.statusbar .sb-mem .sb-mem-value');"
                               "return e?(e.textContent||'').trim():null;})()")
                if total_txt is not None:
                    break
                time.sleep(0.4)
            if total_txt is None:
                raise TestError("状态栏缺「记忆总 token 数」入口（A4 主入口）")
            ev("(function(){const e=document.querySelector('.statusbar .sb-mem');if(e)e.click();return !!e;})()")
            n = 0
            deadline = time.time() + 10
            while time.time() < deadline:
                n = vcount(".mem-cat-list .mem-cat-item")
                if n:
                    break
                time.sleep(0.3)
            if not n:
                raise TestError("点击状态栏总 token 数未弹出分类列表")
            nbtn = dialog_btn(cat)  # 列表项文本 = 类别名（首项同类）
            if not nbtn:
                ev("(function(){const i=document.querySelector('.mem-cat-list .mem-cat-item');"
                   "if(i)i.click();return !!i;})()")
            if not wait_vcount(".text-edit-body", 10):
                raise TestError("列表选中后内容弹框未打开")
            dialog_btn("取消")
            wait_vcount_gone(".text-edit-body", 6)
            _evi("mem-total-list", readonly_total=readonly_txt, total=total_txt, items=n, reopened=True)


    def case_category_max_tokens_ui():
        """UI-6 memory.category-max-tokens：**仅 UI 效果**（超阈值行标红 + 提示文案）。

        后端不消费该键（plugin-memory / llm 侧无引用；spec 61 §3.1「仅前端标红提醒、不截断」）
        → 仅断言前端 DOM 表现，不写后端效果断言。
        """
        with _h.prj_config_guard(c, ["memory.enabled", "memory.category-max-tokens"]):
            prj_save("memory.enabled", "true")
            prj_save("memory.category-max-tokens", "1")  # 极小阈值 → 预置模板 token 即超阈值
            time.sleep(1.2)
            c.mq_emit("preview-tab-close-all")
            time.sleep(0.4)
            c.mq_emit("preview-tab-open", {"kind": "settings-project"})
            deadline = time.time() + 15
            while time.time() < deadline and vcount(".project-config-panel") == 0:
                time.sleep(0.4)
            if vcount(".project-config-panel") == 0:
                raise TestError("项目设置页未打开")
            click_tab("上下文管理")
            deadline = time.time() + 15
            info = None
            while time.time() < deadline:
                info = ev("(function(){const R=[...document.querySelectorAll('.project-config-panel')]"
                          ".find(e=>e.getBoundingClientRect().width>0);if(!R)return null;"
                          "const rows=[...R.querySelectorAll('.b-table tbody tr')];"
                          "return {n:rows.length,over:rows.filter(r=>r.classList.contains('is-over')).length,"
                          "hints:[...R.querySelectorAll('.over-hint')].map(x=>x.textContent.trim())};})()")
                if info and info.get("n"):
                    break
                time.sleep(0.4)
            if not info or not info.get("n"):
                raise TestError("记忆类别表未渲染：%r" % info)
            if not info.get("over"):
                raise TestError("memory.category-max-tokens=1 未标红任何类别行：%r" % info)
            if not any("建议细分记忆" in h for h in (info.get("hints") or [])):
                raise TestError("缺「建议细分记忆」提示文案：%r" % info.get("hints"))
            _evi("category-max-tokens-UI", rows=info["n"], over_rows=info["over"], hints=info["hints"])

    # provider 条目（usr llms）：主轮请求经条目 baseUrl 打到本套件自起的 mock
    llms = [{"name": "mock", "protocol": "openai", "apiKey": "mock-key", "model": "mock-model",
             "baseUrl": "http://127.0.0.1:%d/v1" % m.port, "temperature": 0.7, "maxOutputToken": 4096}]

    ok = 0
    total = 0
    c.console(clear=True)
    with _h.user_config_guard(c, ["llms"]):
        c.req("data-user-config-save", {"data": {"llms": llms}})
        time.sleep(0.3)
        for name, fn in [
            ("A/B-1 memory.enabled（A 回读 + B 指引门控/预置落盘）", case_memory_enabled),
            ("A/B-2 memory.min-turn-tokens（A 回读 + B 无写回/发生沉淀 + 类别→内容一致性 I-78）",
             case_min_turn_tokens),
            ("A/B-3 memory.category.<类别>（A 回读 + B 指引含/不含 + 该类写入/不改写）",
             case_category_gate),
            ("A/B-4 keep_full_max_turns+keep_full_max_tokens+compress_token_threshold（A 回读 + B 压缩真发生）", case_compress_threshold),
            ("A/B-5 capability/system/summary.md（A 回读 + B 摘要请求 system 哨兵/回落）", case_summary_prompt),
            ("UI-6 memory.category-max-tokens（仅 UI 标红/提示，后端不消费）", case_category_max_tokens_ui),
            ("UI-7 记忆内容编辑弹框（撑满/内嵌优化/保存即关+落库/状态栏总量→分类列表）", case_memory_edit_dialog_ui),
        ]:
            total += 1
            ok += run_case(name, fn)
    errs = c.console()
    for e in (errs.get("entries") or []):
        if e.get("level") in ("error",):
            print("  [CONSOLE-ERROR] %s" % e.get("text"))
    print("\n记忆/上下文/压缩配置 A+B：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
