#!/usr/bin/env python3
"""P0 差分验证工装 —— 指标层（trajectory + metrics）Python 参照实现。

用法:
    python3 tools/diff/metrics_ref.py <sessionRoot> <rulesJSON> <inputsJSON> <maxIterations>

走与 Go 侧完全相同的链路：
    解析轨迹 → 跑断言 → 构造 CaseResult
    → build_case_detail(逐用例) → build_round_detail / build_metrics(整轮聚合)
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
from core.metrics import build_metrics  # noqa: E402
from core.models import CaseResult  # noqa: E402
from core.trajectory import build_case_detail, build_round_detail  # noqa: E402


def main() -> int:
    if len(sys.argv) < 5:
        print("用法: metrics_ref.py <sessionRoot> <rulesJSON> <inputsJSON> <maxIter>",
              file=sys.stderr)
        return 2
    root, rules_path, inputs_path, max_iter = (
        sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]))

    rules = json.loads(Path(rules_path).read_text(encoding="utf-8"))
    inp = json.loads(Path(inputs_path).read_text(encoding="utf-8"))
    cfg = inp.get("cfg") or {}
    meta_all = inp.get("sessions") or {}
    names = inp.get("order") or sorted(meta_all)

    case_results = []
    parse_errors: dict = {}

    for name in names:
        meta = meta_all.get(name) or {}
        try:
            trace = parse_session(Path(root) / name)
        except Exception as e:  # noqa: BLE001
            parse_errors[name] = f"{type(e).__name__}: {e}"
            continue
        if trace is None:
            parse_errors[name] = "nil trace"
            continue

        findings = run_assertions(trace, rules, max_iter)
        c = CaseResult(
            case_id=meta.get("case_id", ""),
            name=meta.get("name", ""),
            prompt=meta.get("prompt", ""),
            expected_tools=list(meta.get("expected_tools") or []),
            session_id=trace.session_id,
            trace=trace,
            findings=findings,
            ui_ok=bool(meta.get("ui_ok", False)),
            ui_error=meta.get("ui_error", ""),
            attachments=list(meta.get("attachments") or []),
            attach_note=meta.get("attach_note", ""),
            waited_limit=bool(meta.get("waited_limit", False)),
            wait_note=meta.get("wait_note", ""),
            auto_confirms=int(meta.get("auto_confirms") or 0),
            confirm_events=list(meta.get("confirm_events") or []),
            elapsed_s=float(meta.get("elapsed_s") or 0.0),
        )
        case_results.append(c)

    # findings 按 (session_id, step_index) 索引 —— 与 build_round_detail 内部同构
    by_step: dict = {}
    for c in case_results:
        for f in c.findings:
            if f.step_index is not None:
                by_step.setdefault((c.session_id, f.step_index), []).append(f)

    per_case = {}
    for c in case_results:
        per_case[c.case_id] = build_case_detail(c, by_step, cfg)

    round_detail = build_round_detail(
        case_results, metrics=None, repro_rows=None,
        stage_times={}, cfg=cfg)
    round_detail.pop("cases", None)  # 逐用例已单独输出

    md = build_metrics(case_results, cfg, recipes=None, stage_times={})

    # 交叉校验用：每个步骤参数的未截断、键已排序规范形（与 Go 侧同口径）
    args_canon = {}
    for c in case_results:
        m = {}
        if c.trace:
            for tc in c.trace.tool_calls:
                m[str(tc.index)] = json.dumps(tc.arguments, ensure_ascii=False,
                                              sort_keys=True)
        args_canon[c.case_id] = m

    json.dump({
        "args_canon": args_canon,
        "cases": per_case,
        "parse_errors": parse_errors,
        "round_detail": round_detail,
        "metrics": md,
        "case_count": len(case_results),
    }, sys.stdout, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
