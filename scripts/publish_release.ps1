# GitHub Release publish/fix script (0.0.04+). All-ASCII discipline:
# PS5.1 reads BOM-less files as ANSI/GBK - any non-ASCII char in THIS file
# becomes mojibake and can even break the parser (this is exactly how the
# v0.0.01-v0.0.03 release notes turned into '?'). Notes content lives in
# UTF-8 files under dist/notes/ (data, read explicitly as UTF-8), while
# this script stays ASCII-only. Transport is curl.exe (--data-binary @file
# is byte-exact; Invoke-RestMethod has too many encoding pitfalls).
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/publish_release.ps1 -Version 0.0.04
param(
    [Parameter(Mandatory = $true)]
    [string]$Version
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Repo = "zhangqi77ok-sys/tiancode"

# ---- credentials: git credential manager (LF-only ASCII input file + cmd type pipe) ----
$credFile = Join-Path $env:TEMP "tiancode-cred-input.txt"
[System.IO.File]::WriteAllText($credFile, "protocol=https`nhost=github.com`n`n", [System.Text.Encoding]::ASCII)
$cred = cmd /c "type `"$credFile`" | git credential fill" 2>$null
Remove-Item $credFile -ErrorAction SilentlyContinue
$token = ($cred | Select-String '^password=(.+)$').Matches[0].Groups[1].Value
if (-not $token) { throw "cannot get GitHub credential from git credential manager" }

$utf8 = New-Object System.Text.UTF8Encoding $false
$tmpJson = Join-Path $env:TEMP "tiancode-release-body.json"

function Write-JsonFile($obj, [string]$path) {
    $json = ConvertTo-Json -InputObject $obj -Depth 5 -Compress
    [System.IO.File]::WriteAllText($path, $json, $utf8)
}

function Invoke-GhApi([string]$Method, [string]$Uri, $BodyObj) {
    if ($null -ne $BodyObj) {
        Write-JsonFile $BodyObj $tmpJson
        $out = & curl.exe -sS -X $Method -H "Authorization: token $token" `
            -H "Accept: application/vnd.github+json" `
            -H "Content-Type: application/json; charset=utf-8" `
            --data-binary "@$tmpJson" $Uri 2>&1
    } else {
        $out = & curl.exe -sS -X $Method -H "Authorization: token $token" `
            -H "Accept: application/vnd.github+json" $Uri 2>&1
    }
    $text = ($out | Out-String).Trim()
    if ($text -match '"message"\s*:\s*".*?(Validation|Not Found|long)') {
        throw "GitHub API failed ($Method $Uri): $($text.Substring(0, [Math]::Min(400, $text.Length)))"
    }
    return ($text | ConvertFrom-Json)
}

# ---- 1. fix mojibake notes on old releases (only when docs/release-notes/vX.md exists) ----
$releases = Invoke-GhApi GET "https://api.github.com/repos/$Repo/releases" $null
foreach ($rel in $releases) {
    $notesFile = Join-Path $Root "docs/release-notes/v$($rel.tag_name.TrimStart('v')).md"
    $isMojibake = $rel.body -match '\?\?'
    Write-Host ("  release {0}: body_len={1} mojibake={2} notes={3}" -f $rel.tag_name, $rel.body.Length, $isMojibake, (Test-Path $notesFile))
    if ((Test-Path $notesFile) -and $isMojibake) {
        Write-Host "==> fixing mojibake release notes: $($rel.tag_name)"
        $newBody = [System.IO.File]::ReadAllText($notesFile, $utf8)
        Invoke-GhApi PATCH "https://api.github.com/repos/$Repo/releases/$($rel.id)" @{ body = $newBody } | Out-Null
        Write-Host "    fixed"
    }
}

# ---- 2. create (or update) the release for this version ----
$tag = "v$Version"
$notesFile = Join-Path $Root "docs/release-notes/v$Version.md"
if (-not (Test-Path $notesFile)) { throw "missing release notes: $notesFile" }
$notes = [System.IO.File]::ReadAllText($notesFile, $utf8)

$rel = $releases | Where-Object { $_.tag_name -eq $tag }
if ($null -ne $rel) {
    # 重建：同名资产与 DELETE 的竞态很难逐个处理——整个 release 删掉重建
    #（tag 保留，不重打；release body 随后由 POST 重新写入）
    Write-Host "==> release $tag exists: recreating (drops old assets)"
    Invoke-GhApi DELETE "https://api.github.com/repos/$Repo/releases/$($rel.id)" $null | Out-Null
    Start-Sleep -Seconds 2
}
Write-Host "==> creating release $tag"
$rel = Invoke-GhApi POST "https://api.github.com/repos/$Repo/releases" @{
    tag_name = $tag
    name     = "tiancode $tag"
    body     = $notes
}

# ---- 3. upload assets (delete existing first, so content is always this build) ----
$existing = Invoke-GhApi GET "https://api.github.com/repos/$Repo/releases/$($rel.id)/assets" $null
foreach ($f in @("tiancode-setup-v$Version.exe", "tiancode-v$Version-portable.zip")) {
    $path = Join-Path $Root "dist/$f"
    if (-not (Test-Path $path)) { throw "missing artifact: $path" }
    $old = $existing | Where-Object { $_.name -eq $f }
    if ($old) {
        Write-Host "==> deleting old asset: $f"
        try {
            Invoke-GhApi DELETE "https://api.github.com/repos/$Repo/releases/$($rel.id)/assets/$($old.id)" $null | Out-Null
            Start-Sleep -Milliseconds 500
        } catch {
            # 404 = 已不存在（幂等），继续上传
            Write-Host "    (old asset already gone)"
        }
    }
    Write-Host "==> uploading: $f ($([math]::Round((Get-Item $path).Length / 1MB, 2)) MB)"
    $uploadUri = "https://uploads.github.com/repos/$Repo/releases/$($rel.id)/assets?name=$f"
    $uploaded = $false
    for ($attempt = 1; $attempt -le 3 -and -not $uploaded; $attempt++) {
        $out = & curl.exe -sS -X POST -H "Authorization: token $token" `
            -H "Content-Type: application/octet-stream" `
            --data-binary "@$path" $uploadUri 2>&1
        $text = ($out | Out-String)
        if ($text -match '"state"\s*:\s*"uploaded"') {
            $uploaded = $true
            break
        }
        if ($text -match 'already_exists') {
            # 同名资产仍在（删除有延迟）：重新拉列表按 id 删除后重试
            $fresh = Invoke-GhApi GET "https://api.github.com/repos/$Repo/releases/$($rel.id)/assets" $null
            foreach ($a in ($fresh | Where-Object { $_.name -eq $f })) {
                try {
                    Invoke-GhApi DELETE "https://api.github.com/repos/$Repo/releases/$($rel.id)/assets/$($a.id)" $null | Out-Null
                } catch {
                    Write-Host "    (delete retry: asset may be gone)"
                }
                Start-Sleep -Seconds 2
            }
            continue
        }
        throw "upload failed $f : $($text.Substring(0, [Math]::Min(300, $text.Length)))"
    }
    if (-not $uploaded) { throw "upload failed after retries: $f" }

    # sha256 清单资产（0.0.21）：随包上传 <name>.sha256（裸哈希行），selfupdate
    # 下载后验哈希——"诚实边界"从 TLS+尺寸升级为真校验。清单跟主资产同轮重试。
    $shaFile = "$path.sha256"
    $hash = (Get-FileHash -Path $path -Algorithm SHA256).Hash.ToLowerInvariant()
    [System.IO.File]::WriteAllText($shaFile, "$hash`n", (New-Object System.Text.UTF8Encoding $false))
    $shaName = "$f.sha256"
    $shaUri = "https://uploads.github.com/repos/$Repo/releases/$($rel.id)/assets?name=$shaName"
    $shaOk = $false
    for ($attempt = 1; $attempt -le 3 -and -not $shaOk; $attempt++) {
        $out = & curl.exe -sS -X POST -H "Authorization: token $token" `
            -H "Content-Type: application/octet-stream" `
            --data-binary "@$shaFile" $shaUri 2>&1
        $text = ($out | Out-String)
        if ($text -match '"state"\s*:\s*"uploaded"') { $shaOk = $true; break }
        if ($text -match 'already_exists') {
            $fresh = Invoke-GhApi GET "https://api.github.com/repos/$Repo/releases/$($rel.id)/assets" $null
            foreach ($a in ($fresh | Where-Object { $_.name -eq $shaName })) {
                try { Invoke-GhApi DELETE "https://api.github.com/repos/$Repo/releases/$($rel.id)/assets/$($a.id)" $null | Out-Null } catch { }
                Start-Sleep -Seconds 2
            }
            continue
        }
        throw "upload failed $shaName : $($text.Substring(0, [Math]::Min(300, $text.Length)))"
    }
    if (-not $shaOk) { throw "upload failed after retries: $shaName" }
    Write-Host "==> uploaded: $shaName ($hash)"
    Remove-Item $shaFile -ErrorAction SilentlyContinue
}

Remove-Item $tmpJson -ErrorAction SilentlyContinue
Write-Host "publish OK: https://github.com/$Repo/releases/tag/$tag"
