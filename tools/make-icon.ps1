# Draws the DSH Desktop icon bitmaps as PNGs (icon-16/32/48/256.png) plus
# icon.png (256px, design source for go-winres). The .ico assembly is done by
# tools/png2ico (DIB entries, universally compatible).
$ErrorActionPreference = "Stop"

try { Add-Type -AssemblyName System.Drawing -ErrorAction Stop }
catch { Add-Type -AssemblyName System.Drawing.Common -ErrorAction Stop }

function New-IconBitmap([int]$size, [string]$glyph, [float]$glyphSize) {
    $bmp = New-Object System.Drawing.Bitmap $size, $size, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $g.TextRenderingHint = [System.Drawing.Text.TextRenderingHint]::AntiAliasGridFit
    $g.Clear([System.Drawing.Color]::Transparent)

    $margin = [Math]::Max(1, [int]($size * 0.06))
    $rect = New-Object System.Drawing.Rectangle $margin, $margin, ($size - 2 * $margin), ($size - 2 * $margin)
    $radius = [int]($size * 0.22)
    $path = New-Object System.Drawing.Drawing2D.GraphicsPath
    $d = $radius * 2
    $path.AddArc($rect.X, $rect.Y, $d, $d, 180, 90)
    $path.AddArc($rect.Right - $d, $rect.Y, $d, $d, 270, 90)
    $path.AddArc($rect.Right - $d, $rect.Bottom - $d, $d, $d, 0, 90)
    $path.AddArc($rect.X, $rect.Bottom - $d, $d, $d, 90, 90)
    $path.CloseFigure()

    $c1 = [System.Drawing.Color]::FromArgb(255, 59, 130, 246)   # #3B82F6
    $c2 = [System.Drawing.Color]::FromArgb(255, 15, 23, 42)     # #0F172A
    $brush = New-Object System.Drawing.Drawing2D.LinearGradientBrush($rect, $c1, $c2, 90.0)
    $g.FillPath($brush, $path)

    $fontSize = [float]($size * $glyphSize)
    $font = New-Object System.Drawing.Font("Segoe UI", $fontSize, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
    $sf = New-Object System.Drawing.StringFormat
    $sf.Alignment = [System.Drawing.StringAlignment]::Center
    $sf.LineAlignment = [System.Drawing.StringAlignment]::Center
    $g.DrawString($glyph, $font, [System.Drawing.Brushes]::White,
        (New-Object System.Drawing.RectangleF($rect.X, $rect.Y, $rect.Width, $rect.Height)), $sf)
    $font.Dispose(); $sf.Dispose(); $brush.Dispose(); $path.Dispose()
    $g.Dispose()
    return $bmp
}

$outDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$outDir = Split-Path -Parent $outDir   # tools/ -> project root

$sizes = @(
    @{ Size = 256; Glyph = "DSH"; GlyphSize = 0.22 },
    @{ Size = 48;  Glyph = "D";   GlyphSize = 0.55 },
    @{ Size = 32;  Glyph = "D";   GlyphSize = 0.55 },
    @{ Size = 16;  Glyph = "D";   GlyphSize = 0.55 }
)

foreach ($s in $sizes) {
    $bmp = New-IconBitmap $s.Size $s.Glyph $s.GlyphSize
    $bmp.Save((Join-Path $outDir "icon-$($s.Size).png"), [System.Drawing.Imaging.ImageFormat]::Png)
    if ($s.Size -eq 256) {
        Copy-Item (Join-Path $outDir "icon-256.png") (Join-Path $outDir "icon.png") -Force
    }
    $bmp.Dispose()
}
Write-Host "Drew icon-{16,32,48,256}.png + icon.png in $outDir"
