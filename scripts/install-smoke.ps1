# tiancode installer smoke test: install -> launch -> uninstall, with full artifact assertions.
#
# Why this script backs up the registry first:
#   The installer writes HKCU\...\Uninstall\tiancode, and uninstall DELETES that key.
#   If the machine already has a registered tiancode (e.g. an older version), running this
#   test without backup/restore would silently destroy that user's "Apps & features" entry.
#   Order is therefore fixed: backup -> smoke -> restore -> VERIFY restore.
#
# Why .NET registry API instead of the PowerShell HKCU: provider:
#   Writes through the provider item returned by Get-Item silently did not persist in this
#   environment (the restore reported 0 values). [Microsoft.Win32.Registry]::CurrentUser is
#   explicit and verifiable, so all registry access below goes through it.
#
# Why launch verification reads %APPDATA%\tiancode\tiancode.log instead of MainWindowTitle:
#   main.go writes a lifecycle line from OnStartup precisely as the assertable proof that the
#   window came up ("窗口就绪的可断言证据"). MainWindowTitle is racy here: in a non-interactive
#   session it can stay empty even though the app started fine (observed), which turns a working
#   build into a false failure. The log line is written by the app itself, so it is authoritative.
#
# Why no fixed sleeps for artifact checks:
#   Uninstall schedules a delayed self-cleanup and removes shortcuts synchronously; fixed sleeps
#   made results depend on machine load. Every post-condition below is polled with a deadline.
#
# Why ASCII-only source:
#   Windows PowerShell 5.1 reads .ps1 as ANSI unless the file has a BOM, which garbles non-ASCII
#   literals. Keeping this script ASCII removes that failure mode entirely.
#
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File scripts\install-smoke.ps1
# Exit: 0 = all checks passed, 1 = at least one check failed.

$ErrorActionPreference = 'Continue'

$Root    = Split-Path -Parent $PSScriptRoot
$Version = (Get-Content (Join-Path $Root 'VERSION') -Raw).Trim()
$setup   = Join-Path $Root "dist\tiancode-setup-v$Version.exe"
$dir     = Join-Path $env:LOCALAPPDATA 'Programs\tiancode-smoke'

$keyPath = 'SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\tiancode'
$backup  = Join-Path $env:TEMP 'tiancode-uninstall-key-backup.txt'
$log     = Join-Path $env:TEMP 'tiancode-install-smoke.log'
$stash   = Join-Path $env:TEMP 'tiancode-smoke-stash'

$appDataDir = Join-Path $env:APPDATA 'tiancode'
$cfg        = Join-Path $appDataDir 'config.json'
$lifeLog    = Join-Path $appDataDir 'tiancode.log'
$setupLog   = Join-Path $appDataDir 'setup.log'

$startLnk = Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs\tiancode.lnk'
$deskLnk  = Join-Path ([Environment]::GetFolderPath('Desktop')) 'tiancode.lnk'
$appExe   = Join-Path $dir 'tiancode.exe'
$self     = Join-Path $dir 'tiancode-setup.exe'

New-Item -ItemType Directory -Force -Path $stash | Out-Null
Set-Content -Path $log -Value "=== tiancode install smoke $(Get-Date -Format s) ===" -Encoding utf8
function W($m) { Add-Content -Path $log -Value $m -Encoding utf8 }

$script:fail = 0
function Check($name, $cond) {
    $s = if ($cond) { 'PASS' } else { 'FAIL' }
    if (-not $cond) { $script:fail++ }
    W ("  [$s] " + $name)
}

# Poll a condition until it holds or the deadline passes. Returns the final condition value.
function Wait-For($timeoutSec, [scriptblock]$cond) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (& $cond) { return $true }
        Start-Sleep -Milliseconds 400
    }
    return (& $cond)
}

function Test-PathQ($p) { return (Test-Path -LiteralPath $p) }

# Run the setup exe. Returns the exit code, or -1 if the process could not be started.
#
# Why no -RedirectStandardError here: this environment has both "Path" and "PATH" in the process
# environment block, and Start-Process trips over that duplicate key when it has to build a
# redirected environment ("Item has already been added. Key in dictionary: 'Path'"). The installer
# is a GUI-subsystem binary whose stderr has no console anyway, so its warnings are read from the
# setup log it writes itself (see appendSetupLog in cmd/installer/install.go).
function Invoke-Setup($exe, [string[]]$setupArgs) {
    $before = if (Test-PathQ $setupLog) { (Get-Content -LiteralPath $setupLog -Raw).Length } else { 0 }
    $p = $null
    try {
        $p = Start-Process -FilePath $exe -ArgumentList $setupArgs -Wait -PassThru
    } catch {
        W ("  START FAILED: " + $_.Exception.Message)
        return -1
    }
    if ($null -eq $p) {
        W "  START FAILED: no process object returned"
        return -1
    }
    if (Test-PathQ $setupLog) {
        $now = Get-Content -LiteralPath $setupLog -Raw
        if ($now.Length -gt $before) {
            foreach ($l in ($now.Substring($before) -split "`r?`n")) {
                if ($l.Trim() -ne '') { W ("  setup.log: " + $l.Trim()) }
            }
        }
    }
    return $p.ExitCode
}

function Get-UninstallKeyValues {
    $cu = [Microsoft.Win32.Registry]::CurrentUser
    $k = $cu.OpenSubKey($keyPath)
    $vals = @{}
    if ($null -ne $k) {
        foreach ($n in $k.GetValueNames()) { $vals[$n] = [string]$k.GetValue($n) }
        $k.Close()
    }
    $cu.Close()
    return $vals
}

# ---- 0. Preflight ---- #
W "[0] preflight"
Check "setup exists" (Test-PathQ $setup)

# Back up the pre-existing uninstall key (see header).
$preVals = Get-UninstallKeyValues
$hadKey = $preVals.Count -gt 0
W ("  existing uninstall key present = " + $hadKey + " (" + $preVals.Count + " values)")
if ($hadKey) {
    $lines = @()
    $cu = [Microsoft.Win32.Registry]::CurrentUser
    $k = $cu.OpenSubKey($keyPath)
    foreach ($n in $k.GetValueNames()) {
        $lines += ($n + "`t" + [string]$k.GetValueKind($n) + "`t" + [string]$k.GetValue($n))
    }
    $k.Close(); $cu.Close()
    $lines | Out-File -Encoding utf8 $backup
    W ("  backup written to " + $backup)
}

# Do not destroy a pre-existing desktop shortcut: stash it and put it back at the end.
$hadDeskLnk = Test-PathQ $deskLnk
if ($hadDeskLnk) {
    Move-Item -LiteralPath $deskLnk -Destination (Join-Path $stash 'desktop-tiancode.lnk') -Force
    W "  pre-existing desktop shortcut stashed (will be restored)"
}
$hadCfg = Test-PathQ $cfg
if ($hadCfg) {
    Move-Item -LiteralPath $cfg -Destination (Join-Path $stash 'config.json') -Force
    W "  pre-existing config.json stashed (will be restored)"
}
Check "desktop shortcut absent at start" (-not (Test-PathQ $deskLnk))

# ---- 1. Install (default: start menu + desktop shortcuts) ---- #
W "[1] install -quiet (desktop shortcut enabled)"
Check "installer exit code 0" ((Invoke-Setup $setup @('-quiet', '-dir', $dir)) -eq 0)
Check "app exe installed"             (Test-PathQ $appExe)
Check "uninstall entry installed"     (Test-PathQ $self)
Check "START MENU shortcut created"   (Test-PathQ $startLnk)
Check "DESKTOP shortcut created"      (Test-PathQ $deskLnk)
Check "uninstall registry key written" (Test-PathQ ("HKCU:\" + $keyPath))
$post = Get-UninstallKeyValues
Check "InstallLocation correct" ($post['InstallLocation'] -eq $dir)
Check "UninstallString points at copied setup" ($post['UninstallString'] -like "*tiancode-setup.exe*")

# ---- 2. Launch with a valid config: prove the app actually comes up ---- #
W "[2] launch with config (verdict = app's own lifecycle log line)"
New-Item -ItemType Directory -Force -Path $appDataDir | Out-Null
$beforeText = if (Test-PathQ $lifeLog) { Get-Content -LiteralPath $lifeLog -Raw } else { '' }
@{ baseUrl = 'http://127.0.0.1:1/v1'; apiKey = 'sk-smoke-test'; model = 'smoke'; workspace = $Root } |
    ConvertTo-Json | Set-Content -Path $cfg -Encoding utf8

$p = $null
try { $p = Start-Process -FilePath $appExe -PassThru } catch { W ("  start failed: " + $_.Exception.Message) }
if ($null -eq $p) {
    Check "app process started" $false
} else {
$started = $false
$title = ''
$deadline = (Get-Date).AddSeconds(60)
while ((Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 500
    $p.Refresh()
    if ($p.MainWindowTitle -ne '') { $title = $p.MainWindowTitle }
    if (Test-PathQ $lifeLog) {
        $now = Get-Content -LiteralPath $lifeLog -Raw
        if ($now.Length -gt $beforeText.Length) {
            $appended = $now.Substring($beforeText.Length)
            if ($appended -match 'started v') { $started = $true; break }
        }
    }
    if ($p.HasExited) { break }
}
W ("  new lifecycle lines verdict = started:" + $started + "  MainWindowTitle='" + $title + "' (title is informational)")
Check "app started (OnStartup logged 'started v...')" $started
if (-not $p.HasExited) {
    Stop-Process -Id $p.Id -Force
    Wait-For 15 { $p.Refresh(); return $p.HasExited } | Out-Null
}
W ("  app process exited = " + $p.HasExited)
}

# ---- 3. A foreign-dir uninstall must NOT delete a registration that isn't its own ---- #
# Regression guard for a real defect: the uninstall key name is fixed, so an old implementation
# deleted it unconditionally. Running uninstall with an unrelated -dir therefore removed the
# registration of a *different* installation (observed: it wiped a real install's entry).
W "[3] uninstall with a foreign -dir must not touch our registration"
$foreign = Join-Path $env:TEMP 'tiancode-foreign-probe'
Check "uninstaller exit code 0 (foreign dir)" ((Invoke-Setup $self @('-uninstall', '-quiet', '-dir', $foreign)) -eq 0)
Check "registration survived foreign uninstall" (Test-PathQ ("HKCU:\" + $keyPath))
$still = Get-UninstallKeyValues
Check "registration still points at our install" ($still['InstallLocation'] -eq $dir)
Check "app exe still present (foreign uninstall did nothing)" (Test-PathQ $appExe)

# ---- 4. Uninstall: every artifact must be gone (no orphan .lnk anywhere) ---- #
W "[4] uninstall -quiet (correct dir)"
Check "uninstaller exit code 0" ((Invoke-Setup $self @('-uninstall', '-quiet', '-dir', $dir)) -eq 0)
Check "install dir removed"           (Wait-For 20 { return -not (Test-PathQ $dir) })
Check "app exe removed"               (-not (Test-PathQ $appExe))
Check "START MENU shortcut removed"   (Wait-For 20 { return -not (Test-PathQ $startLnk) })
Check "DESKTOP shortcut removed"      (Wait-For 20 { return -not (Test-PathQ $deskLnk) })
Check "uninstall registry key removed" (Wait-For 20 { return -not (Test-PathQ ("HKCU:\" + $keyPath)) })

# ---- 5. Opt-out flag: -no-desktop-shortcut keeps the desktop clean ---- #
W "[5] install -quiet -no-desktop-shortcut"
Check "installer exit code 0"        ((Invoke-Setup $setup @('-quiet', '-no-desktop-shortcut', '-dir', $dir)) -eq 0)
Check "START MENU shortcut created"  (Test-PathQ $startLnk)
Check "DESKTOP shortcut NOT created" (-not (Test-PathQ $deskLnk))

# ---- 6. Uninstall after the opt-out install: must still clean both positions ---- #
W "[6] uninstall -quiet (after opt-out install)"
Check "uninstaller exit code 0" ((Invoke-Setup $self @('-uninstall', '-quiet', '-dir', $dir)) -eq 0)
Check "install dir removed"           (Wait-For 20 { return -not (Test-PathQ $dir) })
Check "START MENU shortcut removed"   (Wait-For 20 { return -not (Test-PathQ $startLnk) })
Check "DESKTOP shortcut removed"      (Wait-For 20 { return -not (Test-PathQ $deskLnk) })
Check "uninstall registry key removed" (Wait-For 20 { return -not (Test-PathQ ("HKCU:\" + $keyPath)) })

# ---- 7. Restore the pre-existing uninstall key and verify value-for-value ---- #
W "[7] restore pre-existing uninstall key"
if (-not $hadKey) {
    W "  no pre-existing key, nothing to restore"
} else {
    $cu = [Microsoft.Win32.Registry]::CurrentUser
    $cu.DeleteSubKeyTree($keyPath, $false)
    $dst = $cu.CreateSubKey($keyPath)
    $want = @{}
    foreach ($line in (Get-Content -LiteralPath $backup -Encoding utf8)) {
        if ([string]::IsNullOrWhiteSpace($line)) { continue }
        $parts = $line -split "`t"
        if ($parts.Count -lt 3) { continue }
        $n = $parts[0]; $kind = $parts[1]; $v = $parts[2]
        if ($kind -eq 'DWord') {
            $dst.SetValue($n, [int]$v, [Microsoft.Win32.RegistryValueKind]::DWord)
        } else {
            $dst.SetValue($n, $v, [Microsoft.Win32.RegistryValueKind]::String)
        }
        $want[$n] = $v
    }
    $dst.Close()
    $chk = $cu.OpenSubKey($keyPath)
    $restored = @{}
    foreach ($n in $chk.GetValueNames()) { $restored[$n] = [string]$chk.GetValue($n) }
    $chk.Close(); $cu.Close()

    Check ("restored value count matches (" + $want.Count + ")") ($restored.Count -eq $want.Count)
    $mismatch = @()
    foreach ($n in $want.Keys) {
        if (-not $restored.ContainsKey($n) -or $restored[$n] -ne $want[$n]) { $mismatch += $n }
    }
    Check ("all restored values match: " + ($want.Keys -join ', ')) ($mismatch.Count -eq 0)
    if ($mismatch.Count -gt 0) { W ("  mismatched: " + ($mismatch -join ', ')) }
}

# ---- 8. Restore stashed state and leave the machine as we found it ---- #
W "[8] restore stashed state"
if ($hadDeskLnk) {
    Move-Item -LiteralPath (Join-Path $stash 'desktop-tiancode.lnk') -Destination $deskLnk -Force
}
Check "desktop shortcut state matches pre-test" ((Test-PathQ $deskLnk) -eq $hadDeskLnk)

if ($hadCfg) {
    Move-Item -LiteralPath (Join-Path $stash 'config.json') -Destination $cfg -Force
} elseif (Test-PathQ $cfg) {
    Remove-Item -LiteralPath $cfg -Force
}
Check "config.json back to pre-test state" ((Test-PathQ $cfg) -eq $hadCfg)

W "=== DONE: failures=$script:fail ==="
if ($script:fail -gt 0) { exit 1 }
exit 0
