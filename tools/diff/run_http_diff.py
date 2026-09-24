#!/usr/bin/env python3
"""端到端 HTTP 层差分：Go 版 server vs Python oracle server。

这是六层差分之外的**第七层**，也是唯一覆盖 `server.py` 全量路由的层。
前六层（trace/assert/xlsx/db/metrics/eval）都只测到库函数，
服务层的路由分派、JSON 形状、状态码、静态资源、同源校验此前没有任何门禁。

用法：
    python3 tools/diff/run_http_diff.py              # 自行拉起两个服务再比对
    python3 tools/diff/run_http_diff.py --keep       # 复用已在跑的服务
    python3 tools/diff/run_http_diff.py --go-port 8791 --py-port 8792

退出码 0 = 等价，1 = 有真实差异，2 = 工装自身出错。可直接接 CI。

设计要点（沿用本仓库差分工装的三条约定）：
  1. **不比哈希。** 两侧 JSON 键序与空格必然不同（Python 保留插入序、
     Go 的 map 按字典序），因此拉回结构做递归比较，数值按数值比（bool 除外）。
  2. **只读优先。** 除专门的安全层探针外不发有副作用的请求；
     `/api/run` 会真的拉起流水线，**故意不测**（见 SKIP_SIDE_EFFECT）。
  3. **差异要能证明，不要放过。** 唯一保留的已知差异是非法 run_id 含 `..` 时
     `artifacts.root` 的路径归一化（Python 不归一、Go 的 filepath.Join 会归一），
     判据写死在 KNOWN_COSMETIC 里并单独计数，出现别的差异一律算真实差异。
"""

import argparse
import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
GO_ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
DEFAULT_PY_ROOT = os.path.abspath(os.path.join(GO_ROOT, "..", "ruiyun-ui-test-platform"))

# 逐次运行会变的字段：只比结构与时序无关的部分。
VOLATILE = {"elapsed_s", "started_at", "generated_at"}

# 会真的起流水线 / 落盘 / 改配置 / 弹访达窗口的端点 —— 差分**故意不碰**。
# 这些路径的入参校验另有覆盖（见下方 REVEAL/LLM 的「仅校验」探针），
# 但**绝不发起**会产生副作用的那一种调用。踩过的坑：
#   * POST /api/run        → 真的 spawn 流水线子进程
#   * POST /api/app-settings → 真的写 .app_settings.json（覆盖文件）
#   * POST /api/reveal {path: 已存在的目录} → macOS 上真的 `open -R` 弹访达
#   * POST /api/llm/test {合法地址+Key}     → 真的打外网
SKIP_SIDE_EFFECT = {
    "/api/run",
    "/api/app/close",
    "/api/app-settings",
    "/api/evaluate/stop",
}

diffs = []
checked = 0
cosmetic = 0


# ------------------------------------------------------------------ 传输

def fetch(base, path, method="GET", body=None, headers=None, timeout=15):
    """发一个请求；返回 (状态码, Content-Type, 响应体)。HTTP 错误码也当正常返回。"""
    url = base + urllib.parse.quote(path, safe="/?=&%.,:-_")
    data = None
    hdr = {"Host": base.split("//")[1]}
    if body is not None:
        data = json.dumps(body).encode()
        hdr["Content-Type"] = "application/json"
    if headers:
        hdr.update(headers)
    req = urllib.request.Request(url, data=data, method=method, headers=hdr)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.headers.get("Content-Type"), r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.headers.get("Content-Type"), e.read()
    except Exception as e:  # 连接被拒 / 超时 / 协议错
        return -1, str(e), b""


# ------------------------------------------------------------------ 比较

def strip_volatile(v):
    if isinstance(v, dict):
        return {k: strip_volatile(x) for k, x in v.items() if k not in VOLATILE}
    if isinstance(v, list):
        return [strip_volatile(x) for x in v]
    return v


def known_cosmetic(path, key):
    """已知且已证明的差异：非法 run_id（含 ..）时 artifacts.root 的路径归一化。"""
    return ".." in path and key.endswith("artifacts.root")


def deep_cmp(a, b, path, key, out):
    if isinstance(a, dict) and isinstance(b, dict):
        ka, kb = set(a), set(b)
        if ka != kb:
            out.append(f"{path}.{key}: 键集合不同 仅Go={sorted(ka - kb)[:8]} "
                       f"仅Py={sorted(kb - ka)[:8]}")
        for k in sorted(ka & kb):
            deep_cmp(a[k], b[k], path, f"{key}.{k}", out)
        return
    if isinstance(a, list) and isinstance(b, list):
        if len(a) != len(b):
            out.append(f"{path}.{key}: 数组长度不同 Go={len(a)} Py={len(b)}")
        for i, (x, y) in enumerate(zip(a, b)):
            deep_cmp(x, y, path, f"{key}[{i}]", out)
        return
    if isinstance(a, bool) or isinstance(b, bool):
        if a is not b:
            out.append(f"{path}.{key}: bool 不同 Go={a!r} Py={b!r}")
        return
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        if abs(float(a) - float(b)) > 1e-9:
            out.append(f"{path}.{key}: 数值不同 Go={a!r} Py={b!r}")
        return
    if a != b:
        out.append(f"{path}.{key}: 值不同\n      Go={repr(a)[:200]}\n      Py={repr(b)[:200]}")


def cmp_json(go, py, path, method="GET", body=None, headers=None, note=""):
    global checked
    checked += 1
    sg, _, bg = fetch(go, path, method, body, headers)
    sp, _, bp = fetch(py, path, method, body, headers)
    label = f"{path}{note}"
    if sg != sp:
        diffs.append(f"{label}: HTTP 状态不同 Go={sg} Py={sp} "
                     f"Go体={bg[:150]!r} Py体={bp[:150]!r}")
        return
    try:
        jg, jp = json.loads(bg), json.loads(bp)
    except Exception as e:
        diffs.append(f"{label}: 响应不是 JSON（{e}）状态={sg} "
                     f"Go={bg[:150]!r} Py={bp[:150]!r}")
        return
    out = []
    deep_cmp(strip_volatile(jg), strip_volatile(jp), label, "", out)
    _classify(label, out)


def cmp_bytes(go, py, path, method="GET", body=None, headers=None, note=""):
    global checked
    checked += 1
    sg, cg, bg = fetch(go, path, method, body, headers)
    sp, cp, bp = fetch(py, path, method, body, headers)
    label = f"{path}{note}"
    if sg != sp:
        diffs.append(f"{label}: HTTP 状态不同 Go={sg} Py={sp}")
        return
    if bg != bp:
        diffs.append(f"{label}: 字节不同 Go={len(bg)}B Py={len(bp)}B")
    elif (cg or "").split(";")[0] != (cp or "").split(";")[0]:
        diffs.append(f"{label}: Content-Type 不同 Go={cg!r} Py={cp!r}")


def _classify(label, out):
    """把已知的装饰性差异单独计数，其余计入真实差异。"""
    global cosmetic
    for d in out:
        if known_cosmetic(label, d.split(":")[0]):
            cosmetic += 1
        else:
            diffs.append(d)


# ------------------------------------------------------------------ 用例矩阵

def run_matrix(go, py, py_web):
    # 1) 静态资源：前端 4120 行 JS 一行不改，必须逐字节一致
    assets = []
    for dirpath, _, names in os.walk(py_web):
        for n in names:
            rel = os.path.relpath(os.path.join(dirpath, n), py_web)
            if not rel.startswith("."):
                assets.append(rel)
    for rel in sorted(assets):
        cmp_bytes(go, py, "/" + rel)
    print(f"  静态资源 {len(assets)} 个", file=sys.stderr)

    # 2) 全部轮次归档（含 report.html 与详情 JSON）
    _, _, bg = fetch(go, "/api/rounds?limit=200")
    rids = []
    try:
        rids = [r["run_id"] for r in (json.loads(bg).get("rounds") or [])]
    except Exception:
        pass
    for rid in rids:
        cmp_json(go, py, f"/api/rounds/{rid}")
        cmp_bytes(go, py, f"/api/rounds/{rid}/report.html")
    print(f"  轮次归档 {len(rids)} 个", file=sys.stderr)

    # 3) 查询参数组合（含边界与非法值）
    for q in ["limit=1", "limit=0", "limit=-5", "limit=999", "limit=abc",
              "offset=3", "offset=-2", "keyword=北京", "keyword=不存在的关键词xyz",
              "date_from=2026-09-01", "date_to=2026-09-30",
              "date_from=2026-09-23&date_to=2026-09-23", "date_from=bogus",
              "limit=2&offset=1&keyword=用例", "latest=1", "sort=asc"]:
        cmp_json(go, py, f"/api/rounds?{q}")

    for q in ["limit=1", "limit=0", "limit=-5", "limit=501", "limit=abc",
              "offset=1000", "order=desc", "order=asc", "order=bogus",
              "keyword=北京", "keyword=李白", "keyword=不存在的xyz",
              "scene=教学", "targets=docx", "targets=docx,pdf",
              "attachment=yes", "attachment=no", "attachment=maybe",
              "ids=CASE-001", "ids=CASE-001,CASE-002",
              "limit=4&offset=2&order=asc&keyword=北京"]:
        cmp_json(go, py, f"/api/preset-cases?{q}")

    # 4) 无副作用的固定端点
    for p in ["/api/config", "/api/env", "/api/app-settings", "/api/llm/config",
              "/api/run/status", "/api/eval/status", "/api/uploads",
              "/api/app/status", "/api/rounds", "/api/preset-cases?limit=5"]:
        cmp_json(go, py, p)

    # 5) 同源校验：Host 非本机 / Origin 非本机一律 403，且要回显观测到的值
    for hdr, note in [
        ({"Host": "evil.com"}, "HOST外域"),
        ({"Host": "127.0.0.1", "Origin": "http://evil.com"}, "ORIGIN外域"),
        ({"Host": "localhost", "Origin": "http://localhost"}, "localhost同源"),
        ({"Host": "192.168.1.5"}, "私网未放行"),
        ({"Host": "[::1]:1"}, "IPv6字面量"),
    ]:
        for p in ["/api/run/stop", "/api/evaluate/stop", "/api/env", "/api/reveal"]:
            cmp_json(go, py, p, "POST", {}, dict(hdr), f"[{note}]")
        cmp_json(go, py, "/api/config", "GET", None, dict(hdr), f"[GET {note}]")

    # 6) 方法与畸形请求
    cmp_bytes(go, py, "/api/config", "PUT")
    cmp_bytes(go, py, "/api/config", "PATCH")
    cmp_bytes(go, py, "/api/config", "OPTIONS")
    cmp_bytes(go, py, "/api/nope")
    cmp_bytes(go, py, "/css/nope.css")
    cmp_bytes(go, py, "/api/nope", "POST", {})
    cmp_json(go, py, "/api/rounds/", "GET")
    cmp_bytes(go, py, "/api/rounds/x/report.html", "GET")
    cmp_json(go, py, "/api/rounds/does_not_exist_xyz", "GET")
    cmp_json(go, py, "/api/rounds/run_x/extra", "GET")
    cmp_json(go, py, "/api/rounds/../../etc/passwd", "GET")
    cmp_json(go, py, "/api/rounds/%2e%2e%2fetc", "DELETE")
    cmp_json(go, py, "/api/rounds/", "DELETE")
    cmp_json(go, py, "/api/upload/delete", "POST", {"path": ""})
    cmp_json(go, py, "/api/upload/delete", "POST", {"path": "/tmp"})
    cmp_json(go, py, "/api/upload/delete", "POST", {"path": "/etc/passwd"})
    # /api/reveal 只用「路径不存在」的入参 —— 存在的目录会真的弹访达（macOS `open -R`）
    cmp_json(go, py, "/api/reveal", "POST", {})
    cmp_json(go, py, "/api/reveal", "POST", {"path": "/nonexistent/ruiyun_probe_xyz"})
    cmp_json(go, py, "/api/upload", "POST", {})
    cmp_json(go, py, "/api/upload", "POST", {"name": "x", "data_base64": "!!!"})
    # /api/llm/test 只打**本地校验**分支（地址非法/缺 Key），合法地址会真的发外网请求
    cmp_json(go, py, "/api/llm/test", "POST", {})
    cmp_json(go, py, "/api/llm/test", "POST", {"base_url": "notaurl"})
    cmp_json(go, py, "/api/llm/test", "POST", {"base_url": "ftp://x"})
    cmp_json(go, py, "/api/llm/test", "POST", {"base_url": "https://a.com", "api_key": ""})
    cmp_json(go, py, "/api/llm/test", "POST", {"base_url": "https://x:y@a.com", "api_key": "k"})
    cmp_json(go, py, "/api/preset-cases", "POST", {"action": "nope"})
    cmp_json(go, py, "/api/preset-cases", "POST", {"action": "add", "cases": []})
    cmp_json(go, py, "/api/preset-cases", "POST", {"action": "delete", "ids": []})
    cmp_json(go, py, "/api/preset-cases", "POST", {"action": "add_many", "cases": []})
    cmp_json(go, py, "/api/preset-import", "POST", {"name": "x", "data": ""})
    cmp_json(go, py, "/api/preset-import", "POST",
             {"name": "x", "data": "!!!notbase64!!!", "parse_only": True})
    cmp_json(go, py, "/api/env", "POST", {"profile": "bogus"})
    cmp_json(go, py, "/api/env", "POST", {"profile": ""})
    cmp_json(go, py, "/api/llm/config", "POST",
             {"base_url": "", "api_key": "", "model": ""})
    cmp_json(go, py, "/api/evaluate", "POST", {"run_id": "../etc"})


# ------------------------------------------------------------------ 进程管理

def port_open(port):
    with socket.socket() as s:
        s.settimeout(0.4)
        return s.connect_ex(("127.0.0.1", port)) == 0


def wait_up(port, seconds=25):
    for _ in range(int(seconds * 10)):
        if port_open(port):
            return True
        time.sleep(0.1)
    return False


def start_servers(args):
    procs = []
    env = dict(os.environ)

    if not port_open(args.go_port):
        go_bin = os.path.join(args.gocache, "ruiyun_httpdiff_bin")
        subprocess.run(["go", "build", "-o", go_bin, "./cmd/ruiyun"], cwd=GO_ROOT,
                       check=True, env=env)
        procs.append(subprocess.Popen(
            [go_bin, "serve", "--port", str(args.go_port)],
            cwd=GO_ROOT, env=env,
            stdout=open("/tmp/ruiyun_httpdiff_go.log", "wb"),
            stderr=subprocess.STDOUT))

    if not port_open(args.py_port):
        py_python = args.py_python
        if not os.path.exists(py_python):
            venv = os.path.join(args.py_root, ".venv", "bin", "python")
            py_python = venv if os.path.exists(venv) else sys.executable
        procs.append(subprocess.Popen(
            [py_python, "server.py", "--port", str(args.py_port)],
            cwd=args.py_root, env=env,
            stdout=open("/tmp/ruiyun_httpdiff_py.log", "wb"),
            stderr=subprocess.STDOUT))

    ok_go = wait_up(args.go_port)
    ok_py = wait_up(args.py_port)
    if not ok_go:
        print(f"[!] Go 服务未起来（端口 {args.go_port}），见 /tmp/ruiyun_httpdiff_go.log",
              file=sys.stderr)
    if not ok_py:
        print(f"[!] Python 服务未起来（端口 {args.py_port}），见 /tmp/ruiyun_httpdiff_py.log",
              file=sys.stderr)
    return procs, (ok_go and ok_py)


def stop_servers(procs):
    for p in procs:
        try:
            p.send_signal(signal.SIGTERM)
        except Exception:
            pass
    for p in procs:
        try:
            p.wait(timeout=5)
        except Exception:
            try:
                p.kill()
            except Exception:
                pass


# ------------------------------------------------------------------ main

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--go-port", type=int, default=8791)
    ap.add_argument("--py-port", type=int, default=8792)
    ap.add_argument("--py-root", default=os.environ.get("RUIYUN_PY_ROOT", DEFAULT_PY_ROOT))
    ap.add_argument("--py-python", default="",
                    help="跑 server.py 的解释器（默认用 py-root/.venv/bin/python）")
    ap.add_argument("--gocache", default=os.environ.get("GOCACHE",
                                                        os.path.expanduser("~/.cache/go-build")))
    ap.add_argument("--keep", action="store_true", help="不自行拉起/关闭服务")
    args = ap.parse_args()

    py_web = os.path.join(args.py_root, "web")
    if not os.path.isdir(py_web):
        print(f"[!] 找不到 Python 前端目录: {py_web}", file=sys.stderr)
        return 2
    if not os.path.exists(args.py_root):
        print(f"[!] 找不到 Python oracle: {args.py_root}", file=sys.stderr)
        return 2

    go = f"http://127.0.0.1:{args.go_port}"
    py = f"http://127.0.0.1:{args.py_port}"

    procs = []
    if not args.keep:
        procs, ok = start_servers(args)
        if not ok:
            stop_servers(procs)
            return 2
    elif not (port_open(args.go_port) and port_open(args.py_port)):
        print("[!] --keep 但两个端口没有都在监听", file=sys.stderr)
        return 2

    try:
        run_matrix(go, py, py_web)
    finally:
        if not args.keep:
            stop_servers(procs)

    print(f"\n{'='*70}")
    print(f"比较端点 {checked} 个；真实差异 {len(diffs)} 处；"
          f"已知装饰性差异 {cosmetic} 处")
    if cosmetic:
        print("  （装饰性：非法 run_id 含 .. 时 artifacts.root 的路径归一化 —— "
              "Python 不归一、Go 的 filepath.Join 会归一；不影响任何读取）")
    for d in diffs:
        print("  ✗ " + d)
    print("结果: " + ("✅ 语义一致" if not diffs else "❌ 有真实差异"))
    print("=" * 70)
    return 0 if not diffs else 1


if __name__ == "__main__":
    raise SystemExit(main())
