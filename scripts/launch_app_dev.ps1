param (
    [string]$Mode = "run"
)

# 被测应用可执行文件：Windows 默认装在 C:\Program Files\srtclaw\睿云智能工作台.exe
# （用 %ProgramFiles% 而非写死盘符，系统盘/语言不同的机器上它才是权威值）。
# 可用环境变量 APP_BIN 覆盖；该默认路径不存在时退回旧的每用户安装位置，
# 保证老环境不被写死的默认值卡死。
$PF = if ($env:ProgramFiles) { $env:ProgramFiles } else { "C:\Program Files" }
$APP_BIN_DEFAULT = "$PF\srtclaw\睿云智能工作台.exe"
$APP_BIN_LEGACY = if ($env:LOCALAPPDATA) { "$env:LOCALAPPDATA\Programs\睿云智能工作台\睿云智能工作台.exe" } else { "" }
$APP_BIN = if ($env:APP_BIN) { $env:APP_BIN }
           elseif (Test-Path $APP_BIN_DEFAULT) { $APP_BIN_DEFAULT }
           elseif ($APP_BIN_LEGACY -and (Test-Path $APP_BIN_LEGACY)) { $APP_BIN_LEGACY }
           else { $APP_BIN_DEFAULT }
$APP_KILL_PATTERN = "睿云智能工作台.exe"
$DEBUG_PORT = if ($env:DEBUG_PORT) { $env:DEBUG_PORT } else { "9222" }
$LOG_FILE = if ($env:LOG_FILE) { $env:LOG_FILE } else { "$PSScriptRoot\logs\srtclaw_app.log" }
$LAUNCH_TIMEOUT_S = if ($env:LAUNCH_TIMEOUT_S) { $env:LAUNCH_TIMEOUT_S } else { 60 }

# ---------- 1. 开发环境变量 ----------
$env:SRTCLAW_APP_ENV = "dev"
$env:SRTCLAW_LOGIN_URL = "https://ruiyun-dev.3ren.cn/wisdom/commonLogin?"
$env:VITE_SRTCLAW_LOGIN_URL = "https://ruiyun-dev.3ren.cn/wisdom/commonLogin?"
$env:SRTCLAW_MANAGE_SERVICE_BASE_URL = "https://api-dev.3ren.cn/claw-manage-service"
$env:SRTCLAW_INSTITUTION_API_BASE_URL = "https://ruiyun-api-dev.3ren.cn"
$env:SRTCLAW_TEACHING_API_BASE_URL = "https://api-dev.3ren.cn"
$env:JIUWENCLAW_UPDATE_API_URL = "https://appadmin-test.yanxiu.com/app/log/uploadDeviceLog/release.do"

# ---------- 2. 环境清理 ----------
Remove-Item Env:\ELECTRON_RUN_AS_NODE -ErrorAction SilentlyContinue

$CHROMIUM_FLAGS = @(
    "--remote-debugging-port=$DEBUG_PORT",
    "--no-sandbox",
    "--disable-gpu",
    "--disable-gpu-compositing"
)

# ---------- 3. 干跑模式 ----------
if ($Mode -eq "--dry-run") {
    Write-Output "APP_BIN=$APP_BIN"
    Write-Output "---- 环境变量 ----"
    foreach ($v in "SRTCLAW_APP_ENV","SRTCLAW_LOGIN_URL","VITE_SRTCLAW_LOGIN_URL",
             "SRTCLAW_MANAGE_SERVICE_BASE_URL","SRTCLAW_INSTITUTION_API_BASE_URL",
             "SRTCLAW_TEACHING_API_BASE_URL","JIUWENCLAW_UPDATE_API_URL") {
        $val = [Environment]::GetEnvironmentVariable($v)
        Write-Output "  $v = $val"
    }
    Write-Output "---- 将执行 ----"
    Write-Output "  & `"$APP_BIN`" $CHROMIUM_FLAGS"
    exit 0
}

# ---------- 4. 结束应用 ----------
function Stop-App {
    $running = Get-Process -Name "睿云智能工作台" -ErrorAction SilentlyContinue
    if (-not $running) {
        Write-Output "启动前检查：无残留实例"
        return $true
    }
    Write-Output "清理已有实例..."
    for ($i = 1; $i -le 5; $i++) {
        Stop-Process -Name "睿云智能工作台" -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 1
        if (-not (Get-Process -Name "睿云智能工作台" -ErrorAction SilentlyContinue)) {
            Write-Output "  已清理干净（第 $i 轮）"
            return $true
        }
    }
    Write-Warning "警告：5 轮后仍有实例存活"
    return $false
}

if ($Mode -eq "--stop") {
    Stop-App
    exit $?
}

if (-not (Test-Path $APP_BIN)) {
    Write-Error "找不到可执行文件: $APP_BIN"
    exit 1
}

Stop-App | Out-Null

# ---------- 5. 后台脱离启动 ----------
if ($Mode -eq "--detach") {
    $python = if (Get-Command python -ErrorAction SilentlyContinue) { "python" } else { "python3" }
    
    $logDir = Split-Path $LOG_FILE
    if (-not (Test-Path $logDir)) {
        New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    }

    $pycode = @"
import os, subprocess, sys
binary, port, logfile = sys.argv[1], sys.argv[2], sys.argv[3]
env = dict(os.environ)
env.pop('ELECTRON_RUN_AS_NODE', None)
with open(logfile, 'w', encoding='utf-8') as log:
    p = subprocess.Popen(
        [binary, f'--remote-debugging-port={port}', '--no-sandbox', '--disable-gpu', '--disable-gpu-compositing'],
        env=env, creationflags=subprocess.CREATE_NEW_PROCESS_GROUP | 0x00000008,
        stdout=log, stderr=subprocess.STDOUT, cwd=os.environ.get('TEMP', 'C:\\')
    )
print(f'[detach] 已启动 pid={p.pid} 日志={logfile}')
"@
    & $python -c $pycode $APP_BIN $DEBUG_PORT $LOG_FILE
    
    for ($i = 1; $i -le $LAUNCH_TIMEOUT_S; $i++) {
        try {
            $resp = Invoke-WebRequest -Uri "http://127.0.0.1:$DEBUG_PORT/json/version" -TimeoutSec 2 -UseBasicParsing -ErrorAction Stop
            Write-Output "CDP 就绪: http://127.0.0.1:$DEBUG_PORT （等待 ${i}s）"
            exit 0
        } catch {
            Start-Sleep -Seconds 1
        }
    }
    Write-Error "端口 $DEBUG_PORT 在 ${LAUNCH_TIMEOUT_S}s 内未就绪，检查 $LOG_FILE"
    exit 1
}

# ---------- 6. 前台启动 ----------
& $APP_BIN $CHROMIUM_FLAGS
