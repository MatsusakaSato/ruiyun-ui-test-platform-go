#!/usr/bin/env python3
"""P0 差分验证工装 —— DB 层 Python 侧参照实现（oracle）。

用法: db_ref.py <dbPath>

直接 import 原版 core.testcase_db，跑与 Go 侧 cmd/dbdiff 完全相同的查询电池，
输出同形状 JSON。**只读**：不调用任何写接口。
"""
import json
import os
import sys

PY_ROOT = os.environ.get(
    "RUIYUN_PY_ROOT",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "..",
                 "ruiyun-ui-test-platform"),
)
sys.path.insert(0, os.path.abspath(PY_ROOT))

from core import testcase_db  # noqa: E402

# 必须与 cmd/dbdiff/main.go 的 batteries 逐项一致
BATTERIES = [
    ("default",       dict()),
    ("keyword",       dict(keyword="备课")),
    ("keyword_none",  dict(keyword="zzz-不存在-zzz")),
    ("scene",         dict(scene="备课")),
    ("targets_word",  dict(targets=["word"])),
    ("targets_multi", dict(targets=["word", "ppt"])),
    ("attach_yes",    dict(attachment="yes")),
    ("attach_no",     dict(attachment="no")),
    ("ids",           dict(ids=["CASE-001", "CASE-002"])),
    ("limit_0",       dict(limit=0)),
    ("limit_neg",     dict(limit=-5)),
    ("limit_1",       dict(limit=1)),
    ("limit_50",      dict(limit=50)),
    ("limit_500",     dict(limit=500)),
    ("limit_501_OVER", dict(limit=501)),
    ("limit_1000_OVER", dict(limit=1000)),
    ("offset_neg",    dict(offset=-3, limit=5)),
    ("asc_limit5",    dict(order="asc", limit=5)),
    ("desc_limit5",   dict(order="desc", limit=5)),
    ("paging",        dict(limit=7, offset=7)),
]


def main() -> int:
    if len(sys.argv) < 2:
        print("用法: db_ref.py <dbPath>", file=sys.stderr)
        return 2
    db_path = sys.argv[1]

    out = {}
    try:
        out["count"] = testcase_db.count_cases(db_path)
    except Exception as exc:  # noqa: BLE001
        out["count_error"] = str(exc)

    try:
        out["get_preset_cases"] = testcase_db.get_preset_cases(db_path)
    except Exception as exc:  # noqa: BLE001
        out["get_preset_cases_error"] = str(exc)

    queries = {}
    for name, kw in BATTERIES:
        try:
            queries[name] = testcase_db.query_preset_cases(db_path=db_path, **kw)
        except Exception as exc:  # noqa: BLE001
            queries[name] = {"error": str(exc)}
    out["queries"] = queries

    try:
        out["labels_index"] = testcase_db.get_preset_labels_index(db_path)
    except Exception as exc:  # noqa: BLE001
        out["labels_index_error"] = str(exc)

    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
