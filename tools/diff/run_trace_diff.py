#!/usr/bin/env python3
"""P0 差分验证器 —— 分析层（logparser / ExecutionTrace）。

用法:
    python3 tools/diff/run_trace_diff.py [--sessions DIR] [--limit N]

默认对 `~/.srtclaw/workspace/session` 下的**全部真实会话**做验证
（本机 219 份）。

比较口径:
    - 25 个顶层标量字段；
    - 每次工具调用的 17 个字段，其中正文/结果用 **SHA-256 哈希**比较
      （长度相等不代表内容相等），长度用 **码点** 计（暴露 Python len(str)
      与 Go len(string) 的差异）；
    - signature 指纹（会参与 tool_arg_chars 与 token 估算）。

退出码 0 = 完全等价，1 = 有真实差异。
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

SCALARS = [
    "session_id", "title", "status", "mode", "created_at", "updated_at",
    "user_prompt", "final_answer", "reasoning_chars", "is_streaming",
    "completed_at", "raw_message_count", "source_file", "error", "user_at",
    "assistant_at", "first_response_s", "generation_s", "session_s",
    "turn_count", "tool_names", "orphan_ids", "associated_tool_call_ids",
    "thinking_step_count", "message_span_count",
]
CALL_FIELDS = [
    "index", "tool_call_id", "name", "body_from", "event_type", "failed",
    "fail_reason", "truncated", "empty_required_arg", "duration_ms",
    "body_len", "body_sha", "result_len", "result_sha", "sig_sha",
    "result_is_nil", "result_obj_nil",
]


def norm(v):
    """JSON 数值 2.0 与 2 视为相等（Python/Go 的序列化差异，非语义差异）。"""
    if isinstance(v, float) and v == int(v):
        return int(v)
    return v


def run(cmd, env=None):
    p = subprocess.run(cmd, capture_output=True, text=True, env=env)
    if p.returncode != 0:
        print(f"[!] 命令失败: {' '.join(str(c) for c in cmd)}", file=sys.stderr)
        print(p.stderr[:3000], file=sys.stderr)
        sys.exit(2)
    return p.stdout


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--sessions", default=DEFAULT_SESSIONS)
    ap.add_argument("--py-root", default=os.environ.get("RUIYUN_PY_ROOT", DEFAULT_PY_ROOT))
    ap.add_argument("--limit", type=int, default=0)
    args = ap.parse_args()

    if not os.path.isdir(args.sessions):
        print(f"[!] 会话目录不存在: {args.sessions}", file=sys.stderr)
        return 2

    tmp = tempfile.mkdtemp(prefix="ruiyun_tracediff_")
    go_bin = os.path.join(tmp, "tracediff")
    print("[*] 构建 Go 侧 ...")
    subprocess.run(["go", "build", "-o", go_bin, "./cmd/tracediff"],
                   cwd=GO_ROOT, check=True)

    print("[*] Go 侧解析 ...")
    go = json.loads(run([go_bin, args.sessions] + ([str(args.limit)] if args.limit else [])))["sessions"]

    venv_py = os.path.join(args.py_root, ".venv", "bin", "python3")
    py_bin = venv_py if os.path.exists(venv_py) else sys.executable
    print(f"[*] Python 侧解析 ({py_bin}) ...")
    env = dict(os.environ, RUIYUN_PY_ROOT=args.py_root)
    ref = [py_bin, os.path.join(HERE, "trace_ref.py"), args.sessions]
    if args.limit:
        ref.append(str(args.limit))
    py = json.loads(run(ref, env=env))["sessions"]

    print(f"\n{'='*70}\n分析层差分结果\n{'='*70}")
    print(f"  会话: go={len(go)} py={len(py)}")

    fails = 0
    scalar_fail: dict[str, list[str]] = {}
    call_fail: dict[str, list[str]] = {}
    compared_calls = 0

    for name in sorted(py):
        g, p = go.get(name), py.get(name)
        if g is None:
            scalar_fail.setdefault("__missing__", []).append(name)
            continue
        for f in SCALARS:
            if norm(g.get(f)) != norm(p.get(f)):
                scalar_fail.setdefault(f, []).append(name)
        gc, pc = g.get("tool_calls", []), p.get("tool_calls", [])
        if len(gc) != len(pc):
            call_fail.setdefault("__count__", []).append(
                f"{name}(go={len(gc)},py={len(pc)})")
            continue
        compared_calls += len(pc)
        for i, (a, b) in enumerate(zip(gc, pc)):
            for f in CALL_FIELDS:
                if norm(a.get(f)) != norm(b.get(f)):
                    call_fail.setdefault(f, []).append(f"{name}#{i}")

    print(f"  比较工具调用: {compared_calls} 次")
    print(f"\n  顶层字段（{len(SCALARS)} 个）:")
    if not scalar_fail:
        print("    [OK  ] 全部一致")
    for f, names in sorted(scalar_fail.items(), key=lambda x: -len(x[1])):
        fails += 1
        print(f"    [DIFF] {f:26} {len(names):>4} 个会话, 例: {names[:3]}")
    print(f"\n  工具调用字段（{len(CALL_FIELDS)} 个）:")
    if not call_fail:
        print("    [OK  ] 全部一致")
    for f, names in sorted(call_fail.items(), key=lambda x: -len(x[1])):
        fails += 1
        print(f"    [DIFF] {f:26} {len(names):>4} 处, 例: {names[:3]}")

    print(f"\n{'='*70}")
    print("结果: " + ("✅ 完全等价" if fails == 0 else f"❌ {fails} 类真实差异"))
    print("=" * 70)
    return 0 if fails == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
