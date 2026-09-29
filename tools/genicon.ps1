# 一次性工具：用 GDI+ 生成 tiancode 应用图标（标准 ICO 格式，BMP 帧）。
# 设计与 UI 主色一致（紫罗兰渐变）+ 白色 T 字。
Add-Type -AssemblyName System.Drawing

$size = 256
$bmp = New-Object System.Drawing.Bitmap($size, $size)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias

# 背景：左上 #5f43e0 → 右下 #5138c6（UI 主色系）
$rect = New-Object System.Drawing.Rectangle(0, 0, $size, $size)
$c1 = [System.Drawing.Color]::FromArgb(255, 0x5f, 0x43, 0xe0)
$c2 = [System.Drawing.Color]::FromArgb(255, 0x51, 0x38, 0xc6)
$brush = New-Object System.Drawing.Drawing2D.LinearGradientBrush($rect, $c1, $c2, 45.0)
$g.FillRectangle($brush, $rect)

# 白色 "T"：横杠 + 竖杠（居中）
$white = [System.Drawing.Brushes]::White
$g.FillRectangle($white, 48, 74, 160, 34)    # 横杠
$g.FillRectangle($white, 111, 74, 34, 132)   # 竖杠
$g.Dispose()

$hIcon = $bmp.GetHicon()
$icon = [System.Drawing.Icon]::FromHandle($hIcon)
$dir = Join-Path (Get-Location) 'build\windows'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$fs = [System.IO.File]::Create((Join-Path $dir 'icon.ico'))
$icon.Save($fs)
$fs.Close()
Write-Host ("icon written: " + (Get-Item (Join-Path $dir 'icon.ico')).Length + " bytes")
