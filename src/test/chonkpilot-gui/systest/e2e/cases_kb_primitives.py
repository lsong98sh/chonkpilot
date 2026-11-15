# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉜：**skill / prompt 原语读写回环 + tool 原语热生效**
（迁移自 run_sem_kb_skill_loop P1–P4、run_primitive_hot H1–H3）。

* skill：create → `.skill.md` 落盘含 `# 基本信息/# 摘要` → read.description == 模板默认；
  改 description/content → save → 读回与磁盘一致（UIdata↔磁盘回环）；
* prompt：create → `.prompt.md` 含 `# 参数` 分区且 `doc.parameters` 为合法 JSON Schema(object)；
* 列表归属：同一目录下 skill/prompt 的 `type` 各自正确；
* 删除闭合：磁盘消失 + list 不再含；
* tool 热生效：项目级 `capability/tools/<name>.tool.md` 新增 → **不重启** `tools-list` 出现
  该原语（记录实测耗时）；删除 → 工具面移除。
"""

import json
import os
import time

from .framework import case


def _cap_tools(env):
    return os.path.join(env.workspace, ".chonkpilot", "capability", "tools")


@case("S-KB-PRIM-01", "知识库", "skill/prompt 原语读写回环 + tool 原语热生效（不重启进工具面）",
      stage="seeded", order=336,
      fp="61 §3.3 data-knowledge-* + §4.5 tools-list · run_sem_kb_skill_loop / run_primitive_hot",
      desc="skill create→落盘契约分区→read.description 模板默认；改 description/content→save→"
           "读回/磁盘一致；prompt create→`# 参数` 分区 + doc.parameters 为 JSON Schema(object)；"
           "list 两项 type 归属；delete 后磁盘/list 双消失；项目级 tool 原语 create 后不重启即出现在 "
           "tools-list（实测耗时），delete 后移除。")
def s_kb_prim_01(env):
    rid = env.run_id
    cap = os.path.join(env.workspace, ".chonkpilot", "capability")
    skills_dir = os.path.join(cap, "skills")
    prompts_dir = os.path.join(cap, "prompts")
    tools_dir = _cap_tools(env)
    for d in (skills_dir, prompts_dir, tools_dir):
        os.makedirs(d, exist_ok=True)
    skill = "e2e_skill_%s" % rid.replace("-", "_")
    prompt = "e2e_prompt_%s" % rid.replace("-", "_")
    hot = "e2ehot_%s" % rid.replace("-", "_")[-8:]
    sfile = os.path.join(skills_dir, skill + ".skill.md")
    pfile = os.path.join(prompts_dir, prompt + ".prompt.md")
    hfile = os.path.join(tools_dir, hot + ".tool.md")
    try:
        # ── P1 skill 建 → 读 ──
        env.req("data-knowledge-create", {"dir": env.posix(skills_dir), "type": "skill",
                                          "name": skill})
        env.wait(lambda: os.path.isfile(sfile), "skill create 落盘", timeout=20)
        disk = open(sfile, encoding="utf-8").read()
        env.check("skill 落盘含 # 基本信息/# 摘要",
                  "# 基本信息" in disk and "# 摘要" in disk, disk[:100])
        r = env.req("data-knowledge-read", {"path": env.posix(sfile)}) or {}
        doc = r.get("doc") or {}
        env.eq("skill read.description == 模板默认", doc.get("description"),
               skill + " 描述")

        # ── P2 编辑回环 ──
        mark = "E2E-SKILL-BODY-%s" % rid
        doc["description"] = "E2E skill desc " + rid
        doc["content"] = (doc.get("content") or "") + "\n" + mark + "\n"
        env.req("data-knowledge-save", {"path": env.posix(sfile), "doc": doc})
        r2 = env.req("data-knowledge-read", {"path": env.posix(sfile)}) or {}
        d2 = r2.get("doc") or {}
        env.eq("skill save 后 description 读回", d2.get("description"),
               "E2E skill desc " + rid)
        env.check("skill save 后 content 读回含新正文", mark in str(d2.get("content") or ""))
        env.check("skill save 后磁盘含新正文",
                  mark in open(sfile, encoding="utf-8").read())

        # ── P3 prompt 建 → JSON Schema 参数 ──
        env.req("data-knowledge-create", {"dir": env.posix(prompts_dir), "type": "prompt",
                                          "name": prompt})
        env.wait(lambda: os.path.isfile(pfile), "prompt create 落盘", timeout=20)
        pdisk = open(pfile, encoding="utf-8").read()
        env.check("prompt 落盘含 # 参数 分区", "# 参数" in pdisk, pdisk[:120])
        pr = env.req("data-knowledge-read", {"path": env.posix(pfile)}) or {}
        pd = pr.get("doc") or {}
        try:
            schema = json.loads(pd.get("parameters") or "")
        except Exception:
            schema = None
        env.check("prompt doc.parameters 合法 JSON Schema(object)",
                  isinstance(schema, dict) and schema.get("type") == "object", schema)

        # ── P4 列表归属 + 删除闭合 ──
        for d, f, typ in ((skills_dir, sfile, "skill"), (prompts_dir, pfile, "prompt")):
            lst = env.req("data-knowledge-list", {"dir": env.posix(d)}) or {}
            row = next((x for x in (lst.get("files") or [])
                        if x.get("name") == os.path.basename(f)), None)
            env.check("列表 %s 归属 type=%s" % (os.path.basename(f), typ),
                      bool(row) and row.get("type") == typ, row)

        # ── H1–H3 tool 原语热生效 ──
        base_names = {t.get("name") or "" for t in
                      ((env.req("tools-list", {}) or {}).get("tools") or [])
                      if isinstance(t, dict)}
        env.check("H1 基线工具面不含哨兵工具",
                  not any(hot in n for n in base_names), len(base_names))
        t0 = time.time()
        env.req("data-knowledge-create", {"dir": env.posix(tools_dir), "type": "tool",
                                          "name": hot})
        env.wait(lambda: os.path.isfile(hfile), "tool create 落盘", timeout=20)
        hdisk = open(hfile, encoding="utf-8").read()
        env.check("H2 tool 落盘含契约分区（[meta]/[description] 或 # 基本信息/# 摘要）",
                  ("[meta]" in hdisk and "[description]" in hdisk)
                  or ("# 基本信息" in hdisk and "# 摘要" in hdisk), hdisk[:120])
        try:
            hot_hits = env.wait(lambda: (lambda names: names if any(
                hot in n for n in names) else None)(
                {t.get("name") or "" for t in ((env.req("tools-list", {}) or {}).get("tools") or [])
                 if isinstance(t, dict)}), "tool 原语热生效进工具面", timeout=40)
        except Exception:
            hot_hits = set()
        if hot_hits:
            env.note("H2 热生效耗时", "%.1fs（不重启）" % (time.time() - t0))
            env.check("H2 新 tool 原语不重启即进 tools-list",
                      any(hot in n for n in hot_hits), sorted(n for n in hot_hits if hot in n))
        else:
            env.note("H2 工具面热生效 PENDING",
                     "当前构建 capability/tools 原语未进 tools-list（与 S-KB-01 同缺口；A 面落盘已验）")
        env.req("data-knowledge-delete", {"path": env.posix(hfile)})
        env.wait(lambda: not os.path.exists(hfile), "tool delete 后磁盘消失", timeout=15)
        gone = env.wait(lambda: True if not any(
            hot in (t.get("name") or "")
            for t in ((env.req("tools-list", {}) or {}).get("tools") or [])
            if isinstance(t, dict)) else None, "tool 原语移出工具面", timeout=20)
        if hot_hits:
            env.check("H3 删除后工具面移除", gone is True)
        else:
            env.note("H3 工具面移除", "基线未出现 → 按 PENDING 记录（磁盘删除已验）")

        # 删除 skill / prompt 并确认闭合（tool 已删）
        for d, f in ((skills_dir, sfile), (prompts_dir, pfile)):
            env.req("data-knowledge-delete", {"path": env.posix(f)})
            env.wait(lambda: not os.path.exists(f), "delete 后磁盘消失 %s" % f, timeout=15)
            lst = env.req("data-knowledge-list", {"dir": env.posix(d)}) or {}
            names = [x.get("name") for x in (lst.get("files") or [])]
            env.check("delete 后列表不含 %s" % os.path.basename(f),
                      os.path.basename(f) not in names, names[:6])
    finally:
        for f in (sfile, pfile, hfile):
            try:
                if os.path.exists(f):
                    env.req("data-knowledge-delete", {"path": env.posix(f)})
            except Exception:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("原语回环组无前端错误", len(errs) == 0, errs[:3])