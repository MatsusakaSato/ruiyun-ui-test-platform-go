#!/usr/bin/env bash
# ============================================================
# 睿云智能工作台 · 开发(dev)环境启动脚本 —— macOS 版
# 由 Windows PowerShell 片段转换而来，逐行等价关系：
#   $env:FOO = "bar"                   →  export FOO="bar"
#   & "C:\Program Files\...\x.exe"     →  exec "/Applications/x.app/Contents/MacOS/x"
#
# 用法:
#   ./launch_app_dev.sh                 # 前台启动，Ctrl-C 退出（手动调试用）
#   ./launch_app_dev.sh --detach        # 后台脱离启动，命令结束/关终端后仍存活（自动化用）
#   ./launch_app_dev.sh --stop          # 结束应用（杀到干净为止）
#   ./launch_app_dev.sh --dry-run       # 只打印环境变量与将执行的命令，不启动
#   DEBUG_PORT=9333 ./launch_app_dev.sh --detach
#
# 注：两种启动模式都会先清理已有实例 —— Electron 是单实例应用，
#     旧实例还活着时新进程的 argv 会被静默丢弃，调试端口永远开不起来。
# ============================================================
set -euo pipefail

APP_BIN="/Applications/睿云智能工作台.app/Contents/MacOS/睿云智能工作台"
# 用于 pkill/pgrep 的匹配串：用完整包路径，能同时命中主进程、Helper 与内置后端服务，
# 又不会误杀命令行里恰好提到应用名的其它进程
APP_KILL_PATTERN="/Applications/睿云智能工作台.app/"
DEBUG_PORT="${DEBUG_PORT:-9222}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_FILE="${LOG_FILE:-${SCRIPT_DIR}/logs/srtclaw_app.log}"
LAUNCH_TIMEOUT_S="${LAUNCH_TIMEOUT_S:-60}"

# ---------- 1. 开发环境变量 ----------
export SRTCLAW_APP_ENV="dev"
export SRTCLAW_LOGIN_URL="https://ruiyun-dev.3ren.cn/wisdom/commonLogin?"
export VITE_SRTCLAW_LOGIN_URL="https://ruiyun-dev.3ren.cn/wisdom/commonLogin?"
export SRTCLAW_MANAGE_SERVICE_BASE_URL="https://api-dev.3ren.cn/claw-manage-service"
export SRTCLAW_INSTITUTION_API_BASE_URL="https://ruiyun-api-dev.3ren.cn"
export SRTCLAW_TEACHING_API_BASE_URL="https://api-dev.3ren.cn"
export JIUWENCLAW_UPDATE_API_URL="https://appadmin-test.yanxiu.com/app/log/uploadDeviceLog/release.do"

# ---------- 2. 环境清理（macOS 特有，不能省）----------
# 宿主/IDE 注入的 ELECTRON_RUN_AS_NODE=1 会让 Electron 退化成纯 Node 进程，
# 表现为应用"启动"了但 --remote-debugging-port 永远不监听（bad option）。
unset ELECTRON_RUN_AS_NODE

# Chromium 启动开关：受限环境(沙箱/CI)下 GPU 进程起不来会直接 FATAL 自尽，
# 桌面正常环境可去掉后两行。
CHROMIUM_FLAGS=(
  "--remote-debugging-port=${DEBUG_PORT}"
  "--no-sandbox"
  "--disable-gpu"
  "--disable-gpu-compositing"
)

MODE="${1:-run}"

# ---------- 3. 干跑模式 ----------
if [[ "${MODE}" == "--dry-run" ]]; then
  echo "APP_BIN=${APP_BIN}"
  echo "---- 环境变量 ----"
  for v in SRTCLAW_APP_ENV SRTCLAW_LOGIN_URL VITE_SRTCLAW_LOGIN_URL \
           SRTCLAW_MANAGE_SERVICE_BASE_URL SRTCLAW_INSTITUTION_API_BASE_URL \
           SRTCLAW_TEACHING_API_BASE_URL JIUWENCLAW_UPDATE_API_URL; do
    printf '  %-36s = %s\n' "$v" "${!v}"
  done
  echo "---- 将执行 ----"
  echo "  ${APP_BIN} ${CHROMIUM_FLAGS[*]}"
  exit 0
fi

# ---------- 4. 结束应用 ----------
# 必须"杀到干净"再启动：Electron 是单实例应用，只要还有旧实例存活，
# 新进程会把 argv 转交给旧实例然后自己退出 —— 表现是日志里明明有
# "DevTools listening"，但端口连不上、旧实例却没带调试参数。
# 注意匹配串用完整路径而不是应用名，避免误杀调用方自己的 shell。
stop_app() {
  local i
  if ! pgrep -f "${APP_KILL_PATTERN}" >/dev/null 2>&1; then
    echo "启动前检查：无残留实例"
    return 0
  fi
  echo "检测到已有实例，先清理（Electron 单实例会把新进程的 argv 转交过去然后自尽，"
  echo "  表现为 DevToolsActivePort 写了端口号，但那个端口从未被监听）"
  for i in 1 2 3 4 5; do
    pkill -f "${APP_KILL_PATTERN}" 2>/dev/null || true
    sleep 1
    if ! pgrep -f "${APP_KILL_PATTERN}" >/dev/null 2>&1; then
      echo "  已清理干净（第 ${i} 轮）"
      return 0
    fi
  done
  echo "  警告：5 轮后仍有实例存活，启动可能被单实例机制吞掉参数" >&2
  return 1
}

if [[ "${MODE}" == "--stop" ]]; then
  stop_app
  exit $?
fi

if [[ ! -x "${APP_BIN}" ]]; then
  echo "找不到可执行文件: ${APP_BIN}" >&2
  exit 1
fi

# 单实例保护：前台/后台两种启动模式都要先清场，
# 否则新进程会被已在运行的旧实例吞掉参数（静默失败）
stop_app || true

# ---------- 5. 后台脱离启动 ----------
# 为什么不直接 `&`：调用方的进程树可能在命令结束时被整体回收。
# start_new_session=True 让应用进入新会话，彻底脱离调用方进程组。
# 为什么不用 `open -a`：open 走 LaunchServices，环境变量传不进去；
# 它的 --env/--args 实测对本应用无效（调试端口不会开）。见 SKILL.md「启动这一步」。
if [[ "${MODE}" == "--detach" ]]; then
  PYTHON="$(command -v python3 || echo /usr/bin/python3)"
  "${PYTHON}" - "${APP_BIN}" "${DEBUG_PORT}" "${LOG_FILE}" <<'PYEOF'
import os, subprocess, sys
binary, port, logfile = sys.argv[1], sys.argv[2], sys.argv[3]
env = dict(os.environ)
env.pop("ELECTRON_RUN_AS_NODE", None)   # 双保险：见 SKILL.md 坑 1
with open(logfile, "w") as log:
    p = subprocess.Popen(
        [binary, f"--remote-debugging-port={port}",
         "--no-sandbox", "--disable-gpu", "--disable-gpu-compositing"],
        env=env, start_new_session=True,
        stdout=log, stderr=subprocess.STDOUT, cwd=os.environ.get("TMPDIR", "/tmp"),
    )
print(f"[detach] 已启动 pid={p.pid}  日志={logfile}")
PYEOF

  # 自检：端口起来才算成功（端口就绪 ≠ 界面可用，但端口不通必然不可用）
  TIMEOUT="${LAUNCH_TIMEOUT_S:-60}"
  for i in $(seq 1 "${TIMEOUT}"); do
    if curl -s --noproxy '*' --max-time 2 \
         "http://127.0.0.1:${DEBUG_PORT}/json/version" >/dev/null 2>&1; then
      echo "CDP 就绪: http://127.0.0.1:${DEBUG_PORT}（等待 ${i}s）"
      exit 0
    fi
    sleep 1
  done
  echo "端口 ${DEBUG_PORT} 在 ${TIMEOUT}s 内未就绪，检查 ${LOG_FILE}" >&2
  exit 1
fi

# ---------- 6. 前台启动 ----------
exec "${APP_BIN}" "${CHROMIUM_FLAGS[@]}"
