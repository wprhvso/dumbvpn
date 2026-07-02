#Requires -Version 5.1
$ErrorActionPreference = 'Stop'

$InstallDir = 'C:\Program Files\DumbVPN'
$RawUrl     = 'https://raw.githubusercontent.com/wprhvso/dumbvpn/main/uninstall.ps1'

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
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

$exePath = Join-Path $InstallDir 'dumbvpn.exe'

if (Get-Service -Name 'DumbVPN' -ErrorAction SilentlyContinue) {
    if (Test-Path $exePath) {
        & $exePath uninstall | Out-Null
    } else {
        Stop-Service -Name 'DumbVPN' -Force -ErrorAction SilentlyContinue
        sc.exe delete DumbVPN | Out-Null
    }
}

if (Test-Path $InstallDir) {
    Remove-Item -Path $InstallDir -Recurse -Force
}

Write-Host 'DumbVPN has been uninstalled.'
