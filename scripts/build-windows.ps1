# 构建 湉码 Windows 桌面端与安装向导
# 用法：在仓库根目录执行  powershell -File scripts/build-windows.ps1
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$Go = $env:GO_EXE
if (-not $Go) {
  foreach ($c in @(
      "go",
      "E:\pro\tools\go\bin\go.exe",
      "$env:USERPROFILE\go\bin\go.exe"
    )) {
    if (Get-Command $c -ErrorAction SilentlyContinue) { $Go = (Get-Command $c).Source; break }
    if (Test-Path $c) { $Go = $c; break }
  }
}
if (-not $Go) { throw "go.exe not found. Set GO_EXE." }

Write-Host "==> frontend build"
Push-Location frontend
if (Get-Command npm.cmd -ErrorAction SilentlyContinue) { npm.cmd ci; npm.cmd run build }
else { npm ci; npm run build }
Pop-Location

New-Item -ItemType Directory -Force -Path bin, cmd\installer\assets | Out-Null

Write-Host "==> tiancode.exe"
& $Go build -tags "desktop,production" -ldflags="-H windowsgui -s -w" -o bin\tiancode.exe .

Write-Host "==> uninstall.exe"
& $Go build -ldflags="-H windowsgui -s -w" -o bin\uninstall.exe .\cmd\uninstaller
Copy-Item bin\tiancode.exe cmd\installer\assets\tiancode.exe -Force
Copy-Item bin\uninstall.exe cmd\installer\assets\uninstall.exe -Force

Write-Host "==> Tiancode_Setup"
& $Go build -ldflags="-H windowsgui -s -w" -o bin\Tiancode_Setup_v0.0.1.exe .\cmd\installer

Get-ChildItem bin\tiancode.exe, bin\uninstall.exe, bin\Tiancode_Setup_v0.0.1.exe | Format-Table Name, Length
Write-Host "done. binaries are gitignored (*.exe)."
