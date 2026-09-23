#!/usr/bin/env python3
"""P0 差分验证器 —— DB 层（Go vs Python 原版）。

用法:
    python3 tools/diff/run_db_diff.py [--db PATH] [--py-root DIR]

默认对 `~/.ruiyun-autotest/testcases.db` 做验证。

安全约定:
    **绝不写生产库**。脚本先把 db/-wal/-shm 复制到临时目录，
    再给两侧各一份独立副本（避免任何一方建表/建索引影响另一方）。

比较口径:
    - total / limit / offset / scenes / targets / cases 的 id 序列；
    - 每条 case 的字段（忽略 Go 侧多出的顶层 scene/targets —— 见报告 §4）。
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
GO_ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
DEFAULT_PY_ROOT = os.path.abspath(os.path.join(GO_ROOT, "..", "ruiyun-ui-test-platform"))

# Go 侧输出比 Python 多出的顶层键（前端只读 labels.*，这两个键未被使用）。
# 归一化掉，避免永久性的假阳性差异；如需严格比对见 --strict。
GO_EXTRA_CASE_KEYS = {"scene", "targets"}


def copy_db(src: str, dst: str) -> None:
    for suffix in ("", "-wal", "-shm"):
        s = src + suffix
        if os.path.exists(s):
            shutil.copy2(s, dst + suffix)


def run(cmd, env=None):
    proc = subprocess.run(cmd, capture_output=True, text=True, env=env)
    if proc.returncode != 0:
        print(f"[!] 命令失败: {' '.join(cmd)}", file=sys.stderr)
        print(proc.stderr[:2000], file=sys.stderr)
        sys.exit(2)
    return proc.stdout


def strip_keys(case: dict, keys) -> dict:
    return {k: v for k, v in case.items() if k not in keys}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=os.path.expanduser("~/.ruiyun-autotest/testcases.db"))
    ap.add_argument("--py-root", default=os.environ.get("RUIYUN_PY_ROOT", DEFAULT_PY_ROOT))
    ap.add_argument("--strict", action="store_true",
                    help="不忽略 Go 额外顶层键")
    args = ap.parse_args()

    if not os.path.exists(args.db):
        print(f"[!] 数据库不存在: {args.db}", file=sys.stderr)
        return 2

    tmp = tempfile.mkdtemp(prefix="ruiyun_dbdiff_")
    try:
        go_db = os.path.join(tmp, "go.db")
        py_db = os.path.join(tmp, "py.db")
        copy_db(args.db, go_db)
        copy_db(args.db, py_db)
        print(f"[*] 已复制生产库到临时目录（原库不会被修改）")

        go_bin = os.path.join(tmp, "dbdiff")
        print("[*] 构建 Go 侧 ...")
        subprocess.run(["go", "build", "-o", go_bin, "./cmd/dbdiff"],
                       cwd=GO_ROOT, check=True)

        print("[*] Go 侧运行 ...")
        go = json.loads(run([go_bin, go_db]))

        venv_py = os.path.join(args.py_root, ".venv", "bin", "python3")
        py_bin = venv_py if os.path.exists(venv_py) else sys.executable
        print(f"[*] Python 侧运行 ({py_bin}) ...")
        env = dict(os.environ, RUIYUN_PY_ROOT=args.py_root)
        py = json.loads(run([py_bin, os.path.join(HERE, "db_ref.py"), py_db], env=env))

        print(f"\n{'='*70}\nDB 层差分结果\n{'='*70}")

        fails = 0
        if go.get("count") != py.get("count"):
            fails += 1
            print(f"  [DIFF] count: go={go.get('count')} py={py.get('count')}")
        else:
            print(f"  [OK  ] count = {go.get('count')}")

        gq, pq = go.get("queries", {}), py.get("queries", {})
        print(f"\n  查询电池（{len(pq)} 组）:")

        def norm(cases):
            if args.strict:
                return cases
            return [strip_keys(c, GO_EXTRA_CASE_KEYS) for c in (cases or [])]

        for name in pq:
            g, p = gq.get(name, {}), pq.get(name, {})
            gsig = (g.get("total"), g.get("limit"), g.get("offset"),
                    g.get("scenes"), g.get("targets"),
                    [c.get("id") for c in (g.get("cases") or [])])
            psig = (p.get("total"), p.get("limit"), p.get("offset"),
                    p.get("scenes"), p.get("targets"),
                    [c.get("id") for c in (p.get("cases") or [])])
            content_ok = norm(g.get("cases")) == norm(p.get("cases"))
            if gsig == psig and content_ok:
                print(f"  [OK  ] {name:18} total={p.get('total'):<5} "
                      f"limit={p.get('limit'):<4} cases={len(p.get('cases') or [])}")
            else:
                fails += 1
                print(f"  [DIFF] {name:18} total go={g.get('total')} py={p.get('total')} | "
                      f"limit go={g.get('limit')} py={p.get('limit')} | "
                      f"cases go={len(g.get('cases') or [])} py={len(p.get('cases') or [])}")

        # labels_index
        if go.get("labels_index") == py.get("labels_index"):
            print(f"\n  [OK  ] labels_index ({len(go.get('labels_index') or {})} 个键)")
        else:
            fails += 1
            print(f"\n  [DIFF] labels_index: go={len(go.get('labels_index') or {})} "
                  f"py={len(py.get('labels_index') or {})}")

        print(f"\n{'='*70}")
        print("结果: " + ("✅ 语义一致" if fails == 0 else f"❌ {fails} 处真实差异"))
        if not args.strict:
            print(f"注: 已忽略 Go 侧多出的顶层键 {sorted(GO_EXTRA_CASE_KEYS)}"
                  "（前端只读 labels.*，未使用）")
        print("=" * 70)
        return 0 if fails == 0 else 1
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
