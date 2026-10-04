@echo off
chcp 936 >nul
setlocal
cd /d %~dp0

set MANAGER_WEB_DIST=%~dp0dist\web
set MANAGER_DATA_DIR=%~dp0data
set SIDECAR_URL=http://127.0.0.1:8790
set SIDECAR_PORT=8790
set MANAGER_PORT=8787

rem 找 Chrome（sidecar 需要真实浏览器环境）
set SIDECAR_CHROME=
if exist "%ProgramFiles%\Google\Chrome\Application\chrome.exe" set SIDECAR_CHROME=%ProgramFiles%\Google\Chrome\Application\chrome.exe
if exist "%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe" set SIDECAR_CHROME=%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe
if exist "%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe" if "%SIDECAR_CHROME%"=="" set SIDECAR_CHROME=%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe

if not exist "go\manager.exe" (
  echo [错误] 找不到 go\manager.exe
  echo        请先在 go 目录执行：go build -o manager.exe ./cmd/manager
  pause
  exit /b 1
)
if not exist "sidecar\node_modules" (
  echo [错误] 找不到 sidecar\node_modules
  echo        请先在 sidecar 目录执行：pnpm install
  pause
  exit /b 1
)

echo.
echo   正在启动浏览器通道 ...
start "dreamina-sidecar" /min cmd /c "cd /d %~dp0sidecar && set SIDECAR_PORT=8790&& set SIDECAR_CHROME=%SIDECAR_CHROME%&& node server.mjs"
timeout /t 3 /nobreak >nul

rem 延时自动打开浏览器。
rem 核心是前台阻塞运行的，脚本没法在它之后再做任何事，
rem 所以只能提前派生一个「等几秒再开」的后台命令。
rem 不这么做的话，用户会对着黑窗口发呆，不知道该干什么。
start "" /min "%~dp0打开界面.bat"

echo   正在启动核心服务 ...
echo   浏览器会在几秒后自动打开；若没打开请手动访问 http://127.0.0.1:%MANAGER_PORT%
echo   关闭本窗口即停止核心；sidecar 请用「停止.bat」
echo.
go\manager.exe serve

pause
