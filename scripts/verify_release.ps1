# Verify a GitHub release: Chinese intact (no mojibake) + assets present. ASCII-only.
param([string]$Tag = "v0.0.04")

$ErrorActionPreference = "Stop"
$p = Join-Path $env:TEMP "tiancode-cred-input.txt"
[System.IO.File]::WriteAllText($p, "protocol=https`nhost=github.com`n`n", [System.Text.Encoding]::ASCII)
$cred = cmd /c "type `"$p`" | git credential fill" 2>$null
Remove-Item $p -ErrorAction SilentlyContinue
$token = ($cred | Select-String '^password=(.+)$').Matches[0].Groups[1].Value
$headers = @{ Authorization = "token $token"; "User-Agent" = "tiancode-release"; Accept = "application/vnd.github+json" }
$r = Invoke-RestMethod -Uri "https://api.github.com/repos/zhangqi77ok-sys/tiancode/releases/tags/$Tag" -Headers $headers
Write-Host ("name        = {0}" -f $r.name)
Write-Host ("body_chinese= {0}" -f ($r.body -match '[\u4e00-\u9fa5]'))
Write-Host ("body_mojibake={0}" -f $r.body.Contains('??'))
foreach ($a in $r.assets) {
    Write-Host ("asset       = {0} ({1} bytes, state={2})" -f $a.name, $a.size, $a.state)
}
Write-Host ("body_preview= {0}" -f $r.body.Substring(0, 80))
