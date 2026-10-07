@echo off
setlocal

rem reset-proxy.bat — Huy proxy he thong Windows do Rikkei Lms Connect bat len khi giam sat.
rem Dung script nay khi: app bi Task Manager kill / go / crash trong luc dang giam sat, khien
rem may khong vao duoc Internet (trinh duyet bao loi ket noi proxy).
rem An toan de chay nhieu lan, khong can quyen Administrator (chi sua HKCU cua user hien tai).

echo ============================================
echo   Huy proxy he thong (Rikkei Lms Connect)
echo ============================================

reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyEnable /t REG_DWORD /d 0 /f >nul
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyServer /f >nul 2>&1
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyOverride /f >nul 2>&1

del /f /q "%LOCALAPPDATA%\SimpleCare\proxy_state.json" >nul 2>&1

echo Da huy proxy he thong thanh cong.
echo Vui long dong va mo lai trinh duyet de ap dung.
echo.
pause
