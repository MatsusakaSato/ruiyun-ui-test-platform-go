#!/usr/bin/env python3
"""P0 差分验证器 —— Go 版 vs Python 原版（oracle）。

用法:
    python3 tools/diff/run_diff.py <file.xlsx> [--max-rows N]
                                   [--py-root DIR] [--keep-going]

行为:
    1. 构建并运行 Go 侧 cmd/xlsxdiff；
    2. 运行 Python 侧 tools/diff/xlsx_ref.py（import 真实生产实现）；
    3. 对已知「顺序不确定」的字段做归一化后逐字段比对；
    4. 语义不一致 → 退出码 1；一致 → 退出码 0。

为什么需要归一化:
    Python 原版 core/xlsx.py 的 infer_targets 遍历 set
    （顺序随 PYTHONHASHSEED 变化），Go 早期移植遍历 map（同样随机）。
    因此 targets 的「顺序」本身不可作为契约，只有「集合」是契约。
    若两侧都改成确定性实现，可用 --strict 关闭归一化。
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

# 已知顺序不确定的字段路径（归一化时排序）
ORDER_UNSTABLE = {("targets",), ("labels", "targets")}


def canon(obj, strict: bool):
    """递归规范化。strict=True 时不做任何排序，用于逐字节比对。"""
    if strict:
        return obj
    if isinstance(obj, dict):
        out = {}
        for k, v in obj.items():
            if (k,) in ORDER_UNSTABLE and isinstance(v, list):
                out[k] = sorted(v)
            else:
                out[k] = canon(v, strict)
        # labels.targets 是嵌套的，单独处理
        if "labels" in out and isinstance(out["labels"], dict):
            lb = out["labels"]
            if isinstance(lb.get("targets"), list):
                lb = dict(lb, targets=sorted(lb["targets"]))
                out["labels"] = lb
        return out
    if isinstance(obj, list):
        return [canon(v, strict) for v in obj]
    return obj


def run(cmd, **kw):
    proc = subprocess.run(cmd, capture_output=True, text=True, **kw)
    if proc.returncode != 0:
        print(f"[!] 命令失败 ({proc.returncode}): {' '.join(cmd)}", file=sys.stderr)
        if proc.stderr:
            print(proc.stderr[:2000], file=sys.stderr)
        sys.exit(2)
    return proc.stdout


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("file")
    ap.add_argument("--max-rows", type=int, default=0)
    ap.add_argument("--py-root", default=os.environ.get("RUIYUN_PY_ROOT", DEFAULT_PY_ROOT))
    ap.add_argument("--strict", action="store_true",
                    help="不做顺序归一化，逐字节比对（仅在两侧都已确定时可用）")
    args = ap.parse_args()

    # 构建到临时目录，避免把二进制留进源码树
    go_bin = os.path.join(tempfile.mkdtemp(prefix="ruiyun_xlsxdiff_"), "xlsxdiff")
    print(f"[*] 构建 Go 侧: {go_bin}")
    subprocess.run(["go", "build", "-o", go_bin, "./cmd/xlsxdiff"],
                   cwd=GO_ROOT, check=True)

    print(f"[*] Go 侧运行 ...")
    go_out = run([go_bin, args.file, str(args.max_rows)])

    # Python 侧优先用原项目的 venv，保证依赖（PyYAML 等）可用
    venv_py = os.path.join(args.py_root, ".venv", "bin", "python3")
    py_bin = venv_py if os.path.exists(venv_py) else sys.executable

    print(f"[*] Python 侧运行 ({py_bin}) ...")
    env = dict(os.environ, RUIYUN_PY_ROOT=args.py_root)
    py_out = run([py_bin, os.path.join(HERE, "xlsx_ref.py"),
                  args.file, str(args.max_rows or 5000)], env=env)

    go, py = json.loads(go_out), json.loads(py_out)

    print(f"\n{'='*66}\n差分结果（{os.path.basename(args.file)}）\n{'='*66}")

    meta_keys = ["sheet", "head_idx", "prompt_col", "scene_col", "target_col",
                 "name_col", "attach_col", "skill_col", "total_rows", "skipped"]
    meta_bad = 0
    for k in meta_keys:
        g, p = go.get(k), py.get(k)
        ok = g == p
        if not ok:
            meta_bad += 1
        print(f"  [{'OK ' if ok else 'DIFF'}] {k:12} go={g!r:>12}  py={p!r:>12}")

    gh, ph = go.get("header"), py.get("header")
    if gh != ph:
        meta_bad += 1
        print(f"  [DIFF] header\n         go={gh}\n         py={ph}")
    else:
        print(f"  [OK ] header       {gh}")

    gi, pi = go.get("items") or [], py.get("items") or []
    print(f"\n  条目数: go={len(gi)}  py={len(pi)}  {'OK' if len(gi)==len(pi) else 'DIFF'}")

    strict_bad, norm_bad = 0, 0
    field_counts: dict[str, int] = {}
    examples: list[str] = []
    for idx, (g, p) in enumerate(zip(gi, pi)):
        if g != p:
            strict_bad += 1
        if canon(g, args.strict) != canon(p, args.strict):
            norm_bad += 1
            cg, cp = canon(g, args.strict), canon(p, args.strict)
            for k in sorted(set(cg) | set(cp)):
                if cg.get(k) != cp.get(k):
                    field_counts[k] = field_counts.get(k, 0) + 1
                    if len(examples) < 8:
                        examples.append(
                            f"    item[{idx}].{k}:\n      go={g.get(k)!r}\n      py={p.get(k)!r}")

    print(f"\n  严格不一致:   {strict_bad} / {len(gi)}")
    print(f"  语义不一致:   {norm_bad} / {len(gi)}"
          + ("   （--strict 模式）" if args.strict else "   （已归一化顺序）"))
    if field_counts:
        print("\n  按字段统计:")
        for k, c in sorted(field_counts.items(), key=lambda x: -x[1]):
            print(f"    {k:16} {c} 项")
    if examples:
        print("\n  样例:")
        print("\n".join(examples))

    ok = (norm_bad == 0 and meta_bad == 0 and len(gi) == len(pi))
    print(f"\n{'='*66}")
    print("结果: " + ("✅ 语义一致" if ok else "❌ 存在真实差异"))
    if strict_bad and not args.strict:
        print(f"提示: 仍有 {strict_bad} 项仅顺序不同 —— 这是 Python oracle 自身的"
              "不确定性（set 遍历），不是 Go 侧缺陷。")
    print("=" * 66)
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
