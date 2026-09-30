$ErrorActionPreference = "Continue"
$p = Join-Path $env:TEMP "tiancode-cred-input.txt"
[System.IO.File]::WriteAllText($p, "protocol=https`nhost=github.com`n`n", [System.Text.Encoding]::ASCII)
$cred = cmd /c "type `"$p`" | git credential fill" 2>$null
Remove-Item $p -ErrorAction SilentlyContinue
$token = ($cred | Select-String '^password=(.+)$').Matches[0].Groups[1].Value
if (-not $token) { Write-Host "NO TOKEN"; exit 1 }
Write-Host "token_len=$($token.Length)"
$headers = @{
    Authorization = "token $token"
    "User-Agent"  = "tiancode-release"
    Accept        = "application/vnd.github+json"
}
$rels = Invoke-RestMethod -Uri "https://api.github.com/repos/zhangqi77ok-sys/tiancode/releases" -Headers $headers
foreach ($r in $rels) {
    Write-Host ("REL {0} id={1} body_len={2} assets={3}" -f $r.tag_name, $r.id, $r.body.Length, $r.assets.Count)
}
