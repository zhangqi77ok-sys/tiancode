#!/usr/bin/env powershell
# scripts/arch_check.ps1
# 架构守卫 - 检查插件热插拔架构合规性
$ErrorActionPreference = "Stop"
$errors = 0

Write-Host "🔍 [ARCH CHECK] 检查插件架构合规性..." -ForegroundColor Cyan

# 规则 1: app.go 禁止直接持有具体 Tool 实例字段
$appContent = Get-Content "app.go" -Raw
if ($appContent -match '(?m)^\s+(?:gitTool|fsTool|termTool)\s+\*') {
    Write-Host "❌ [R1] app.go 持有具体 Tool 实例字段，必须通过 registry 访问" -ForegroundColor Red
    $errors++
}

# 规则 2: app.go 禁止 switch toolName hardcode 路由
if ($appContent -match 'switch\s+toolName\s*\{') {
    Write-Host "❌ [R2] app.go 使用 switch toolName hardcode 工具路由" -ForegroundColor Red
    $errors++
}

# 规则 3: app.go 禁止直接调用具体 Tool 实例方法
if ($appContent -match 'a\.(termTool|fsTool|gitTool)\.(Execute|ExecuteStream|GetStatus)') {
    Write-Host "❌ [R3] app.go 直接调用具体 Tool 实例方法，必须通过 registry" -ForegroundColor Red
    $errors++
}

# 规则 4: internal/core/ 层禁止直接 import plugins/tool/ 具体实现
# (transport/ 层因需要类型断言可以 import，但必须通过 registry 获取实例，不得持有字段)
$coreFiles = Get-ChildItem -Path "internal\core" -Filter "*.go" -Recurse -ErrorAction SilentlyContinue | Where-Object { $_.Name -notmatch "_test\.go$" }
foreach ($file in $coreFiles) {
    $fc = Get-Content $file.FullName -Raw
    if ($fc -match '"tiancode/plugins/tool/|"tcode/plugins/tool/') {
        Write-Host "❌ [R4] $($file.Name) (internal/core/) 直接 import plugins/tool/ 具体实现，违反依赖反转" -ForegroundColor Red
        $errors++
    }
}

# 规则 4b: transport/ 层若 import plugins/tool/ 必须通过 registry.GetTool() 获取，禁止持有字段
$transportFiles = Get-ChildItem -Path "internal\transport" -Filter "*.go" -Recurse -ErrorAction SilentlyContinue | Where-Object { $_.Name -notmatch "_test\.go$" }
foreach ($file in $transportFiles) {
    $fc = Get-Content $file.FullName -Raw
    if ($fc -match '"tiancode/plugins/tool/|"tcode/plugins/tool/') {
        # 允许 import，但禁止持有字段（字段声明模式）
        if ($fc -match '(?m)^\s+\w+\s+\*\w+tool\.\w+\s*$') {
            Write-Host "❌ [R4b] $($file.Name) (transport/) 持有具体 Tool 字段，必须通过 registry + 类型断言" -ForegroundColor Red
            $errors++
        }
    }
}

# 规则 5: plugins/ 禁止 import 宿主层
$pluginFiles = Get-ChildItem -Path "plugins" -Filter "*.go" -Recurse | Where-Object { $_.Name -notmatch "_test\.go$" }
foreach ($file in $pluginFiles) {
    $fc = Get-Content $file.FullName -Raw
    if ($fc -match '"tiancode/app|"tiancode/main|"tcode/app|"tcode/main') {
        Write-Host "❌ [R5] $($file.Name) 依赖宿主层，违反单向依赖规则" -ForegroundColor Red
        $errors++
    }
}

# 规则 6: Rail 钩子必须在 ExecutionEngine 中被调用
$engineGo = Get-ChildItem -Path "internal\core\loop" -Filter "*.go" | Where-Object { $_.Name -notmatch "_test\.go$" } | ForEach-Object { Get-Content $_.FullName -Raw } | Out-String
if ($engineGo -notmatch 'OnBeforeAct') {
    Write-Host "❌ [R6] engine 缺少 Rail.OnBeforeAct() 调用" -ForegroundColor Red
    $errors++
}
if ($engineGo -notmatch 'OnAfterAct') {
    Write-Host "❌ [R6] engine 缺少 Rail.OnAfterAct() 调用" -ForegroundColor Red
    $errors++
}

# 规则 7: 宿主层 (app.go / app_chat.go) 禁止内含上下文窗口构建逻辑
# 该逻辑已收归 internal/core/memory，宿主只应调用 memory.BuildConversationWindow（对应 T2）
$hostFiles = @("app.go", "app_chat.go")
foreach ($hf in $hostFiles) {
    if (Test-Path $hf) {
        $hc = Get-Content $hf -Raw
        if ($hc -match 'pruneHistoricalOutput|func buildConversationWindow') {
            Write-Host "❌ [R7] $hf 内含上下文窗口构建逻辑，必须收归 internal/core/memory" -ForegroundColor Red
            $errors++
        }
    }
}
if (-not (Test-Path "internal\core\memory\window.go")) {
    Write-Host "❌ [R7] 缺少 internal/core/memory/window.go，上下文窗口逻辑无处安放" -ForegroundColor Red
    $errors++
} else {
    $mc = Get-Content "internal\core\memory\window.go" -Raw
    if ($mc -notmatch 'func BuildConversationWindow') {
        Write-Host "❌ [R7] memory/window.go 未定义 BuildConversationWindow" -ForegroundColor Red
        $errors++
    }
}

# 规则 8: 工具注册必须经由 registerWorkspaceTools 单一入口，禁止 NewApp/SetWorkspace 各自重复注册（对应 T0）
foreach ($tc in @('gittool.NewTool', 'fstool.NewTool', 'terminaltool.NewTool', 'searchtool.NewTool', 'archtool.NewTool')) {
    $count = ([regex]::Matches($appContent, '(?<![a-zA-Z])' + [regex]::Escape($tc))).Count
    if ($count -gt 1) {
        Write-Host "❌ [R8] $tc 在 app.go 出现 $count 次，工具注册应集中在 registerWorkspaceTools 单一入口" -ForegroundColor Red
        $errors++
    }
}
if ($appContent -notmatch 'func registerWorkspaceTools') {
    Write-Host "❌ [R8] 缺少 registerWorkspaceTools 单一工具注册入口" -ForegroundColor Red
    $errors++
}

Write-Host ""
if ($errors -gt 0) {
    Write-Host "❌ 架构守卫失败（$errors 项违规）| 阅读 AGENTS.md 铁律7 和 docs/architecture/PLUGIN_ARCH.md" -ForegroundColor Red
    exit 1
} else {
    Write-Host "✅ 架构守卫通过（热插拔插件架构合规）" -ForegroundColor Green
    exit 0
}
