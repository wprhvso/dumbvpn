#Requires -Version 5.1
<#
Uninstalls the DumbVPN Windows service and removes everything install.ps1 put on disk.

Usage (PowerShell, as any user):
    irm https://raw.githubusercontent.com/wprhvso/dumbvpn/main/uninstall.ps1 | iex
#>

$ErrorActionPreference = 'Stop'

$InstallDir = 'C:\Program Files\DumbVPN'
$SrcDir     = Join-Path $env:LOCALAPPDATA 'DumbVPN\src'
$RawUrl     = 'https://raw.githubusercontent.com/wprhvso/dumbvpn/main/uninstall.ps1'

function Write-Step {
    param([string]$Message)
    Write-Host "==> $Message" -ForegroundColor Cyan
}

# --- Self-elevate to Administrator (needed to stop/remove the service) ---
$currentPrincipal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isAdmin = $currentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
    Write-Step 'Requesting Administrator privileges...'

    if ($PSCommandPath) {
        $inner = "& '$PSCommandPath'"
    } else {
        $inner = "irm '$RawUrl' | iex"
    }

    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($inner))
    $proc = Start-Process -FilePath 'powershell.exe' -Verb RunAs -PassThru -Wait `
        -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', $encoded)
    exit $proc.ExitCode
}

$installedExe = Join-Path $InstallDir 'dumbvpn.exe'

if (Get-Service -Name 'DumbVPN' -ErrorAction SilentlyContinue) {
    Write-Step 'Removing DumbVPN service...'
    if (Test-Path $installedExe) {
        & $installedExe uninstall | Out-Host
    } else {
        Stop-Service -Name 'DumbVPN' -Force -ErrorAction SilentlyContinue
        sc.exe delete DumbVPN | Out-Null
    }
} else {
    Write-Step 'DumbVPN service not found, skipping service removal.'
}

if (Test-Path $InstallDir) {
    Write-Step "Removing $InstallDir..."
    Remove-Item -Path $InstallDir -Recurse -Force
}

if (Test-Path $SrcDir) {
    Write-Step "Removing cloned source at $SrcDir..."
    Remove-Item -Path $SrcDir -Recurse -Force
}

Write-Host 'DumbVPN has been uninstalled.'
