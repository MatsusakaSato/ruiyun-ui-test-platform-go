"""eval 层差分验证 —— Python 参照实现。

本脚本**直接 import 生产实现**（core.format_check / core.safety_scan / core.case_intent），
比对的是真实生产行为，而不是"我理解的 Python 行为"。

用法: eval_ref.py <inputsJSON>

inputsJSON 由 run_eval_diff.py 生成，两侧读同一份，保证输入逐字节一致：

    {
      "cases": {
        "<case_id>": {
            "labels": {...},              # 传给 format_check.evaluate 的 labels
            "artifact_kinds": [...],      # 产物类型集合
            "requirements": [...] | null, # 抽取出的显式格式要求
            "haystack": "...",            # 产物正文
            "weights": {"type":..,"coverage":..} | null,
            "kinds": [...] | null,        # kindsOverride
            "final_answer": "...",        # 安全扫描
            "artifact_texts": [[name, text], ...],
            "tool_calls": [{name, arguments, result, error, success}, ...],
            "prompt": "..."               # 意图推断
        }
      },
      "order": ["<case_id>", ...]
    }

输出（stdout，UTF-8 JSON）：
    {"formatcheck": {...}, "safetyscan": {...}, "intent": {...}, "errors": {...}}
"""
from __future__ import annotations

import json
import sys
from pathlib import Path


def _find_project_root() -> Path:
    """定位 Python 原版项目根（含 core/ 的那一层）。"""
    for base in Path(__file__).resolve().parents:
        if (base / "core" / "format_check.py").is_file():
            return base
    # 回退：Go 项目的同级目录
    guess = Path(__file__).resolve().parents[2].parent / "ruiyun-ui-test-platform"
    if (guess / "core" / "format_check.py").is_file():
        return guess
    raise SystemExit("找不到 Python 原版项目根（core/format_check.py）")


sys.path.insert(0, str(_find_project_root()))

from core.format_check import evaluate as fc_evaluate          # noqa: E402
from core.safety_scan import (                                  # noqa: E402
    build_sources, scan, redlines, exempt_hits, has_redline,
)
from core.case_intent import infer_target_kinds, infer_scene    # noqa: E402
from core.models import ToolCall                                # noqa: E402


def _to_tool_calls(raw: list) -> list:
    """把 inputs.json 里的纯 dict 还原成真实 ToolCall 对象。

    build_sources 用的是 getattr(tc, "name"/"arguments")，
    传 dict 会静默取到空串 —— 那样两侧就都在扫空文本，差分毫无意义。
    """
    out = []
    for i, d in enumerate(raw or []):
        if not isinstance(d, dict):
            continue
        out.append(ToolCall(
            index=i,
            tool_call_id="tc-%d" % i,
            name=d.get("name") or "",
            arguments=d.get("arguments"),
            raw_result=None,
        ))
    return out


def hit_to_dict(h) -> dict:
    """把 Hit 归一化成纯 dict（Python 侧是 NamedTuple/dataclass）。"""
    if isinstance(h, dict):
        return {
            "rule": h.get("rule"), "label": h.get("label"), "source": h.get("source"),
            "snippet": h.get("snippet"), "exempt": bool(h.get("exempt")),
            "executable": bool(h.get("executable")),
        }
    return {
        "rule": getattr(h, "rule", None),
        "label": getattr(h, "label", None),
        "source": getattr(h, "source", None),
        "snippet": getattr(h, "snippet", None),
        "exempt": bool(getattr(h, "exempt", False)),
        "executable": bool(getattr(h, "executable", False)),
    }


def run_formatcheck(c: dict) -> dict:
    kinds = c.get("kinds")
    return fc_evaluate(
        c.get("labels") or {},
        c.get("artifact_kinds") or [],
        c.get("requirements"),
        c.get("haystack") or "",
        c.get("weights"),
        kinds,
    )


def run_safetyscan(c: dict) -> dict:
    sources = build_sources(
        c.get("final_answer") or "",
        [tuple(x) for x in (c.get("artifact_texts") or [])],
        _to_tool_calls(c.get("tool_calls")),
    )
    hits = scan(sources)
    return {
        # 按**插入序**输出：Go 侧已改用有序的 BuildSourcesOrdered，
        # 顺序本身有语义（决定 hits 顺序与 _MAX_HITS 截断），必须比。
        "sources": [[k, v] for k, v in sources.items()],
        "hits": [hit_to_dict(h) for h in hits],
        "redlines": [hit_to_dict(h) for h in redlines(hits)],
        "exempt": [hit_to_dict(h) for h in exempt_hits(hits)],
        "has_redline": has_redline(hits),
    }


def run_intent(c: dict) -> dict:
    prompt = c.get("prompt") or ""
    kinds, kind_why = infer_target_kinds(prompt)
    scene, scene_why = infer_scene(prompt)
    return {
        "kinds": sorted(kinds) if not isinstance(kinds, (list, tuple)) else list(kinds),
        "kind_why": list(kind_why),
        "scene": scene,
        "scene_why": list(scene_why),
    }


def main() -> int:
    if len(sys.argv) < 2:
        print("用法: eval_ref.py <inputsJSON>", file=sys.stderr)
        return 2
    with open(sys.argv[1], encoding="utf-8") as fh:
        payload = json.load(fh)

    cases = payload.get("cases") or {}
    order = payload.get("order") or sorted(cases)

    out = {"formatcheck": {}, "safetyscan": {}, "intent": {}, "errors": {}}
    for cid in order:
        c = cases.get(cid)
        if c is None:
            continue
        for layer, fn in (("formatcheck", run_formatcheck),
                          ("safetyscan", run_safetyscan),
                          ("intent", run_intent)):
            try:
                out[layer][cid] = fn(c)
            except Exception as exc:  # noqa: BLE001
                out["errors"][f"{layer}:{cid}"] = f"{type(exc).__name__}: {exc}"

    json.dump(out, sys.stdout, ensure_ascii=False, sort_keys=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
