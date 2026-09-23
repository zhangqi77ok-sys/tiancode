# tiancode 发布流水线：门禁 → 前端 → 应用 exe → 安装包 → 便携包
# 用法: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1
# 交付纪律：每次开发完成必须执行本脚本产出安装包（见 docs/STANDARDS.md 交付纪律）。
# 注：保持本脚本 ASCII-only（Windows PowerShell 5.1 按 ANSI 读无 BOM 文件会乱码）。
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

# Prerequisite check: fail fast, with an actionable message.
# Why this exists (2026-09-23): on a machine where the Go toolchain was installed but not on PATH,
# this script died at step 1 with a cryptic PowerShell error:
#   "gofmt : The term 'gofmt' is not recognized as the name of a cmdlet, function, script file..."
#   + CommandNotFoundException at release.ps1:12
# That reads like a bug in the script, not a missing toolchain, and it costs a full debugging
# round-trip. Check the prerequisites up front instead of discovering them one step at a time.
function Require-Command([string]$Name, [string]$Hint) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "prerequisite missing: '$Name' was not found on PATH. $Hint"
    }
}
Require-Command 'go'    'Install Go 1.22+ and add its bin directory to PATH, then reopen the shell.'
Require-Command 'gofmt' 'gofmt ships with the Go toolchain; if go is found but gofmt is not, the Go install is incomplete.'
Require-Command 'node'  'Install Node 18+ and add its directory to PATH, then reopen the shell.'
Require-Command 'npm'   'npm ships with Node; if node is found but npm is not, the Node install is incomplete.'

$Version = (Get-Content VERSION -Raw).Trim()
if (-not $Version) { throw "VERSION file is empty" }

Write-Host "==> [1/6] gates: gofmt / vet / test / arch_check"
$fmt = gofmt -l main.go app internal cmd
if ($fmt) { throw "gofmt failed:`n$fmt" }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
go test ./... -count=1
if ($LASTEXITCODE -ne 0) { throw "go test failed" }
& powershell -NoProfile -ExecutionPolicy Bypass -File scripts/arch_check.ps1
if ($LASTEXITCODE -ne 0) { throw "arch_check failed" }

Write-Host "==> [2/6] frontend build"
Push-Location frontend
try {
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "frontend build failed" }
} finally {
    Pop-Location
}

Write-Host "==> [3/6] app exe"
New-Item -ItemType Directory -Force -Path build, dist, cmd\installer\payload | Out-Null
# 必须带 wails 生产构建标签（desktop,production），否则运行时报
# "Wails applications will not build without the correct build tags"。
go build -tags desktop,production -ldflags="-H windowsgui -s -w -X main.version=$Version" -o build\tiancode.exe .
if ($LASTEXITCODE -ne 0) { throw "app build failed" }

Write-Host "==> [4/6] installer (native Go, no NSIS required)"
Copy-Item build\tiancode.exe cmd\installer\payload\tiancode.exe -Force
go build -tags installer_payload -ldflags="-H windowsgui -s -w -X main.version=$Version" -o "dist\tiancode-setup-v$Version.exe" .\cmd\installer
if ($LASTEXITCODE -ne 0) { throw "installer build failed" }
Remove-Item cmd\installer\payload\tiancode.exe -Force -ErrorAction SilentlyContinue

Write-Host "==> [5/6] portable zip"
Compress-Archive -Path build\tiancode.exe, README.md -DestinationPath "dist\tiancode-v$Version-portable.zip" -Force

Write-Host "==> [6/6] artifacts"
Get-ChildItem dist | Select-Object Name, @{ n = 'MB'; e = { [math]::Round($_.Length / 1MB, 2) } } | Format-Table -AutoSize
Write-Host "release OK: dist\ (version $Version)"
