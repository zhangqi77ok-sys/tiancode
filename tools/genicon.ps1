# 一次性工具：用 GDI+ 生成 tiancode 应用图标（标准 ICO 格式，BMP 帧）。
# 样式：亮紫圆角方块（squircle 风格，四周透明）+ 白色粗 T，居中。
Add-Type -AssemblyName System.Drawing

$size = 256
$bmp = New-Object System.Drawing.Bitmap($size, $size)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
$g.Clear([System.Drawing.Color]::Transparent)   # 形状之外透明

function RoundRect([float]$x, [float]$y, [float]$w, [float]$h, [float]$r) {
  $path = New-Object System.Drawing.Drawing2D.GraphicsPath
  $d = $r * 2
  $path.AddArc($x, $y, $d, $d, 180, 90)
  $path.AddArc($x + $w - $d, $y, $d, $d, 270, 90)
  $path.AddArc($x + $w - $d, $y + $h - $d, $d, $d, 0, 90)
  $path.AddArc($x, $y + $h - $d, $d, $d, 90, 90)
  $path.CloseFigure()
  return $path
}

# 图标形状：圆角方块（inset 10，圆角 64——接近 iOS squircle 观感）
$inset = 10
$shape = RoundRect $inset $inset ($size - 2 * $inset) ($size - 2 * $inset) 64

# 亮紫渐变（#6C4FF7 → #5B3FE8，轻微垂直渐变）
$c1 = [System.Drawing.Color]::FromArgb(255, 0x6c, 0x4f, 0xf7)
$c2 = [System.Drawing.Color]::FromArgb(255, 0x5b, 0x3f, 0xe8)
$bounds = [System.Drawing.RectangleF]::FromLTRB($inset, $inset, $size - $inset, $size - $inset)
$grad = New-Object System.Drawing.Drawing2D.LinearGradientBrush($bounds, $c1, $c2, 90.0)
$g.FillPath($grad, $shape)

# 白色粗 "T"：横杠 + 竖杠，整体居中（形状中心 128）
$white = [System.Drawing.Brushes]::White
$g.FillRectangle($white, 62, 72, 132, 40)    # 横杠
$g.FillRectangle($white, 109, 72, 38, 124)   # 竖杠
$g.Dispose()

$hIcon = $bmp.GetHicon()
$icon = [System.Drawing.Icon]::FromHandle($hIcon)
$dir = Join-Path (Get-Location) 'build\windows'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$fs = [System.IO.File]::Create((Join-Path $dir 'icon.ico'))
$icon.Save($fs)
$fs.Close()
Write-Host ("icon written: " + (Get-Item (Join-Path $dir 'icon.ico')).Length + " bytes")
