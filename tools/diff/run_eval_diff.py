"""eval 层差分验证运行器（formatcheck + safetyscan + intent）。

设计要点（与指标层一致）：

1. **两侧读同一份 inputs.json** —— 输入逐字节一致，差异只可能来自实现；
2. **结构化比较，不比哈希** —— 浮点序列化跨语言不同（Go `100` / Python `100.0`）；
3. **语料 = 真实会话 + 对抗注入** —— 真实语料保证覆盖面，
   对抗注入保证已知陷阱（Unicode `\\s`、空值、边界）一定被踩到。

用法:
    python3 tools/diff/run_eval_diff.py [--limit N] [--strict]

退出码 0 = 等价，1 = 有真实差异。
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
GO_ROOT = HERE.parents[1]
DEFAULT_SESSIONS = Path.home() / ".srtclaw" / "workspace" / "session"


def find_python() -> str:
    """定位 Python 原版项目的解释器（含 core 依赖）。"""
    for base in (GO_ROOT.parent / "ruiyun-ui-test-platform",
                 Path("/Users/amano/WorkSpace/ruiyun-ui-test-platform")):
        cand = base / ".venv" / "bin" / "python3"
        if cand.is_file():
            return str(cand)
    return sys.executable


def find_go() -> str:
    for cand in ("go", "/usr/local/go/bin/go", "/opt/homebrew/bin/go"):
        if shutil.which(cand) or Path(cand).is_file():
            return cand
    raise SystemExit("找不到 go")


# --------------------------------------------------------------------------
# 构造语料
# --------------------------------------------------------------------------

# Python re 的 \s 匹配、而 Go regexp 的 \s **不**匹配的字符。
# 这些正是本轮要抓的陷阱（全角空格在中文文档里极常见）。
UNICODE_SPACES = [
    ("全角空格U+3000", "\u3000"),
    ("NBSP_U+00A0", "\u00a0"),
    ("EM_SP_U+2003", "\u2003"),
    ("VT_U+000B", "\u000b"),
    ("行分隔U+2028", "\u2028"),
    ("NEL_U+0085", "\u0085"),
    ("文件分隔U+001C", "\u001c"),
    ("OGHAM_U+1680", "\u1680"),
    ("窄NBSP_U+202F", "\u202f"),
    ("表意空格_U+2000", "\u2000"),
]


def build_inputs(names: list, session_root: Path, cfg: dict) -> dict:
    """把真实会话 + 对抗注入拼成一份确定性输入。"""
    sys.path.insert(0, str(_project_root()))
    from core.log_parser import parse_session
    from core.artifacts import extract_artifacts
    from core.case_intent import infer_target_kinds

    weights = cfg.get("format_weights") or {}
    cases: dict = {}
    order: list = []
    skipped: list = []

    for i, name in enumerate(names):
        try:
            trace = parse_session(session_root / name)   # 必须是 Path，传 str 会 TypeError
        except Exception as exc:  # noqa: BLE001
            skipped.append(f"{name}: {type(exc).__name__}: {exc}")
            continue
        if trace is None:
            skipped.append(f"{name}: parse_session 返回 None")
            continue
        arts = extract_artifacts(trace.tool_calls)
        final_answer = getattr(trace, "final_answer", "") or ""
        art_texts = [[lab, txt] for lab, txt in arts.texts()]
        haystack = final_answer + "\n" + "\n".join(t for _, t in art_texts)
        prompt = getattr(trace, "turn_prompt", "") or ""
        if not prompt:
            prompt = "第 %d 条会话问题" % i
        req_kinds, _ = infer_target_kinds(prompt)
        # labels 用真实 targets（有则用，无则空）+ attachment
        targets = sorted(req_kinds) if req_kinds else []
        labels = {"targets": targets, "attachment": bool(art_texts)}
        tool_calls = [
            {"name": getattr(tc, "name", "") or "",
             "arguments": getattr(tc, "arguments", None)}
            for tc in (trace.tool_calls or [])
        ]

        cid = "R-%04d" % i
        order.append(cid)
        cases[cid] = {
            "labels": labels,
            "artifact_kinds": sorted(arts.kinds or []),
            "requirements": [],
            "haystack": haystack,
            "weights": weights,
            "kinds": sorted(req_kinds) if req_kinds else None,
            "final_answer": final_answer,
            "artifact_texts": art_texts,
            "tool_calls": tool_calls,
            "prompt": prompt,
        }

        # --- 变体 A：要求项里塞入 Unicode 空白（打 _norm / _SPLIT）---
        # 每个会话轮换一种空白字符，保证 10 种都被覆盖
        sp = UNICODE_SPACES[i % len(UNICODE_SPACES)][1]
        reqs = [
            "包含封面与目录",
            "正文不少于 800 字",
            "使用宋体",
            "包含" + sp + "页眉",
            "参考" + sp + "文献" + sp + "列表",
        ]
        aid = "A-%04d" % i
        order.append(aid)
        cases[aid] = dict(cases[cid])
        cases[aid]["requirements"] = reqs
        # haystack 也要带 Unicode 空白，否则「两边都不命中」看不出差异
        cases[aid]["haystack"] = (
            "封面\n目录\n正文\n页眉\n参考文献列表\n" + sp_join() + "\n" + haystack
        )
        cases[aid]["weights"] = {"type": 0.4, "coverage": 0.6}

        # --- 变体 B：安全扫描源文本里塞入 Unicode 空白（打 _exec_intent/_refusal）---
        bid = "B-%04d" % i
        order.append(bid)
        cases[bid] = dict(cases[cid])
        cases[bid]["final_answer"] = (
            "我不能执行这个。" + sp + "`rm -rf /` 会删除系统文件。\n"
            + sp_join() + "eval(1)\n" + final_answer
        )
        cases[bid]["tool_calls"] = tool_calls + [
            {"name": "run_shell", "arguments": {"cmd": "rm" + sp + "-rf" + sp + "/"}},
            {"name": "py", "arguments": {"code": "os.system" + sp + "('ls')"}},
        ]

    # --- 纯对抗用例：不依赖真实会话，专门打边界 ---
    for label, sp in UNICODE_SPACES:
        cid = "S-" + label
        order.append(cid)
        cases[cid] = {
            "labels": {"targets": ["docx"], "attachment": False},
            "artifact_kinds": ["docx"],
            "requirements": ["包含" + sp + "封面", sp + "目录", "正文"],
            "haystack": "封面" + sp + "目录" + sp + "正文",
            "weights": {"type": 0.5, "coverage": 0.5},
            "kinds": None,
            "final_answer": "拒绝执行" + sp + "`rm" + sp + "-rf" + sp + "/`" + sp + "命令。",
            "artifact_texts": [[sp + "产出物", "内容" + sp + "正文"]],
            "tool_calls": [{"name": "t", "arguments": {"k": "v" + sp + "w"}}],
            "prompt": "请生成" + sp + "一个" + sp + "word" + sp + "文档" + sp + "包含表格",
        }
    if skipped:
        print(f"   ⚠️ {len(skipped)} 个会话未纳入语料，例如: {skipped[:3]}")
    return {"cases": cases, "order": order}


_SP_CACHE: list = []


def sp_join() -> str:
    if not _SP_CACHE:
        _SP_CACHE.append("".join(sp for _, sp in UNICODE_SPACES))
    return _SP_CACHE[0]


def _project_root() -> Path:
    for base in (GO_ROOT.parent / "ruiyun-ui-test-platform",
                 Path("/Users/amano/WorkSpace/ruiyun-ui-test-platform")):
        if (base / "core" / "log_parser.py").is_file():
            return base
    raise SystemExit("找不到 Python 原版项目根")


def load_cfg() -> dict:
    """读真实运行配置（两侧一致）。"""
    p = Path.home() / ".ruiyun-autotest" / "config.yaml"
    if not p.is_file():
        return {}
    try:
        import yaml
        with open(p, encoding="utf-8") as fh:
            return yaml.safe_load(fh) or {}
    except Exception:  # noqa: BLE001
        return {}


# --------------------------------------------------------------------------
# 比较
# --------------------------------------------------------------------------

def deep_diff(a, b, path: str, out: list, limit: int = 100000) -> None:
    """递归结构化比较：数值按数值比（bool 除外），字符串严格比。"""
    if len(out) >= limit:
        return
    if isinstance(a, bool) or isinstance(b, bool):
        if a is not b:
            out.append((path, a, b))
        return
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        if abs(float(a) - float(b)) > 1e-12:
            out.append((path, a, b))
        return
    if type(a) is not type(b) and not (a is None or b is None):
        out.append((path, f"<{type(a).__name__}> {a!r}", f"<{type(b).__name__}> {b!r}"))
        return
    if a is None or b is None:
        if a is not b:
            out.append((path, a, b))
        return
    if isinstance(a, dict):
        for k in sorted(set(a) | set(b)):
            if k not in a:
                out.append((f"{path}.{k}", "<Go 缺失>", b[k]))
            elif k not in b:
                out.append((f"{path}.{k}", a[k], "<Python 缺失>"))
            else:
                deep_diff(a[k], b[k], f"{path}.{k}", out, limit)
        return
    if isinstance(a, list):
        if len(a) != len(b):
            out.append((f"{path}.length", len(a), len(b)))
        for i, (x, y) in enumerate(zip(a, b)):
            deep_diff(x, y, f"{path}[{i}]", out, limit)
        return
    if a != b:
        out.append((path, a, b))


def canon_json(s):
    """把「JSON 对象文本」归一化：解析后按 key 排序再 dump。

    用途：证明 `sources` 里的差异**只是键序**，而不是内容不同。
    Python 的 json.dumps 保留 JSON 源文件键序，Go 的 map 无法保留 ——
    这是已知且已被证明无害的类别（见 G 报告 §「键序类差异」）。
    非 JSON 文本原样返回。
    """
    if not isinstance(s, str) or not s.startswith(("{", "[")):
        return s
    try:
        obj = json.loads(s)
    except (ValueError, TypeError):
        return s
    if isinstance(obj, dict):
        return json.dumps(obj, ensure_ascii=False, sort_keys=True)
    return s


def canonize_sources(v):
    """对 safetyscan 结果做「键序无关」归一化。"""
    if not isinstance(v, dict):
        return v
    out = dict(v)
    if isinstance(out.get("sources"), list):
        out["sources"] = [[lbl, canon_json(txt)] for lbl, txt in out["sources"]]
    return out


def show(v, n: int = 100) -> str:
    s = repr(v)
    return s if len(s) <= n else s[:n] + "…"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--limit", type=int, default=100000, help="最多处理多少个会话")
    ap.add_argument("--sessions", default=str(DEFAULT_SESSIONS))
    ap.add_argument("--strict", action="store_true", help="忽略已知无害类别")
    ap.add_argument("--show", type=int, default=12, help="每层最多打印多少条差异样本")
    ap.add_argument("--dump", metavar="DIR", help="把两侧原始输出与差异明细落盘，便于事后分析")
    args = ap.parse_args()

    session_root = Path(args.sessions)
    names = sorted(d.name for d in session_root.iterdir()
                   if d.is_dir() and d.name.startswith("sess_"))[: args.limit]
    if not names:
        print("没找到会话语料", file=sys.stderr)
        return 2

    cfg = load_cfg()
    print("=" * 70)
    print("eval 层差分（formatcheck + safetyscan + intent）")
    print("=" * 70)
    print(f"会话数: {len(names)}（+ 每个 3 个变体 + {len(UNICODE_SPACES)} 个对抗用例）")

    payload = build_inputs(names, session_root, cfg)
    print(f"用例总数: {len(payload['order'])}")

    tmp = tempfile.mkdtemp(prefix="evaldiff-")
    env = dict(os.environ, GOCACHE=os.environ.get("GOCACHE", "/Users/amano/WorkSpace/.gocache"))
    try:
        inputs_path = os.path.join(tmp, "inputs.json")
        with open(inputs_path, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, ensure_ascii=False)

        go_bin = os.path.join(tmp, "evaldiff")
        subprocess.run([find_go(), "build", "-o", go_bin, "./cmd/evaldiff"],
                       cwd=GO_ROOT, env=env, check=True)
        go_out = json.loads(subprocess.run(
            [go_bin, inputs_path], cwd=GO_ROOT, env=env,
            capture_output=True, text=True, check=True).stdout)
        py_out = json.loads(subprocess.run(
            [find_python(), str(HERE / "eval_ref.py"), inputs_path],
            cwd=GO_ROOT, env=env, capture_output=True, text=True, check=True).stdout)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)

    if args.dump:
        os.makedirs(args.dump, exist_ok=True)
        for tag, obj in (("go", go_out), ("py", py_out), ("inputs", payload)):
            with open(os.path.join(args.dump, tag + ".json"), "w", encoding="utf-8") as fh:
                json.dump(obj, fh, ensure_ascii=False, indent=1)
        print(f"\n已落盘到 {args.dump}/")

    if py_out.get("errors"):
        print("\n⚠️  Python 侧抛异常（说明输入构造有问题）:")
        for k, v in list(py_out["errors"].items())[:10]:
            print(f"   {k}: {v}")
    if go_out.get("errors"):
        print("\n⚠️  Go 侧抛异常:")
        for k, v in list(go_out["errors"].items())[:10]:
            print(f"   {k}: {v}")

    real_total = 0
    for layer in ("formatcheck", "safetyscan", "intent"):
        g, p = go_out.get(layer, {}), py_out.get(layer, {})
        diffs: list = []
        canon_diffs: list = []
        bad_case = None
        for cid in payload["order"]:
            if cid not in g or cid not in p:
                diffs.append((f"{cid}.<存在性>", cid in g, cid in p))
                continue
            before = len(diffs)
            deep_diff(g[cid], p[cid], cid, diffs)
            if len(diffs) > before and bad_case is None:
                bad_case = cid
            if layer == "safetyscan":
                deep_diff(canonize_sources(g[cid]), canonize_sources(p[cid]),
                          cid, canon_diffs)
        status = "✅ 一致" if not diffs else f"❌ {len(diffs)} 处差异"
        print(f"\n[{layer}] {status}（比对 {len(payload['order'])} 个用例）")
        if diffs:
            # 按「差异路径的末段」归类，快速看出主要矛盾在哪
            import collections
            bucket = collections.Counter()
            for path, _a, _b in diffs:
                tail = path.split(".")[-1].split("[")[0]
                bucket[tail] += 1
            print("   差异归类（路径末段 → 次数）:")
            for k, n in bucket.most_common(12):
                print(f"      {k:34s} {n}")
            if layer == "safetyscan":
                ko = len(diffs) - len(canon_diffs)
                print(f"   其中「键序无关归一化后消失」的: {ko} 条（JSON 对象键序，已知无害）")
                print(f"   归一化后**仍然存在**的真实差异: {len(canon_diffs)} 条")
            real_total += len(canon_diffs) if layer == "safetyscan" else len(diffs)
        else:
            if layer == "safetyscan":
                real_total += 0

            print(f"   首个不一致用例: {bad_case}")
            for path, a, b in diffs[: args.show]:
                print(f"   · {path}")
                print(f"       Go     = {show(a)}")
                print(f"       Python = {show(b)}")

    print("\n" + "=" * 70)
    print(f"结果: {'✅ 完全等价' if real_total == 0 else f'❌ {real_total} 处真实差异'}")
    print("=" * 70)
    return 0 if real_total == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
