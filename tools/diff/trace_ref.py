#!/usr/bin/env python3
"""P0 差分验证工装 —— 分析层（logparser）Python 参照实现。

用法:
    python3 tools/diff/trace_ref.py <sessionRoot> [limit]

直接 import 生产实现 core.log_parser / core.models，保证比对的是真实行为。
输出与 Go 侧 cmd/tracediff 完全同构的规范 JSON。
"""
from __future__ import annotations

import hashlib
import json
import os
import sys
from pathlib import Path

PY_ROOT = os.environ.get("RUIYUN_PY_ROOT")
if not PY_ROOT:
    PY_ROOT = str(Path(__file__).resolve().parents[2].parent / "ruiyun-ui-test-platform")
sys.path.insert(0, PY_ROOT)

from core.log_parser import parse_session  # noqa: E402


def sha16(s) -> str:
    if not s:
        return ""
    return hashlib.sha256(s.encode("utf-8")).hexdigest()[:16]


def dump_call(tc) -> dict:
    raw = tc.raw_result if isinstance(tc.raw_result, str) else ""
    return {
        "index": tc.index,
        "tool_call_id": tc.tool_call_id,
        "name": tc.name,
        "body_from": tc.body_from,
        "event_type": tc.event_type,
        "failed": bool(tc.failed),
        "fail_reason": tc.fail_reason,
        "truncated": bool(tc.truncated),
        "empty_required_arg": list(tc.empty_required_arg or []),
        "duration_ms": tc.duration_ms,
        "body_len": len(tc.body or ""),
        "body_sha": sha16(tc.body or ""),
        "result_len": len(raw),
        "result_sha": sha16(raw),
        "sig_sha": sha16(tc.signature()),
        "result_is_nil": tc.raw_result is None,
        "result_obj_nil": tc.result_obj is None,
    }


def dump_trace(t) -> dict:
    return {
        "session_id": t.session_id,
        "title": t.title,
        "status": t.status,
        "mode": t.mode,
        "created_at": t.created_at,
        "updated_at": t.updated_at,
        "user_prompt": t.user_prompt,
        "final_answer": t.final_answer,
        "reasoning_chars": t.reasoning_chars,
        "is_streaming": bool(t.is_streaming),
        "completed_at": t.completed_at,
        "raw_message_count": t.raw_message_count,
        "source_file": t.source_file,
        "error": t.error,
        "user_at": getattr(t, "user_at", ""),
        "assistant_at": getattr(t, "assistant_at", ""),
        "first_response_s": getattr(t, "first_response_s", 0.0),
        "generation_s": getattr(t, "generation_s", 0.0),
        "session_s": getattr(t, "session_s", 0.0),
        "turn_count": getattr(t, "turn_count", 0),
        "tool_names": [tc.name for tc in t.tool_calls],
        "orphan_ids": t.orphan_ids,  # @property，不是方法
        "associated_tool_call_ids": list(t.associated_tool_call_ids or []),
        "thinking_step_count": len(t.thinking_steps or []),
        "message_span_count": len(t.message_spans or []),
        "tool_calls": [dump_call(tc) for tc in t.tool_calls],
    }


def main() -> int:
    if len(sys.argv) < 2:
        print("用法: trace_ref.py <sessionRoot> [limit]", file=sys.stderr)
        return 2
    root = sys.argv[1]
    limit = int(sys.argv[2]) if len(sys.argv) > 2 else 0

    dirs = sorted(
        d for d in os.listdir(root)
        if d.startswith("sess_")
        and os.path.isfile(os.path.join(root, d, "session.messages.json"))
    )
    if limit > 0:
        dirs = dirs[:limit]

    out = {}
    for name in dirs:
        try:
            t = parse_session(Path(root) / name)
        except Exception as e:  # noqa: BLE001
            out[name] = {"parse_error": f"{type(e).__name__}: {e}"}
            continue
        if t is None:
            out[name] = {"parse_error": ""}
            continue
        out[name] = dump_trace(t)

    json.dump({"root": root, "sessions": out}, sys.stdout,
              ensure_ascii=False, indent=1)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
