@echo off
rem 延时打开管理界面。
rem
rem 单独成一个文件是有原因的：核心是前台阻塞运行的，启动脚本没法在它之后
rem 再做任何事；而把这段逻辑塞进 `start cmd /c "..."` 又会遇到嵌套引号——
rem 批处理里双引号不能用反斜杠转义，写出来必然出问题。拆开就干净了。
timeout /t 5 /nobreak >nul
start "" "http://127.0.0.1:8787"
exit
