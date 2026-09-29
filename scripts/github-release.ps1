# GitHub Releases 发布脚本：创建 release 并上传 dist 附件。
# 用法：powershell -File scripts/github-release.ps1 -Version 0.0.01 [-NotesFile path]
# 凭证来自 git credential（已存的 github.com 凭证），不落日志。
param(
  [Parameter(Mandatory = $true)][string]$Version,
  [string]$NotesFile = ''
)
$ErrorActionPreference = 'Stop'

# ---- 凭证（不回显） ----
# 为什么用临时文件 + cmd 重定向：PowerShell 5.1 管道喂原生命令 stdin 时首行
# 偶发丢失（实测 git 报 missing protocol field），文件重定向则稳定可靠。
$tmpIn = Join-Path $env:TEMP 'tiancode-cred-in.txt'
Set-Content -Path $tmpIn -Value @('protocol=https', 'host=github.com', '') -Encoding ASCII
$cred = cmd /c "git credential fill < `"$tmpIn`"" 2>&1
Remove-Item $tmpIn -Force -ErrorAction SilentlyContinue
$token = ($cred | Select-String '^password=').Line -replace '^password=', ''
if (-not $token) { throw "未取到 github.com 凭证：请先 git push 一次让凭据管理器保存 token" }
$repo = 'zhangqi77ok-sys/tiancode'
$tag = "v$Version"
$hdr = @{ Authorization = "Bearer $token"; Accept = 'application/vnd.github+json'; 'X-GitHub-Api-Version' = '2022-11-28' }

# ---- Release 说明 ----
$defaultNotes = @"
## 湉码 / tiancode v$Version

**Windows 桌面 AI 编程工作台**——单 exe、数据全在本地、模型与工具热插拔。

### 亮点
- **模型即插即用**：多渠道池（OpenAI 兼容 / Anthropic / ChatGPT 订阅）、多 Key 轮询、优先级与权重选路、行内连通性测试、故障自动切换
- **工具与扩展热插拔**：MCP 与技能在对话里让 AI 自己安装，保存即生效，无需重启
- **对话可靠**：流式分段、多会话并行、消息队列、本地账本崩溃恢复、零反馈看门狗
- **安全受控**：审批闸门按工具名确认、工作区根锁定、密钥只存本机
- **渠道级鉴权**：Bearer / 自定义请求头 / URL 参数 / 无鉴权，兼容各类中转；支持全局上游代理

### 下载
- tiancode-setup-v$Version.exe —— 安装器（推荐）：安装 + 桌面/开始菜单快捷方式 + 卸载项
- tiancode-v$Version-portable.zip —— 便携包（免安装，解压即用）

### 说明
- 数据全在 %APPDATA%\tiancode，卸载不丢会话
- 上游在海外时请在「渠道管理」配置全局代理（如 http://127.0.0.1:7897）
- 规划中：跨会话记忆、多 Agent 编排、Claude 订阅授权、Gemini/Bedrock 适配器
"@
$notes = $defaultNotes
if ($NotesFile -and (Test-Path $NotesFile)) { $notes = Get-Content $NotesFile -Raw }

# ---- 创建 Release（tag 不存在则从 main 自动创建） ----
$payload = @{
  tag_name         = $tag
  target_commitish = 'main'
  name             = "tiancode v$Version"
  body             = $notes
  draft            = $false
  prerelease       = $false
} | ConvertTo-Json
$rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases" -Method Post -Headers $hdr -ContentType 'application/json' -Body $payload
Write-Host ("release created: id=" + $rel.id + " tag=" + $rel.tag_name + " url=" + $rel.html_url)

# ---- 上传附件 ----
$assets = @("dist\tiancode-setup-v$Version.exe", "dist\tiancode-v$Version-portable.zip")
foreach ($a in $assets) {
  if (-not (Test-Path $a)) { throw "缺少产物：$a（先跑 release.ps1）" }
  $name = [System.IO.Path]::GetFileName($a)
  $uri = "https://uploads.github.com/repos/$repo/releases/$($rel.id)/assets?name=$name"
  Invoke-RestMethod -Uri $uri -Method Post -Headers @{ Authorization = "Bearer $token" } -ContentType 'application/octet-stream' -InFile $a | Out-Null
  Write-Host ("uploaded: " + $name)
}
Write-Host ("DONE: " + $rel.html_url)
