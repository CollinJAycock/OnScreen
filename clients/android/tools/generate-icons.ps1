# Generate Android launcher + TV-banner assets from drawable-nodpi/ic_logo.png.
#
# What it produces (idempotent — overwrites previous output):
#
#   res/mipmap-anydpi-v26/ic_launcher.xml       — adaptive icon (already exists, left alone)
#   res/mipmap-anydpi-v26/ic_launcher_round.xml — round adaptive icon
#   res/drawable/ic_launcher_foreground.xml     — bitmap wrapper around foreground PNG
#   res/drawable-mdpi/ic_launcher_foreground.png  (108×108 px)
#   res/drawable-hdpi/ic_launcher_foreground.png  (162×162 px)
#   res/drawable-xhdpi/ic_launcher_foreground.png (216×216 px)
#   res/drawable-xxhdpi/ic_launcher_foreground.png (324×324 px)
#   res/drawable-xxxhdpi/ic_launcher_foreground.png (432×432 px)
#   res/mipmap-{mdpi..xxxhdpi}/ic_launcher.png      legacy square icons
#   res/mipmap-{mdpi..xxxhdpi}/ic_launcher_round.png legacy round icons (circle-masked)
#   res/drawable-xhdpi/banner.png                   Android TV launcher banner (320×180)
#   res/drawable-xxxhdpi/banner.png                 Android TV launcher banner (1280×720)
#
# The adaptive icon foreground centers the source artwork at ~70% scale on a
# transparent canvas so the system's circle/squircle/rounded-square mask
# never crops the silhouette. Background is the bg_primary color (already
# defined in colors.xml) — solid dark fits the existing artwork's letterboxing.
#
# Run from anywhere; paths are computed relative to this script.

Add-Type -AssemblyName System.Drawing

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ResDir    = Resolve-Path "$ScriptDir\..\app\src\main\res"
$Source    = "$ResDir\drawable-nodpi\ic_logo.png"

if (-not (Test-Path $Source)) {
    throw "Source logo not found: $Source"
}

function New-Dir($path) {
    if (-not (Test-Path $path)) { New-Item -ItemType Directory -Path $path -Force | Out-Null }
}

# Render the source logo onto a transparent square canvas, scaled to fit the
# given fraction of canvas size. fraction=0.70 leaves ~15% margin per side so
# the system's adaptive-icon mask (worst case: 72/108 visible = 66.7%) never
# clips the artwork. fraction=1.0 fills (used for legacy square icon).
function Render-Centered([string]$srcPath, [int]$canvasSize, [double]$fraction, [string]$dstPath, [System.Drawing.Color]$bgColor) {
    $src = [System.Drawing.Image]::FromFile($srcPath)
    $bmp = New-Object System.Drawing.Bitmap($canvasSize, $canvasSize)
    $g   = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode     = 'AntiAlias'
    $g.InterpolationMode = 'HighQualityBicubic'
    $g.PixelOffsetMode   = 'HighQuality'
    $g.Clear($bgColor)

    $artSize = [int]($canvasSize * $fraction)
    $offset  = [int](($canvasSize - $artSize) / 2)
    $g.DrawImage($src, $offset, $offset, $artSize, $artSize)

    $g.Dispose()
    New-Dir (Split-Path -Parent $dstPath)
    $bmp.Save($dstPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose(); $src.Dispose()
}

# Render-Centered + circular mask. Used for legacy round launcher icons on
# devices that pull mipmap-…/ic_launcher_round.png pre-API-26.
function Render-Round([string]$srcPath, [int]$canvasSize, [string]$dstPath, [System.Drawing.Color]$bgColor) {
    $src = [System.Drawing.Image]::FromFile($srcPath)
    $bmp = New-Object System.Drawing.Bitmap($canvasSize, $canvasSize)
    $g   = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode     = 'AntiAlias'
    $g.InterpolationMode = 'HighQualityBicubic'
    $g.PixelOffsetMode   = 'HighQuality'
    $g.Clear([System.Drawing.Color]::Transparent)

    # Clip to a circle inscribed in the canvas, then paint the bg + scaled art.
    $path = New-Object System.Drawing.Drawing2D.GraphicsPath
    $path.AddEllipse(0, 0, $canvasSize, $canvasSize)
    $g.SetClip($path)

    $brush = New-Object System.Drawing.SolidBrush($bgColor)
    $g.FillRectangle($brush, 0, 0, $canvasSize, $canvasSize)
    $brush.Dispose()

    $artSize = [int]($canvasSize * 0.78)
    $offset  = [int](($canvasSize - $artSize) / 2)
    $g.DrawImage($src, $offset, $offset, $artSize, $artSize)

    $g.Dispose()
    New-Dir (Split-Path -Parent $dstPath)
    $bmp.Save($dstPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose(); $src.Dispose()
}

# 16:9 banner used by the Leanback launcher on Android TV / Fire TV. The
# launcher card defaults to landscape — bg color fills the rest, art sits
# left-of-center with the app name to its right.
#
# Font size is autoshrunk until "OnScreen" fits on one line in the
# remaining horizontal space. Hard-coding a size produced wraps at
# 1280×720; this loop guarantees a single-line render at any banner
# dimension the launcher might request.
function Render-Banner([string]$srcPath, [int]$w, [int]$h, [string]$dstPath, [System.Drawing.Color]$bgColor, [string]$title) {
    $src = [System.Drawing.Image]::FromFile($srcPath)
    $bmp = New-Object System.Drawing.Bitmap($w, $h)
    $g   = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode     = 'AntiAlias'
    $g.InterpolationMode = 'HighQualityBicubic'
    $g.PixelOffsetMode   = 'HighQuality'
    $g.TextRenderingHint = 'AntiAliasGridFit'
    $g.Clear($bgColor)

    # Logo on the left at 70% of banner height — leaves room on the
    # right for the wordmark without crowding either element.
    $artH = [int]($h * 0.70)
    $artW = $artH
    $artX = [int]($h * 0.08)
    $artY = [int](($h - $artH) / 2)
    $g.DrawImage($src, $artX, $artY, $artW, $artH)

    # Reserve the band right of the logo for "OnScreen". Auto-fit the
    # font so the wordmark renders on one line — start big and shrink
    # until the measured width fits the available band with margin.
    $textLeft   = $artX + $artW + [int]($h * 0.06)
    $textRight  = $w - [int]($h * 0.06)
    $textWidth  = $textRight - $textLeft
    $fontSize   = [int]($h * 0.45)
    $font       = $null
    while ($fontSize -gt 16) {
        $candidate = New-Object System.Drawing.Font('Segoe UI', $fontSize, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
        $measured  = $g.MeasureString($title, $candidate).Width
        if ($measured -le $textWidth) {
            $font = $candidate
            break
        }
        $candidate.Dispose()
        $fontSize = [int]($fontSize * 0.9)
    }
    if ($font -eq $null) {
        $font = New-Object System.Drawing.Font('Segoe UI', 16, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
    }

    $textBrush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::White)
    $textRect  = New-Object System.Drawing.RectangleF($textLeft, 0, $textWidth, $h)
    $sf        = New-Object System.Drawing.StringFormat
    $sf.Alignment     = 'Near'
    $sf.LineAlignment = 'Center'
    $sf.FormatFlags   = [System.Drawing.StringFormatFlags]::NoWrap
    $g.DrawString($title, $font, $textBrush, $textRect, $sf)

    $textBrush.Dispose(); $font.Dispose(); $sf.Dispose(); $g.Dispose()
    New-Dir (Split-Path -Parent $dstPath)
    $bmp.Save($dstPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose(); $src.Dispose()
}

# bg_primary in res/values/colors.xml is the OnScreen brand dark — match it
# exactly so the foreground PNG and the adaptive-icon background drawable
# meet at the same color regardless of which launcher path is taken.
$bg = [System.Drawing.ColorTranslator]::FromHtml('#07070d')

# ── Adaptive icon foreground: 5 densities × 108dp ───────────────────────────
# 108dp at mdpi=108, hdpi=162, xhdpi=216, xxhdpi=324, xxxhdpi=432.
# Foreground keeps a transparent background — the system layers it over the
# bg drawable referenced in mipmap-anydpi-v26/ic_launcher.xml.
$fgSizes = @{
    'drawable-mdpi'    = 108
    'drawable-hdpi'    = 162
    'drawable-xhdpi'   = 216
    'drawable-xxhdpi'  = 324
    'drawable-xxxhdpi' = 432
}
foreach ($dir in $fgSizes.Keys) {
    $size = $fgSizes[$dir]
    Render-Centered $Source $size 0.70 "$ResDir\$dir\ic_launcher_foreground.png" ([System.Drawing.Color]::Transparent)
    Write-Host "wrote $dir/ic_launcher_foreground.png ($size×$size)"
}

# ── Legacy launcher icons: 5 densities, square + round ──────────────────────
# Used pre-API-26 and on launchers that don't honor the adaptive icon
# (notably some smart-TV launchers on older Fire OS builds).
$legacySizes = @{
    'mipmap-mdpi'    = 48
    'mipmap-hdpi'    = 72
    'mipmap-xhdpi'   = 96
    'mipmap-xxhdpi'  = 144
    'mipmap-xxxhdpi' = 192
}
foreach ($dir in $legacySizes.Keys) {
    $size = $legacySizes[$dir]
    Render-Centered $Source $size 0.84 "$ResDir\$dir\ic_launcher.png" $bg
    Render-Round    $Source $size       "$ResDir\$dir\ic_launcher_round.png" $bg
    Write-Host "wrote $dir/{ic_launcher,ic_launcher_round}.png ($size×$size)"
}

# ── Android TV / Fire TV banner ─────────────────────────────────────────────
# 320×180 dp is the spec minimum; render at 1280×720 (xxxhdpi-equivalent)
# into the existing nodpi slot the manifest already points at. Single
# asset is enough — Leanback scales it, and bundling per-density variants
# would just bloat the APK with redundant images.
Render-Banner $Source 1280 720 "$ResDir\drawable-nodpi\ic_banner_art.png" $bg "OnScreen"
Write-Host "wrote drawable-nodpi/ic_banner_art.png (1280×720)"

Write-Host "`nDone. Manifest already references @drawable/ic_banner -> wraps ic_banner_art."
