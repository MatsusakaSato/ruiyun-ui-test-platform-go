#!/usr/bin/env python3
"""P0 差分验证工装 —— Python 侧参照实现（oracle）。

用法:
    python3 tools/diff/xlsx_ref.py <file> [maxRows]

输出与 Go 侧 cmd/xlsxdiff 完全同形状的规范 JSON，供逐字段 diff。
注意：本脚本刻意 **import 原始 Python 实现**（而非重写一遍），
以保证比对的是真实生产行为。
"""
import json
import os
import sys

# 通过环境变量定位原始 Python 项目，默认指向同级目录
PY_ROOT = os.environ.get(
    "RUIYUN_PY_ROOT",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "..",
                 "ruiyun-ui-test-platform"),
)
sys.path.insert(0, os.path.abspath(PY_ROOT))

from core import xlsx_reader  # noqa: E402


def main() -> int:
    if len(sys.argv) < 2:
        print("用法: xlsx_ref.py <file> [maxRows]", file=sys.stderr)
        return 2
    path = sys.argv[1]
    max_rows = int(sys.argv[2]) if len(sys.argv) > 2 else 5000

    with open(path, "rb") as fh:
        data = fh.read()

    table = xlsx_reader.read_table(os.path.basename(path), data, max_rows=max_rows)
    rows = table["rows"]
    got = xlsx_reader.detect_table(rows)

    out = {
        "sheet": table.get("sheet"),
        "header": got.get("header"),
        "head_idx": got.get("head_idx"),
        "prompt_col": got.get("prompt_col"),
        "scene_col": got.get("scene_col"),
        "target_col": got.get("target_col"),
        "name_col": got.get("name_col"),
        "attach_col": got.get("attachment_col"),
        "skill_col": got.get("skill_col"),
        "total_rows": got.get("total_rows"),
        "items": got.get("items"),
        "skipped": got.get("skipped"),
    }
    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
