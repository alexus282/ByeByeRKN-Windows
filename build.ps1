# Builds dist\ByeByeRKN.exe and puts wintun.dll next to it, where the Wintun loader looks for it.
$ErrorActionPreference = 'Stop'
$go = 'C:\android\go\bin\go.exe'
if (-not (Test-Path $go)) { $go = 'go' }

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root
$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force $dist | Out-Null

# The Windows app shares its version with the Android one.
$version = 'dev'
$gradle = Join-Path (Split-Path -Parent $root) 'app\build.gradle.kts'
if ((Test-Path $gradle) -and ((Get-Content $gradle -Raw) -match 'versionName\s*=\s*"([^"]+)"')) { $version = $Matches[1] }

# windowsgui: this is a windowed app, not a console one.
& $go build -trimpath -ldflags "-H windowsgui -s -w -X github.com/alexus282/ByeByeRKN/windows/ui.Version=$version" -o (Join-Path $dist 'ByeByeRKN.exe') .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Copy-Item -Force (Join-Path $root 'wintun\amd64\wintun.dll') (Join-Path $dist 'wintun.dll')
Write-Host "Built $dist\ByeByeRKN.exe"
