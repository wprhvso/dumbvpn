#Requires -Version 5.1
$ErrorActionPreference = 'Stop'

$RepoOwner  = 'wprhvso'
$RepoName   = 'dumbvpn'
$InstallDir = 'C:\Program Files\DumbVPN'
$RawUrl     = "https://raw.githubusercontent.com/$RepoOwner/$RepoName/main/install.ps1"

$Token = $env:GITHUB_TOKEN
if (-not $Token) {
    Write-Host 'ERROR: GITHUB_TOKEN is not set.' -ForegroundColor Red
    exit 1
}

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    if ($PSCommandPath) {
        $inner = "`$env:GITHUB_TOKEN = '$Token'; & '$PSCommandPath'"
    } else {
        $inner = "`$env:GITHUB_TOKEN = '$Token'; irm -Headers @{Authorization=`"token $Token`"} '$RawUrl' | iex"
    }
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($inner))
    $proc = Start-Process -FilePath 'powershell.exe' -Verb RunAs -PassThru -Wait `
        -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', $encoded)
    exit $proc.ExitCode
}

$headers = @{ Authorization = "token $Token"; Accept = 'application/vnd.github+json' }
$release = Invoke-RestMethod -Headers $headers -Uri "https://api.github.com/repos/$RepoOwner/$RepoName/releases/latest"

function Get-ReleaseAsset {
    param([string]$Name, [string]$Dest)
    $asset = $release.assets | Where-Object { $_.name -eq $Name }
    if (-not $asset) {
        Write-Host "ERROR: asset '$Name' not found in latest release." -ForegroundColor Red
        exit 1
    }
    Invoke-WebRequest -Headers @{ Authorization = "token $Token"; Accept = 'application/octet-stream' } -Uri $asset.url -OutFile $Dest
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$exePath = Join-Path $InstallDir 'dumbvpn.exe'

if (Get-Service -Name 'DumbVPN' -ErrorAction SilentlyContinue) {
    if (Test-Path $exePath) {
        & $exePath uninstall | Out-Null
    } else {
        Stop-Service -Name 'DumbVPN' -Force -ErrorAction SilentlyContinue
        sc.exe delete DumbVPN | Out-Null
    }
    Start-Sleep -Seconds 1
}

Get-ReleaseAsset -Name 'dumbvpn.exe' -Dest $exePath
Get-ReleaseAsset -Name 'wintun.dll' -Dest (Join-Path $InstallDir 'wintun.dll')

& $exePath install

Get-Service -Name 'DumbVPN' | Format-Table -AutoSize
Write-Host "DumbVPN is installed at $InstallDir and running as a Windows service."
Write-Host 'Uninstall with uninstall.ps1'
