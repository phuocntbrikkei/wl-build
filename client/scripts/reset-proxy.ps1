# reset-proxy.ps1 - Huy proxy he thong Windows do Rikkei Lms Connect bat len khi giam sat.
# Dung khi: app bi Task Manager kill / go / crash trong luc dang giam sat, khien may khong vao
# duoc Internet (trinh duyet bao loi ket noi proxy). An toan chay nhieu lan, khong can quyen Admin.

$ErrorActionPreference = "Stop"

Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  Huy proxy he thong (Rikkei Lms Connect)    " -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan

$key = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings"

Set-ItemProperty -Path $key -Name ProxyEnable -Value 0 -Type DWord -ErrorAction SilentlyContinue
Remove-ItemProperty -Path $key -Name ProxyServer -ErrorAction SilentlyContinue
Remove-ItemProperty -Path $key -Name ProxyOverride -ErrorAction SilentlyContinue

$stateFile = Join-Path $env:LOCALAPPDATA "SimpleCare\proxy_state.json"
if (Test-Path $stateFile) {
    Remove-Item $stateFile -Force -ErrorAction SilentlyContinue
}

try {
    Add-Type -MemberDefinition @"
[DllImport("wininet.dll", SetLastError = true)]
public static extern bool InternetSetOption(IntPtr hInternet, int dwOption, IntPtr lpBuffer, int dwBufferLength);
"@ -Namespace WinAPI -Name Internet -ErrorAction Stop

    [WinAPI.Internet]::InternetSetOption([IntPtr]::Zero, 39, [IntPtr]::Zero, 0) | Out-Null
    [WinAPI.Internet]::InternetSetOption([IntPtr]::Zero, 37, [IntPtr]::Zero, 0) | Out-Null
} catch {
    Write-Host "[!] Khong the lam moi cai dat mang ngay - hay dong va mo lai trinh duyet." -ForegroundColor Yellow
}

Write-Host "[+] Da huy proxy he thong thanh cong." -ForegroundColor Green
