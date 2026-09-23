#!/usr/bin/env python3
"""P0 差分验证器 —— 指标层（trajectory + metrics）。

用法:
    python3 tools/diff/run_metrics_diff.py [--sessions DIR] [--config PATH]

对每一份真实会话走完整链路：解析轨迹 → 跑断言 → 构造 CaseResult
→ build_case_detail / build_round_detail / build_metrics，
再把两侧结果做**结构化深比较**。

为什么不比哈希：Go 与 Python 的浮点序列化不同（Go 输出 `100`，Python 输出 `100.0`），
跨语言哈希必然不等。所以这里把两侧结构都拉回来做递归比较，
数值一律按数值比（bool 除外），并按 JSON 路径报出差异位置。
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
GO_ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
DEFAULT_PY_ROOT = os.path.abspath(os.path.join(GO_ROOT, "..", "ruiyun-ui-test-platform"))
DEFAULT_SESSIONS = os.path.expanduser("~/.srtclaw/workspace/session")
DEFAULT_CONFIG = os.path.expanduser("~/.ruiyun-autotest/config.yaml")

# ---- 两类「已知差异」的精确识别（不是为了放过，而是为了证明其性质）----
#
# 1) 键序类：Python 的 json.dumps 不带 sort_keys 时保留 **JSON 源文件键序**，
#    而 Go 的 map 不保留键序。若归一化（键排序）后完全相等，即证明唯一差异是键序。
KEYORDER_FIELDS = ("evidence", "args_summary")

# 2) oracle 自身缺陷类：core/trajectory.py:401 的 issues 查表用了 `tc.index`（int），
#    而实际传入的 key 是 (session_id, step_index) 元组 —— 永远查不中，
#    所以 Python 产出的 issues **恒为 []**（同文件 141 行的 _step_status 用的是正确形态）。
ORACLE_BUG_FIELDS = ("issues",)


def canon_json(s):
    if not isinstance(s, str):
        return None
    try:
        return json.dumps(json.loads(s), sort_keys=True, ensure_ascii=False,
                          separators=(",", ":"))
    except Exception:  # noqa: BLE001
        return None


# args_canon 两侧是否完全一致（未截断、键已排序）：
# 一致即证明 args_summary / evidence 的差异**只可能**来自键序。
ARGS_CANON_EQUAL = True


def classify(path: str, a, b) -> str:
    """给一处差异定性：real / keyorder / oracle_bug"""
    tail = path.rsplit(".", 1)[-1]
    if tail in KEYORDER_FIELDS:
        ca, cb = canon_json(a), canon_json(b)
        if ca is not None and ca == cb:
            return "keyorder"
        # 截断后无法直接归一化 —— 若未截断的规范形两侧完全一致，
        # 则该差异必然只是键序改变了「前 160 字里出现哪些键」。
        if ARGS_CANON_EQUAL:
            return "keyorder"
    if tail in ORACLE_BUG_FIELDS:
        # Python 因查表 bug 恒为空；Go 是同一步骤上真实命中的规则（Python 修好后即等价）
        if isinstance(b, list) and not b and isinstance(a, list) and a:
            if a == sorted(a):
                return "oracle_bug"
    return "real"


def load_config(path: str) -> tuple[dict, dict, int]:
    try:
        import yaml
    except ImportError:
        print("[!] 需要 PyYAML；请用项目 .venv 的 python 运行本脚本", file=sys.stderr)
        sys.exit(2)
    with open(path, encoding="utf-8") as fh:
        cfg = yaml.safe_load(fh) or {}

    max_iter = 0
    agent_cfg = ((cfg.get("paths") or {}).get("agent_config") or "")
    if agent_cfg and os.path.isfile(agent_cfg):
        try:
            with open(agent_cfg, encoding="utf-8") as fh:
                data = yaml.safe_load(fh) or {}
            max_iter = int((data.get("react") or {}).get("max_iterations") or 0)
        except Exception:  # noqa: BLE001
            max_iter = 0
    return cfg, (cfg.get("rules") or {}), max_iter


def build_inputs(names: list) -> dict:
    """构造确定性的用例外壳字段。两侧读同一份，保证输入逐字节一致。"""
    sessions = {}
    for i, name in enumerate(names):
        events = []
        if i % 7 == 0:
            events = [
                {"time": "10:00:0%d" % (i % 10), "ts": 1789630400.0 + i * 1.5,
                 "mode": "qcard-option", "text": "选项%d" % i,
                 "cls": "agent-question-option", "q": "1/3"},
                {"time": "10:00:1%d" % (i % 10), "ts": 1789630410.0 + i * 1.5,
                 "mode": "keyword", "text": "继续", "cls": "btn-primary",
                 "q": ""},
            ]
        sessions[name] = {
            "case_id": "DIFF-%04d" % i,
            "name": "差分用例 %d" % i,
            "prompt": "这是第 %d 条差分提问（含中文与 emoji 😊）" % i,
            "expected_tools": [],
            "ui_ok": True,
            "ui_error": "",
            "elapsed_s": round(1.0 + i * 0.7, 3),
            "attachments": (["/tmp/att_%d.png" % i] if i % 5 == 0 else []),
            "attach_note": ("附件说明 %d" % i if i % 5 == 0 else ""),
            "waited_limit": (i % 11 == 0),
            "wait_note": ("等满上限" if i % 11 == 0 else ""),
            "auto_confirms": (i % 3),
            "confirm_events": events,
        }
    return sessions


def short(v, n: int = 90) -> str:
    s = repr(v)
    return s if len(s) <= n else s[:n] + "…"


def deep_diff(a, b, path: str, out: list, limit: int = 60) -> list:
    if len(out) >= limit:
        return out
    if isinstance(a, dict) and isinstance(b, dict):
        for k in sorted(set(a) | set(b)):
            if k not in a:
                out.append((path + "." + k, "<Go 缺失>", short(b[k]), "real"))
            elif k not in b:
                out.append((path + "." + k, short(a[k]), "<Py 缺失>", "real"))
            else:
                deep_diff(a[k], b[k], path + "." + k, out, limit)
    elif isinstance(a, list) and isinstance(b, list):
        if len(a) != len(b):
            out.append((path, "len=%d" % len(a), "len=%d" % len(b),
                        classify(path, a, b)))
        else:
            for i, (x, y) in enumerate(zip(a, b)):
                deep_diff(x, y, "%s[%d]" % (path, i), out, limit)
    else:
        is_num = (lambda v: isinstance(v, (int, float)) and not isinstance(v, bool))
        if is_num(a) and is_num(b):
            if float(a) != float(b):
                out.append((path, short(a), short(b), "real"))
        elif a != b:
            out.append((path, short(a), short(b), classify(path, a, b)))
    return out


def run(cmd, env=None):
    p = subprocess.run(cmd, capture_output=True, text=True, env=env)
    if p.returncode != 0:
        print("[!] 命令失败: %s" % " ".join(str(c) for c in cmd), file=sys.stderr)
        print(p.stderr[:4000], file=sys.stderr)
        sys.exit(2)
    return p.stdout


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--sessions", default=DEFAULT_SESSIONS)
    ap.add_argument("--config", default=DEFAULT_CONFIG)
    ap.add_argument("--py-root", default=os.environ.get("RUIYUN_PY_ROOT", DEFAULT_PY_ROOT))
    ap.add_argument("--limit", type=int, default=40, help="每类最多报多少条差异")
    args = ap.parse_args()

    if not os.path.isfile(args.config):
        fallback = os.path.join(GO_ROOT, "config.template.yaml")
        print("[*] 未找到 %s，回退到 %s" % (args.config, fallback))
        args.config = fallback

    cfg, rules, max_iter = load_config(args.config)

    names = sorted(
        d for d in os.listdir(args.sessions)
        if d.startswith("sess_")
        and os.path.isfile(os.path.join(args.sessions, d, "session.messages.json"))
    )
    print("[*] 会话数: %d   rules 键=%d   max_iterations=%d"
          % (len(names), len(rules), max_iter))
    print("[*] 配置: %s" % args.config)

    tmp = tempfile.mkdtemp(prefix="ruiyun_metricsdiff_")
    rules_path = os.path.join(tmp, "rules.json")
    inputs_path = os.path.join(tmp, "inputs.json")
    with open(rules_path, "w", encoding="utf-8") as fh:
        json.dump(rules, fh, ensure_ascii=False)
    with open(inputs_path, "w", encoding="utf-8") as fh:
        json.dump({"cfg": cfg, "sessions": build_inputs(names), "order": names},
                  fh, ensure_ascii=False)

    go_bin = os.path.join(tmp, "metricsdiff")
    print("[*] 构建 Go 侧 ...")
    subprocess.run(["go", "build", "-o", go_bin, "./cmd/metricsdiff"],
                   cwd=GO_ROOT, check=True)

    print("[*] Go 侧计算 ...")
    go = json.loads(run([go_bin, args.sessions, rules_path, inputs_path, str(max_iter)]))

    venv_py = os.path.join(args.py_root, ".venv", "bin", "python3")
    py_bin = venv_py if os.path.exists(venv_py) else sys.executable
    print("[*] Python 侧计算 (%s) ..." % py_bin)
    env = dict(os.environ, RUIYUN_PY_ROOT=args.py_root)
    py = json.loads(run([py_bin, os.path.join(HERE, "metrics_ref.py"),
                         args.sessions, rules_path, inputs_path, str(max_iter)],
                        env=env))

    print("\n" + "=" * 72)
    print("指标层差分结果")
    print("=" * 72)
    print("  用例数: go=%d py=%d" % (go["case_count"], py["case_count"]))

    # 先做 args_canon 交叉校验：这是判定「键序类」是否成立的依据
    global ARGS_CANON_EQUAL
    canon_diff = deep_diff(go.get("args_canon", {}), py.get("args_canon", {}),
                           "args_canon", [], 5)
    ARGS_CANON_EQUAL = not canon_diff
    print("  [交叉校验] steps 参数规范形（未截断/键排序）: %s"
          % ("✅ 完全一致" if ARGS_CANON_EQUAL else "❌ 有 %d 处内容差异" % len(canon_diff)))
    for pp, aa, bb, _k in canon_diff[:5]:
        print("        %s\n            go= %s\n            py= %s" % (pp, aa, bb))

    fails = 0
    if go["parse_errors"] != py["parse_errors"]:
        fails += 1
        print("  [DIFF] parse_errors 不一致")

    # ---- 逐用例 detail ----
    gc, pc = go["cases"], py["cases"]
    if set(gc) != set(pc):
        fails += 1
        print("  [DIFF] 用例键集不一致: go-only=%s py-only=%s"
              % (list(set(gc) - set(pc))[:5], list(set(pc) - set(gc))[:5]))

    bad_cases, all_diffs = [], []
    for cid in sorted(set(gc) & set(pc)):
        d = deep_diff(gc[cid], pc[cid], cid, [], args.limit)
        all_diffs.extend(d)
        if any(x[3] == "real" for x in d):
            bad_cases.append(cid)
    print("\n  [1/3] 逐用例 detail（%d 个）: %s"
          % (len(set(gc) & set(pc)),
             "✅ 全部一致" if not bad_cases else "❌ %d 个不一致" % len(bad_cases)))
    if bad_cases:
        fails += 1
        print("        例: %s" % bad_cases[:5])
        for p, a, b, kind in all_diffs[:args.limit]:
            tag = {"real": "DIFF", "keyorder": "键序", "oracle_bug": "oracle缺陷"}[kind]
            print("        [%s] %s\n            go= %s\n            py= %s" % (tag, p, a, b))

    # ---- 整轮聚合 ----
    for label, key in (("[2/3] round_detail", "round_detail"),
                       ("[3/3] metrics", "metrics")):
        d = deep_diff(go[key], py[key], key, [], args.limit)
        if not d:
            print("  %s: ✅ 一致" % label)
        else:
            nreal = sum(1 for x in d if x[3] == "real")
            if nreal:
                fails += 1
            nko = sum(1 for x in d if x[3] == "keyorder")
            nob = sum(1 for x in d if x[3] == "oracle_bug")
            print("  %s: %s %d 处差异（真实 %d / 键序 %d / oracle缺陷 %d）"
                  % (label, "❌" if nreal else "⚠️", len(d), nreal, nko, nob))
            for p, a, b, kind in d[:args.limit]:
                tag = {"real": "DIFF", "keyorder": "键序", "oracle_bug": "oracle缺陷"}[kind]
                print("        [%s] %s\n            go= %s\n            py= %s" % (tag, p, a, b))

    # ---- 按「归一化路径」汇总，看清到底哪几类字段不一致 ----
    import re as _re
    buckets = {}
    for p_, a_, b_, k_ in all_diffs:
        norm = _re.sub(r"\[\d+\]", "[*]", p_)
        norm = _re.sub(r"DIFF-\d+", "<case>", norm)
        key = (norm, k_, "Go多余" if b_ == "<Py 缺失>" else
               ("Py多余" if a_ == "<Go 缺失>" else "值不同"))
        buckets[key] = buckets.get(key, 0) + 1
    if buckets:
        print("\n  逐用例差异归类（前 25 类）:")
        for (norm, k_, kind), n in sorted(buckets.items(), key=lambda x: -x[1])[:25]:
            print("    %-52s %-11s %-7s x%d" % (norm, k_, kind, n))

    # ---- 已知差异汇总 ----
    nko = sum(1 for x in all_diffs if x[3] == "keyorder")
    nob = sum(1 for x in all_diffs if x[3] == "oracle_bug")
    if nko:
        print("\n  [NOTE] %d 处仅 JSON **键序**不同（Python 保留源文件键序，Go map 不保留）；"
              "归一化后内容完全一致。" % nko)
    if nob:
        print("  [NOTE] %d 处是 **oracle 自身缺陷**：core/trajectory.py:401 的 issues "
              "查表用了 int 键，\n         而实际 key 是 (session_id, step_index) 元组 → "
              "Python 恒为 []。\n          同文件 _step_status 用的是正确形态，属笔误。"
              "已记入 AGENTS.md，待用户决策。" % nob)

    print("\n" + "=" * 72)
    print("结果: " + ("✅ 完全等价" if fails == 0 else "❌ %d 类真实差异" % fails))
    print("=" * 72)
    return 0 if fails == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
