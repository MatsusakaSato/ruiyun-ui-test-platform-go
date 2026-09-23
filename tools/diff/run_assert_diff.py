#!/usr/bin/env python3
"""P0 差分验证器 —— 断言层（assertor）。

用法:
    python3 tools/diff/run_assert_diff.py [--sessions DIR] [--config PATH]

默认对 `~/.srtclaw/workspace/session` 下的全部真实会话做验证（本机 219 份），
配置取 `~/.ruiyun-autotest/config.yaml`，缺失时回退到项目内 config.template.yaml。

关键设计：**两侧共用同一份 rules**（先落盘成临时 JSON 再各自读入），
避免"因为配置不同所以结果不同"的假阳性。

比较口径：逐会话按**顺序**比对 Finding 的 7 个字段
（rule / severity / session_id / detail / tool / step_index / evidence）。
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
DEFAULT_CONFIG = os.path.expanduser("~/.ruiyun-autotest/config.yaml")

FIELDS = ["rule", "severity", "session_id", "detail", "tool", "step_index", "evidence"]


def canon_json(s):
    """把形如 JSON 对象的 evidence 归一化成「键排序后的紧凑 JSON」。

    只用于识别一类**已知的显示层差异**：Python 的 check_empty_args 用的是
    json.dumps(args, ensure_ascii=False)（不排序 → 保留源文件键序），
    而 Go 的 map 不保留键序。若归一化后完全相等，即可证明「唯一差异就是键序」，
    而不是内容有别。除此之外的任何差异都仍然算真实差异。
    """
    if not isinstance(s, str):
        return None
    try:
        return json.dumps(json.loads(s), sort_keys=True, ensure_ascii=False,
                          separators=(",", ":"))
    except Exception:  # noqa: BLE001
        return None


def field_equiv(field: str, a, b) -> bool:
    if a == b:
        return True
    if field == "evidence":
        ca, cb = canon_json(a), canon_json(b)
        return ca is not None and ca == cb
    return False


def load_config(path: str) -> tuple[dict, int]:
    """复刻 run_pipeline.read_max_iterations：从 paths.agent_config 指向的
    agent 配置里读 react.max_iterations，取不到就是 0。"""
    try:
        import yaml
    except ImportError:
        print("[!] 需要 PyYAML；请用项目 .venv 的 python 运行本脚本", file=sys.stderr)
        sys.exit(2)

    with open(path, encoding="utf-8") as fh:
        cfg = yaml.safe_load(fh) or {}
    rules = cfg.get("rules") or {}

    max_iter = 0
    agent_cfg = ((cfg.get("paths") or {}).get("agent_config") or "")
    if agent_cfg and os.path.isfile(agent_cfg):
        try:
            with open(agent_cfg, encoding="utf-8") as fh:
                data = yaml.safe_load(fh) or {}
            max_iter = int((data.get("react") or {}).get("max_iterations") or 0)
        except Exception:  # noqa: BLE001
            max_iter = 0
    return rules, max_iter


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
    ap.add_argument("--config", default=DEFAULT_CONFIG)
    ap.add_argument("--py-root", default=os.environ.get("RUIYUN_PY_ROOT", DEFAULT_PY_ROOT))
    args = ap.parse_args()

    if not os.path.isdir(args.sessions):
        print(f"[!] 会话目录不存在: {args.sessions}", file=sys.stderr)
        return 2
    if not os.path.isfile(args.config):
        fallback = os.path.join(GO_ROOT, "config.template.yaml")
        print(f"[*] 未找到 {args.config}，回退到 {fallback}")
        args.config = fallback

    rules, max_iter = load_config(args.config)
    print(f"[*] 配置: {args.config}")
    print(f"[*] rules 键数={len(rules)}  max_iterations={max_iter}")

    tmp = tempfile.mkdtemp(prefix="ruiyun_assertdiff_")
    rules_path = os.path.join(tmp, "rules.json")
    with open(rules_path, "w", encoding="utf-8") as fh:
        json.dump(rules, fh, ensure_ascii=False)

    go_bin = os.path.join(tmp, "assertdiff")
    print("[*] 构建 Go 侧 ...")
    subprocess.run(["go", "build", "-o", go_bin, "./cmd/assertdiff"],
                   cwd=GO_ROOT, check=True)

    print("[*] Go 侧断言 ...")
    go = json.loads(run([go_bin, args.sessions, rules_path, str(max_iter)]))

    venv_py = os.path.join(args.py_root, ".venv", "bin", "python3")
    py_bin = venv_py if os.path.exists(venv_py) else sys.executable
    print(f"[*] Python 侧断言 ({py_bin}) ...")
    env = dict(os.environ, RUIYUN_PY_ROOT=args.py_root)
    py = json.loads(run([py_bin, os.path.join(HERE, "assert_ref.py"),
                         args.sessions, rules_path, str(max_iter)], env=env))

    print(f"\n{'='*70}\n断言层差分结果\n{'='*70}")
    print(f"  Finding 总数: go={go['total']} py={py['total']}")
    print(f"  规则分布  go={dict(sorted(go['by_rule'].items()))}")
    print(f"           py={dict(sorted(py['by_rule'].items()))}")

    fails = 0
    if go["total"] != py["total"]:
        fails += 1
        print("  [DIFF] Finding 总数不一致")

    gs, ps = go["sessions"], py["sessions"]
    if set(gs) != set(ps):
        fails += 1
        print(f"  [DIFF] 会话键集不一致: go-only={list(set(gs)-set(ps))[:5]} "
              f"py-only={list(set(ps)-set(gs))[:5]}")

    mismatched: list[str] = []
    cosmetic: list[str] = []
    field_fail: dict[str, int] = {}
    for name in sorted(set(gs) & set(ps)):
        gf, pf = gs[name], ps[name]
        if "parse_error" in gf or "parse_error" in pf:
            if gf != pf:
                mismatched.append(name)
            continue
        gfl, pfl = gf.get("findings", []), pf.get("findings", [])
        if len(gfl) != len(pfl):
            mismatched.append(name)
            field_fail["__count__"] = field_fail.get("__count__", 0) + 1
            continue
        bad = False
        only_keyorder = True
        for a, b in zip(gfl, pfl):
            for f in FIELDS:
                if a.get(f) != b.get(f):
                    if field_equiv(f, a.get(f), b.get(f)):
                        # 唯一差异是 JSON 键序
                        pass
                    else:
                        only_keyorder = False
                        field_fail[f] = field_fail.get(f, 0) + 1
                    bad = True
        if bad:
            (cosmetic if only_keyorder else mismatched).append(name)

    print(f"\n  逐会话比对（{len(set(gs) & set(ps))} 个会话）:")
    if not mismatched:
        print("    [OK  ] 无真实差异")
    else:
        fails += 1
        print(f"    [DIFF] {len(mismatched)} 个会话不一致，例: {mismatched[:5]}")
        for f, n in sorted(field_fail.items(), key=lambda x: -x[1]):
            print(f"          字段 {f}: {n} 处")
    if cosmetic:
        print(f"    [NOTE] {len(cosmetic)} 个会话仅有 evidence 的 JSON 键序不同 "
              f"（Go map 不保留键序；已归一化后证明内容完全一致）")
        print(f"           例: {cosmetic[:3]}")

    print(f"\n{'='*70}")
    print("结果: " + ("✅ 完全等价" if fails == 0 else f"❌ {fails} 类真实差异"))
    if not fails and cosmetic:
        print("      （另有 evidence 键序差异，属已知显示层差异）")
    print("=" * 70)
    return 0 if fails == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
