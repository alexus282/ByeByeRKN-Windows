# Builds dist\ByeByeRKN.exe and puts wintun.dll next to it, where the Wintun loader looks for it.
$ErrorActionPreference = 'Stop'
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) { $go = 'C:\android\go\bin\go.exe' }
if (-not (Test-Path $go)) { throw "Go is not installed. Put go.exe in PATH or set C:\android\go\bin\go.exe." }

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root
$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force $dist | Out-Null

# The Windows port is released independently of the Android app; its version lives in VERSION.
$version = '0.1.0'
$versionFile = Join-Path $root 'VERSION'
if (Test-Path $versionFile) { $version = (Get-Content $versionFile -Raw).Trim() }

# windowsgui: this is a windowed app, not a console one.
& $go build -trimpath -ldflags "-H windowsgui -s -w -X github.com/alexus282/ByeByeRKN-Windows/ui.Version=$version" -o (Join-Path $dist 'ByeByeRKN.exe') .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
# A running app keeps the DLL loaded; it never changes, so the copy already next to the exe is fine.
$dll = Join-Path $dist 'wintun.dll'
try {
    Copy-Item -Force (Join-Path $root 'wintun\amd64\wintun.dll') $dll -ErrorAction Stop
} catch {
    if (-not (Test-Path $dll)) { throw }
    Write-Warning "wintun.dll is in use by a running ByeByeRKN; kept the existing copy"
}
# The one-file installer carries the app and the DLL, then runs the app's own --install.
$setupDir = Join-Path $root 'cmd\setup'
$payload = Join-Path $setupDir 'payload'
New-Item -ItemType Directory -Force $payload | Out-Null
Copy-Item -Force (Join-Path $dist 'ByeByeRKN.exe') $payload
Copy-Item -Force $dll $payload
# Icon only: Setup elevates itself at start, and a requireAdministrator manifest would also make
# `go test` demand elevation for the installer's own tests.
& $go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico (Join-Path $root 'winres\app.ico') -o (Join-Path $setupDir 'rsrc_windows_amd64.syso')
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& $go build -trimpath -ldflags "-H windowsgui -s -w -X main.version=$version" -o (Join-Path $dist 'ByeByeRKN-Setup.exe') ./cmd/setup
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Built $dist\ByeByeRKN.exe"
Write-Host "Built $dist\ByeByeRKN-Setup.exe"
