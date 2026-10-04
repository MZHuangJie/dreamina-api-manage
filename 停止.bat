@echo off
chcp 936 >nul
echo.
echo   ÕýÔÚÍ£Ö¹ ...
taskkill /FI "WINDOWTITLE eq dreamina-sidecar*" /T /F >nul 2>&1
taskkill /FI "WINDOWTITLE eq dm-sidecar*" /T /F >nul 2>&1
taskkill /FI "WINDOWTITLE eq dm-core*" /T /F >nul 2>&1
taskkill /FI "WINDOWTITLE eq dm-vite*" /T /F >nul 2>&1
taskkill /IM manager.exe /F >nul 2>&1
echo   ÒÑÍ£Ö¹¡£
echo.
pause
