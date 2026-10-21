@echo off
setlocal
REM The PowerShell script validates tools, source state, and new output paths.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0build_release.ps1" %*
exit /b %ERRORLEVEL%
