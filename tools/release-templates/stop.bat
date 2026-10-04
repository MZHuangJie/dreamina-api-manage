@echo off
chcp 65001 >nul
echo.
echo   正在停止服务 ...
taskkill /FI "WINDOWTITLE eq dreamina-sidecar*" /T /F >nul 2>&1
taskkill /IM manager.exe /F >nul 2>&1
echo   已停止。
echo.
pause
