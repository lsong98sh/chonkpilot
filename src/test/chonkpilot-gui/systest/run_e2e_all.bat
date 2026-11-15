@echo off
rem 全量 E2E：先跑常规测试点（默认不含破坏性），再独立跑破坏性批（独立工作区/HOME）。
rem 任一阶段失败 → 最终退出码非零；两阶段都会执行以便一次看到全部结果。
setlocal
cd /d "%~dp0"

python run_e2e.py %*
set RC_NORMAL=%errorlevel%

python run_e2e_destructive.py %*
set RC_DESTR=%errorlevel%

if not "%RC_NORMAL%"=="0" exit /b %RC_NORMAL%
exit /b %RC_DESTR%
