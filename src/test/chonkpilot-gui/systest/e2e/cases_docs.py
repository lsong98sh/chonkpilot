# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊹：**vfts 文档索引（Office/PDF）端到端**
（迁移自 run_docs_gate D1–D4）。

前置：`dist/mcps/markitdown/markitdown-mcp.exe` 且 vfts 引擎带 docs 支持；缺失 → **SKIP**
（与旧套件同口径，不伪造通过）。

该测试点需要独立实例（独立 work-dir/HOME + 自管转换器进程）。

* D1 开 docs + 真转换器 → `vfts.status{docsEnabled=true, docsService=running, docsPort}`；
  `file_list.json` 含 docx；`vfts_query` 正文 ASCII 哨兵与中文词均命中；
* D2 file_list 集合 == {note.txt, 中文文档.docx}（字面全等）；
* D3 二次强制重建 → 文档缓存文件集合与 mtime 不变（未重转）；
* D4 停转换器 → 强制重建 → 文档类跳过（清单不含 docx）、`docsService=absent`、正文不再命中，
  非文档仍保留且不报错。
"""

import json
import os
import subprocess
import sys
import time
import urllib.request
import zipfile

import harness as _h
from .framework import CaseSkip, case

REPO = os.path.abspath(os.path.join(_h.HERE, "..", "..", "..", ".."))
CONV_DIR = os.path.join(REPO, "dist", "mcps", "markitdown")
CONV_EXE = os.path.join(CONV_DIR, "markitdown-mcp.exe")
CONV_STATE = os.path.join(CONV_DIR, "state.json")

DOC_NAME = "中文文档.docx"
TXT_NAME = "note.txt"
DOC_SENT = "CK-DOC-OFFICE"
DOC_CN = "全文索引"
CREATE_NO_WINDOW = 0x08000000


def _make_docx(path, paras):
    ct = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
          '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
          '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
          '<Default Extension="xml" ContentType="application/xml"/>'
          '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-'
          'officedocument.wordprocessingml.document.main+xml"/></Types>')
    rels = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/'
            'relationships/officeDocument" Target="word/document.xml"/></Relationships>')
    body = "".join('<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>' % p
                   for p in paras)
    doc = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
           '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">'
           '<w:body>%s</w:body></w:document>' % body)
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr("[Content_Types].xml", ct)
        z.writestr("_rels/.rels", rels)
        z.writestr("word/document.xml", doc)


class _Docs:
    def __init__(self, client, ws):
        self.c, self.ws = client, ws

    def api(self, action, payload=None, timeout=180):
        body = json.dumps(json.dumps(payload or {}))
        self.c.eval("window.__ckApiRes=null;fetch('/plugins/codegraph/api/%s',{method:'POST',"
                    "headers:{'Content-Type':'application/json','X-CK-Plugin-Id':'codegraph'},"
                    "body:%s}).then(async r=>{window.__ckApiRes={status:r.status,"
                    "body:await r.text()}}).catch(e=>{window.__ckApiRes={status:0,"
                    "body:String(e)}});'ok'" % (action, body))
        end = time.time() + timeout
        while time.time() < end:
            r = _h._plain(self.c.eval("window.__ckApiRes ? JSON.stringify(window.__ckApiRes) : null"))
            if isinstance(r, dict):
                code, raw = r.get("status"), r.get("body") or ""
                try:
                    val = json.loads(raw) if raw else None
                except Exception:
                    val = raw
                if code != 200:
                    raise AssertionError("插件动作 %s 失败（HTTP %s）：%s"
                                         % (action, code, str(val)[:300]))
                return val
            time.sleep(0.2)
        raise AssertionError("插件动作 %s 超时" % action)

    def status(self):
        try:
            r = self.api("engine-status", {"workdir": self.ws, "engine": "vfts"}) or {}
        except Exception:
            return {}
        return (r.get("vfts") or {}) if isinstance(r, dict) else {}

    def save(self, k, v):
        self.c.req("data-prj-config-save", {"data": {"key": k, "value": v}})
        self.c.mq_emit("data-prj-config-refresh", {"id": k})
        time.sleep(0.15)

    def filelist(self):
        p = os.path.join(self.ws, ".chonkpilot", "vfts", "file_list.json")
        try:
            with open(p, encoding="utf-8") as f:
                rows = json.load(f)
        except (OSError, ValueError):
            return set()
        out = set()
        for e in (rows or []):
            rel = str((e or {}).get("path") or "")
            if not rel:
                continue
            try:
                out.add(os.path.relpath(os.path.normpath(rel),
                                        os.path.normpath(self.ws)).replace("\\", "/"))
            except ValueError:
                out.add(rel.replace("\\", "/"))
        return out

    def query(self, match):
        res = self.c.req("chonk.mcp-tools-call", {
            "name": "vfts_query", "arguments": {"workdir": self.ws, "match": match},
            "tool_call_display_name": "docs探针", "work_dir": self.ws}) or {}
        parts = [b.get("text") or "" for b in (res.get("content") or [])
                 if isinstance(b, dict) and b.get("type") == "text"]
        return json.loads("\n".join(parts)) if parts else {}

    def wait(self, fn, desc, timeout=180):
        end = time.time() + timeout
        last = None
        while time.time() < end:
            last = fn()
            if last:
                return last
            time.sleep(0.4)
        raise AssertionError("%s 超时（%ss）；末次=%r" % (desc, timeout, last))

    def cache_dir(self):
        return os.path.join(self.ws, ".chonkpilot", "vfts", "doc_text")

    def cache(self):
        d = self.cache_dir()
        out = {}
        if os.path.isdir(d):
            for n in os.listdir(d):
                try:
                    out[n] = os.path.getmtime(os.path.join(d, n))
                except OSError:
                    pass
        return out


def _health(port):
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/vfts/health" % port, timeout=3) as r:
            return 200 <= r.status < 300
    except Exception:
        return False


def _start_converter():
    if not os.path.isfile(CONV_EXE):
        raise CaseSkip("转换器未构建：%s（先跑 src/mcps/markitdown/build-mcps.ps1）" % CONV_EXE)
    proc = subprocess.Popen([CONV_EXE], cwd=CONV_DIR, creationflags=CREATE_NO_WINDOW,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    end = time.time() + 40
    st = None
    while time.time() < end:
        try:
            with open(CONV_STATE, encoding="utf-8") as f:
                st = json.load(f)
            if int(st.get("port") or 0) > 0:
                break
        except Exception:
            st = None
        time.sleep(0.5)
    if not st:
        raise AssertionError("转换器未写出状态文件：%s" % CONV_STATE)
    port = int(st.get("port") or 0)
    end = time.time() + 40
    while time.time() < end:
        if _health(port):
            return proc, port
        time.sleep(0.5)
    raise AssertionError("转换器 /vfts/health 未就绪（port=%s）" % port)


def _stop_converter(proc, port):
    try:
        proc.terminate()
        proc.wait(timeout=5)
    except Exception:
        try:
            proc.kill()
        except Exception:
            pass
    state = {}
    try:
        with open(CONV_STATE, encoding="utf-8") as f:
            state = json.load(f)
    except Exception:
        pass
    pid = int(state.get("pid") or 0)
    if pid:
        subprocess.run(["taskkill", "/PID", str(pid), "/T", "/F"],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                       creationflags=CREATE_NO_WINDOW)
    for p in (CONV_STATE, CONV_STATE + ".tmp"):
        try:
            os.remove(p)
        except OSError:
            pass
    end = time.time() + 15
    while time.time() < end:
        if port <= 0 or not _health(port):
            return
        time.sleep(0.3)


@case("S-DOCS-01", "索引", "vfts 文档索引：docx 入索引可检索/file_list 全等/缓存重建/停服降级",
      stage="seeded", order=395, fp="SRCH-007/S04–S07 · run_docs_gate D1–D4",
      desc="预检转换器产物（缺失 SKIP）；开 plugins.codegraph.vfts.docs → docsService=running+docsPort，"
           "docx 进 file_list 且正文 ASCII/中文均命中；file_list 字面全等；二次强制重建缓存 mtime "
           "不变；停转换器后重建 → docs 跳过/file_list 仅 txt/docsService=absent/正文不命中。")
def s_docs_01(env):
    if not os.path.isfile(CONV_EXE):
        raise CaseSkip("转换器未构建（%s）→ 与旧套件同口径 SKIP" % CONV_EXE)
    ws = _h.tmp_dir("ck-e2e-docs-ws-")
    _make_docx(os.path.join(ws, DOC_NAME), ["ChonkPilot 文档索引夹具",
                                            "%s %s" % (DOC_SENT, DOC_CN)])
    with open(os.path.join(ws, TXT_NAME), "w", encoding="utf-8") as f:
        f.write("plain note\n")
    proc = port = None
    h = None
    try:
        proc, conv_port = _start_converter()
        port = conv_port
        h = _h.start_gui(port=_h.free_port(), work_dir=ws, home=_h.tmp_home(),
                         ensure_spec=True,
                         extra_args=("-llm-base=http://127.0.0.1:%d/v1" % env.mock_port,
                                     "-llm-model=mock-model"))
        c = h.client
        c.wait_ready(120)
        d = _Docs(c, ws)
        for k, v in (("plugins.codegraph.vfts.exts", ".txt"),
                     ("plugins.codegraph.vfts.doc-max-mb", "50"),
                     ("plugins.codegraph.vfts.enabled", "true"),
                     ("plugins.codegraph.vfts.docs", "true")):
            d.save(k, v)
        d.wait(lambda: (lambda s: s if s.get("state") == "ready" else None)(d.status()),
               "vfts 首次索引 ready", 240)
        st = d.status()
        if "docsEnabled" not in st:
            raise CaseSkip("vfts 引擎无 docs 支持（status 无 docsEnabled）→ 需重新构建 build-vfts")

        # D1
        s = d.wait(lambda: (lambda x: x if x.get("docsService") == "running"
                            and x.get("state") == "ready" else None)(d.status()),
                   "docsService=running & ready", 120)
        env.check("D1 docsEnabled/docsService/docsPort",
                  s.get("docsEnabled") is True and s.get("docsService") == "running"
                  and bool(s.get("docsPort")), {k: s.get(k) for k in
                                                ("docsEnabled", "docsService", "docsPort")})
        d.wait(lambda: True if DOC_NAME in d.filelist() else None, "清单含 docx", 60)
        hit_sent = d.query(DOC_SENT)
        hit_cn = d.query(DOC_CN)
        paths = [str(x.get("path") or "").replace("\\", "/")
                 for x in (hit_sent.get("hits") or [])]
        env.check("D1 正文 ASCII 哨兵命中且含 docx",
                  bool(hit_sent.get("hits")) and any(p.endswith(DOC_NAME) for p in paths),
                  paths[:4])
        env.check("D1 正文中文词命中", bool(hit_cn.get("hits")), hit_cn.get("hits")[:2])

        # D2
        want = {TXT_NAME, DOC_NAME}
        got = d.wait(lambda: d.filelist() if d.filelist() >= want else None,
                     "file_list 收敛", 60)
        env.check("D2 file_list 字面全等 {note.txt, 中文文档.docx}", got == want, sorted(got))

        # D3
        before = d.cache()
        env.check("D3 首次索引写出文档缓存", bool(before), d.cache_dir())
        d.save("plugins.codegraph.vfts.doc-max-mb", "51")
        time.sleep(1.2)
        d.wait(lambda: True if d.status().get("state") == "ready" else None,
               "强制重建 ready", 180)
        after = d.cache()
        env.check("D3 二次重建复用缓存（集合与 mtime 不变）",
                  set(after) == set(before)
                  and not [k for k in before if abs(after.get(k, 0) - before[k]) > 0.001],
                  {"before": len(before), "after": len(after)})

        # D4
        _stop_converter(proc, port)
        proc = None
        time.sleep(1.0)
        d.save("plugins.codegraph.vfts.doc-max-mb", "52")
        time.sleep(1.2)
        d.wait(lambda: True if d.status().get("state") == "ready" else None,
               "停服后重建 ready", 240)
        got2 = d.wait(lambda: d.filelist() if DOC_NAME not in d.filelist() else None,
                      "文档类被跳过", 90)
        env.check("D4 文档类跳过、非文档保留", DOC_NAME not in got2 and TXT_NAME in got2,
                  sorted(got2))
        env.check("D4 docsService=absent", d.status().get("docsService") == "absent",
                  d.status().get("docsService"))
        env.check("D4 服务停止后正文不再命中", not (d.query(DOC_SENT).get("hits") or []))
    finally:
        if proc is not None:
            try:
                _stop_converter(proc, port or 0)
            except Exception:
                pass
        if h is not None:
            try:
                h.stop()
            except Exception:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("文档索引组（主实例）无前端错误", len(errs) == 0, errs[:3])