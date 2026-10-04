@echo off
chcp 936 >nul
setlocal
cd /d %~dp0

rem ============================================================
rem  开发模式
rem ============================================================
rem  只起两个东西：
rem    1. Vite 开发服务器（前端热更新）
rem    2. Electron（它自己会拉起 Go 核心和浏览器通道）
rem
rem  刻意不单独起核心和 sidecar —— 那会变成两套独立后端，
rem  界面连的是其中一套，而你在另一套上看日志，排查会很痛苦。
rem ============================================================

set MANAGER_DATA_DIR=%~dp0data
set MANAGER_WEB_DIST=%~dp0dist\web
rem 固定核心端口：Vite 的代理目标是写死的 8787，
rem 端口对不上界面就会连到别的核心，或者干脆没人接。
set MANAGER_CORE_PORT=8787
set MANAGER_DEV_URL=http://127.0.0.1:5173

if not exist "go\manager.exe" (
  echo [错误] 找不到 go\manager.exe，请先在 go 目录执行 go build
  pause
  exit /b 1
)
if not exist "desktop\node_modules\electron\dist\electron.exe" (
  echo [错误] Electron 未安装，请在 desktop 目录执行 pnpm install
  pause
  exit /b 1
)

echo.
echo   [1/2] 启动 Vite 开发服务器 ...
start "dm-vite" /min cmd /c "cd /d %~dp0 && pnpm dev:web"
echo         等待 Vite 就绪 ...
timeout /t 9 /nobreak >nul

echo   [2/2] 启动 Electron（会自动拉起核心与浏览器通道）...
cd desktop
set ELECTRON_RUN_AS_NODE=
.\node_modules\electron\dist\electron.exe .

echo.
echo   Electron 已退出。停止 Vite 请运行「停止.bat」。
pause
