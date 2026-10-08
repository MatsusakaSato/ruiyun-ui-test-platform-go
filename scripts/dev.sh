#!/usr/bin/env bash
# ==============================================================================
# 开发模式：热更新
#
#   make dev
#
# 两类改动分开处理：
#   1) 前端 web/*（HTML / CSS / JS）
#      服务直接读磁盘（RUYIYUN_DEV=1 → internal/server/dev.go），
#      浏览器通过 SSE /api/dev/events 收到通知后自动刷新：
#      纯 CSS 改动只换样式表不刷新页面，其余改动整页重载。
#      → 改前端**不需要重新编译**。
#   2) 后端 *.go / go.mod / go.sum
#      每 1 秒比对一次指纹，变了就重新编译并重启服务（旧进程继续服务直到新的编好）。
#
# Ctrl-C 退出时会连同服务进程一起收掉。
# ==============================================================================
set -uo pipefail

cd "$(dirname "$0")/.."
ROOT="$PWD"
BIN="$ROOT/bin/ruiyun"
PORT="${PORT:-8765}"

export RUYIYUN_DEV=1
export RUYIYUN_WEB_DIR="$ROOT/internal/server/web"

# 指纹：所有影响编译结果的文件内容
snapshot() {
  {
    find cmd internal -name '*.go' -type f 2>/dev/null | sort | while read -r f; do cat "$f"; done
    cat go.mod 2>/dev/null
    cat go.sum 2>/dev/null
  } | shasum -a 256 2>/dev/null | awk '{print $1}'
}

cleanup() {
  if [ -n "${SRV_PID:-}" ] && kill -0 "${SRV_PID}" 2>/dev/null; then
    kill "${SRV_PID}" 2>/dev/null
    wait "${SRV_PID}" 2>/dev/null
  fi
  # 兜底清掉可能残留的同名进程（kill $SRV_PID 有时杀不到真正监听的子进程）
  pkill -f "$BIN serve" 2>/dev/null
  echo ""
  echo "[dev] 已退出，服务已停止"
}
trap cleanup EXIT INT TERM

# 启动前先清掉可能占着端口的旧实例
pkill -f "$BIN serve" 2>/dev/null
sleep 0.3

build() {
  echo "[dev] 编译中…"
  if ./scripts/build.sh build >/tmp/ruiyun_dev_build.log 2>&1; then
    echo "[dev] 编译完成"
    return 0
  fi
  echo "[dev] 编译失败："
  tail -5 /tmp/ruiyun_dev_build.log | sed 's/^/       /'
  return 1
}

start() {
  "$BIN" serve &
  SRV_PID=$!
  # 变量名一律用 ${} 包起来：后面紧跟全角括号时，bash 会把 "SRV_PID）"
  # 整个当成一个变量名，进而报 unbound variable（set -u 下直接中断）。
  echo "[dev] 服务已启动 http://127.0.0.1:${PORT} (pid ${SRV_PID})"
}

if ! build; then
  echo "[dev] 首次编译失败，请先修复编译错误"
  exit 1
fi
start

LAST="$(snapshot)"
echo ""
echo "[dev] 监听中：web/ 改动即时热更新，Go 改动自动重启（Ctrl-C 退出）"
echo ""

while true; do
  sleep 1
  CUR="$(snapshot)"
  if [ "$CUR" = "$LAST" ]; then
    continue
  fi
  echo "[dev] 检测到 Go 代码改动 → 重新编译…"
  if build; then
    if [ -n "${SRV_PID:-}" ] && kill -0 "${SRV_PID}" 2>/dev/null; then
      kill "${SRV_PID}" 2>/dev/null
      wait "${SRV_PID}" 2>/dev/null
    fi
    pkill -f "$BIN serve" 2>/dev/null
    sleep 0.3
    start
  else
    echo "[dev] 编译未通过，服务保持运行，修好后再自动重试"
  fi
  # 无论成功与否都更新指纹：避免同一处错误每秒重复刷屏，
  # 后续再次改动会重新触发。
  LAST="$CUR"
done
