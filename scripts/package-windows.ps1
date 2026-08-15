# packaging script (Windows): assemble the Windows release zip.
# Usage: powershell -File scripts\package-windows.ps1 [-Naive <naive.exe path>]
param(
  [string]$Naive = ""   # patched kernel (bin\naiveproxy\naive.exe); optional until CI builds it
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$out = Join-Path $root "release-windows"
Remove-Item $out -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $out | Out-Null

$env:GOPROXY = "https://goproxy.cn,direct"; $env:GOSUMDB = "sum.golang.google.cn"
Push-Location (Join-Path $root "tui")
go build -trimpath -o (Join-Path $out "naivereal-tui.exe") .
Pop-Location
Push-Location (Join-Path $root "frontend")
go build -trimpath -o (Join-Path $out "naivereal-frontend.exe") .
Pop-Location

if ($Naive -ne "" -and (Test-Path $Naive)) {
  Copy-Item $Naive (Join-Path $out "naive.exe")
} else {
  Write-Host "NOTE: patched kernel not provided; copy naive.exe into the zip yourself"
}

Copy-Item (Join-Path $root "LICENSE-Go") $out
Copy-Item (Join-Path $root "NOTICE.md") $out
Copy-Item (Join-Path $root "README.md") $out
New-Item -ItemType Directory -Force -Path (Join-Path $out "docs") | Out-Null
Copy-Item (Join-Path $root "docs\windows.md") (Join-Path $out "docs")
Copy-Item (Join-Path $root "docs\v2rayN.md") (Join-Path $out "docs")
Copy-Item (Join-Path $root "docs\tun.md") (Join-Path $out "docs")

Compress-Archive -Path (Join-Path $out "*") -DestinationPath (Join-Path $root "naivereal-windows-x64.zip") -Force
Write-Host "release: $(Join-Path $root 'naivereal-windows-x64.zip')"