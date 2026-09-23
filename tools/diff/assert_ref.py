#!/usr/bin/env python3
"""P0 差分验证工装 —— 断言层（assertor）Python 参照实现。

用法:
    python3 tools/diff/assert_ref.py <sessionRoot> <rulesJSONPath> <maxIterations>

直接 import 生产实现 core.assertor.run_assertions，保证比对的是真实行为。
rules 从外部 JSON 读入，与 Go 侧共用同一份配置。
"""
from __future__ import annotations

import json
import os
import sys
from pathlib import Path

PY_ROOT = os.environ.get("RUIYUN_PY_ROOT")
if not PY_ROOT:
    PY_ROOT = str(Path(__file__).resolve().parents[2].parent / "ruiyun-ui-test-platform")
sys.path.insert(0, PY_ROOT)

from core.assertor import run_assertions  # noqa: E402
from core.log_parser import parse_session  # noqa: E402


def main() -> int:
    if len(sys.argv) < 4:
        print("用法: assert_ref.py <sessionRoot> <rulesJSONPath> <maxIterations>",
              file=sys.stderr)
        return 2
    root, cfg_path, max_iter = sys.argv[1], sys.argv[2], int(sys.argv[3])
    cfg = json.loads(Path(cfg_path).read_text(encoding="utf-8"))

    dirs = sorted(
        d for d in os.listdir(root)
        if d.startswith("sess_")
        and os.path.isfile(os.path.join(root, d, "session.messages.json"))
    )

    out: dict = {}
    by_rule: dict = {}
    by_sev: dict = {}
    total = 0

    for name in dirs:
        try:
            t = parse_session(Path(root) / name)
        except Exception as e:  # noqa: BLE001
            out[name] = {"parse_error": f"{type(e).__name__}: {e}"}
            continue
        if t is None:
            out[name] = {"parse_error": ""}
            continue

        findings = run_assertions(t, cfg, max_iter)
        arr = []
        for f in findings:
            arr.append({
                "rule": f.rule,
                "severity": f.severity,
                "session_id": f.session_id,
                "detail": f.detail,
                "tool": f.tool,
                "step_index": f.step_index,
                "evidence": f.evidence,
            })
            by_rule[f.rule] = by_rule.get(f.rule, 0) + 1
            by_sev[f.severity] = by_sev.get(f.severity, 0) + 1
            total += 1
        out[name] = {"count": len(arr), "findings": arr}

    json.dump({
        "root": root,
        "max_iter": max_iter,
        "total": total,
        "by_rule": by_rule,
        "by_severity": by_sev,
        "sessions": out,
    }, sys.stdout, ensure_ascii=False, indent=1)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
