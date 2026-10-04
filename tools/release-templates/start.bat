@echo off
chcp 65001 >nul
setlocal

set ROOT=%~dp0
set MANAGER_WEB_DIST=%ROOT%web
set MANAGER_DATA_DIR=%ROOT%data
set SIDECAR_URL=http://127.0.0.1:8790
set SIDECAR_HEADLESS=true
if "%MANAGER_PORT%"=="" set MANAGER_PORT=8787
if "%SIDECAR_SECRET%"=="" set SIDECAR_SECRET=

echo.
echo   正在启动浏览器通道 sidecar ...
start "dreamina-sidecar" /min cmd /c "cd /d "%ROOT%sidecar" && set SIDECAR_PORT=8790&& set SIDECAR_SECRET=%SIDECAR_SECRET%&& node server.mjs"

timeout /t 2 /nobreak >nul

echo   正在启动核心服务 ...
echo.
"%ROOT%manager.exe" serve
