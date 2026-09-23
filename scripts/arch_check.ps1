# Architecture guard (rule evolution: see docs/adr/0004-guard-rules.md)
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/arch_check.ps1
# Exit code 0 = pass, 1 = violations found. Must pass before every commit / in CI.
# NOTE: keep this script ASCII-only to avoid encoding issues under Windows PowerShell 5.1.
$ErrorActionPreference = "Stop"
$fail = 0
$root = (Get-Location).Path
$goFiles = Get-ChildItem -Recurse -Filter *.go | Where-Object { $_.FullName -notmatch '\\vendor\\|\\frontend\\' }

# R1 dependency direction: internal/core/** must not import orchestrator/adapters/shell/wails
# Scope note (2026-09-23): _test.go files are excluded on purpose.
#   R1 guards the *production* dependency direction -- what actually ships and what
#   `go build ./...` links. Two reasons tests must not be scanned:
#     1) a test under internal/core may legitimately reference another layer (test-only import
#        creates no production dependency and no cycle);
#     2) this rule is a substring match, so a test that merely *mentions* these import paths in a
#        comment or in an assertion string flags itself -- e.g. the C-RT-4 facade-boundary test
#        literally lists the forbidden paths to assert against them.
#   R2 below already excludes tests for the same reason; keeping R1 consistent avoids false reds,
#   which are how a guard earns the "it always complains, ignore it" reputation.
foreach ($f in $goFiles) {
    $rel = $f.FullName.Substring($root.Length + 1)
    if ($rel -like 'internal\core\*' -and $f.Name -notlike '*_test.go') {
        $bad = Select-String -Path $f.FullName -Pattern '"tiancode/internal/(app|platform)"|"github\.com/wailsapp/wails'
        if ($bad) {
            Write-Host "[R1][FAIL] core package has cross-layer import: $rel"
            $bad | ForEach-Object { Write-Host "    line$($_.LineNumber): $($_.Line.Trim())" }
            $fail++
        }
    }
}

# R2 no silent error discard: internal/** non-test files must not contain `_ = xxx.`
foreach ($f in ($goFiles | Where-Object { $_.FullName -match '\\internal\\' -and $_.Name -notlike '*_test.go' })) {
    $bad = Select-String -Path $f.FullName -Pattern '^\s*_\s*=\s*\w+\.'
    if ($bad) {
        Write-Host "[R2][FAIL] error return value silently discarded: $($f.FullName.Substring($root.Length + 1))"
        $bad | ForEach-Object { Write-Host "    line$($_.LineNumber): $($_.Line.Trim())" }
        $fail++
    }
}

# R3 tool timeout contract: platform files defining Execute must use WithTimeout/WithDeadline
foreach ($f in ($goFiles | Where-Object { $_.FullName -match '\\internal\\platform\\' -and $_.Name -notlike '*_test.go' })) {
    $definesExecute = Select-String -Path $f.FullName -Pattern 'func .*Execute\('
    if ($definesExecute) {
        $hasTimeout = Select-String -Path $f.FullName -Pattern 'context\.WithTimeout|context\.WithDeadline'
        if (-not $hasTimeout) {
            Write-Host "[R3][FAIL] tool implementation lacks timeout contract (no WithTimeout/WithDeadline): $($f.Name)"
            $fail++
        }
    }
}

# R4 package doc: every package dir under internal/ must carry a "// Package " doc comment
$pkgDirs = $goFiles | Where-Object { $_.FullName -match '\\internal\\' } |
    ForEach-Object { $_.DirectoryName } | Sort-Object -Unique
foreach ($d in $pkgDirs) {
    $hasDoc = Get-ChildItem $d -Filter *.go | Where-Object {
        (Get-Content $_.FullName -TotalCount 40) -match '^// Package '
    }
    if (-not $hasDoc) {
        Write-Host "[R4][FAIL] package missing doc comment: $d"
        $fail++
    }
}

if ($fail -gt 0) {
    Write-Host ""
    Write-Host "[ARCH CHECK] FAILED: $fail violation(s)"
    exit 1
}
Write-Host "[ARCH CHECK] PASS"
