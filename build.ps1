# Builds dist\SilverSpoon: the exe, the bundled 7-Zip and (if found) the
# Playwright Chromium the CAPTCHA solver prefers, like build_exe.bat did for
# the Python app.
#
#   .\build.ps1                 # build
#   .\build.ps1 -Repo owner/name  # also bake in the GitHub repo updates come from
param([string]$Repo = "", [string]$Version = "v2.0.0")
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

Get-Process -Name SilverSpoon -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$PSScriptRoot*" } | Stop-Process -Force

# Optional: embed the icon and manifest (go install github.com/tc-hib/go-winres@latest).
if (Get-Command go-winres -ErrorAction SilentlyContinue) {
    go-winres simply --icon assets\SilverSpoon.png --manifest gui --product-name SilverSpoon `
        --file-version ($Version.TrimStart('v') + ".0") --product-version $Version.TrimStart('v') --out cmd\silverspoon\rsrc
} else {
    Write-Host "go-winres not found: building without the exe icon."
}

$ldflags = "-s -w -H windowsgui -X silverspoon/internal/engine.Version=$Version"
if ($Repo) { $ldflags += " -X silverspoon/internal/update.ReleaseRepo=$Repo" }

$dist = "dist\SilverSpoon"
if (Test-Path $dist) { Remove-Item $dist -Recurse -Force }
New-Item -ItemType Directory -Force $dist | Out-Null
go build -trimpath -ldflags $ldflags -o "$dist\SilverSpoon.exe" .\cmd\silverspoon
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
Copy-Item assets\7z.exe, assets\7z.dll $dist

# Bundle Playwright's Chromium when installed (python -m playwright install chromium).
$pw = Get-ChildItem (Join-Path $env:LOCALAPPDATA 'ms-playwright') -Directory -Filter 'chromium-*' -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -notlike 'chromium_headless*' } | Sort-Object Name -Descending |
    ForEach-Object { Join-Path $_.FullName 'chrome-win64' } | Where-Object { Test-Path (Join-Path $_ 'chrome.exe') } | Select-Object -First 1
if ($pw) {
    $dest = "$dist\playwright-chromium"
    Copy-Item $pw $dest -Recurse
    Get-ChildItem (Join-Path $dest 'locales') -Filter '*.pak' | Where-Object { $_.Name -notmatch '^(en-US|en-GB)\.pak$' } | Remove-Item -Force
    'setup.exe', 'elevated_tracing_service.exe', 'elevation_service.exe', 'notification_helper.exe', 'chrome_pwa_launcher.exe' |
        ForEach-Object { $p = Join-Path $dest $_; if (Test-Path $p) { Remove-Item $p -Force } }
} else {
    Write-Host "Playwright Chromium not found: the solver will use an installed Chrome/Edge/Brave."
}

Compress-Archive -Path "$dist" -DestinationPath "dist\SilverSpoon-$Version-windows.zip" -Force
Write-Host "Built $dist and dist\SilverSpoon-$Version-windows.zip"
