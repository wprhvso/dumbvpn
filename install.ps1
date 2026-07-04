#Requires -Version 5.1
param(
    [string]$Version = 'latest'
)
$ErrorActionPreference = 'Stop'

$RepoOwner  = 'wprhvso'
$RepoName   = 'dumbvpn'
$InstallDir = 'C:\Program Files\DumbVPN'
$RawUrl     = "https://raw.githubusercontent.com/$RepoOwner/$RepoName/main/install.ps1"
$TokenPath  = Join-Path $env:LOCALAPPDATA 'DumbVPN\github-token.xml'

function Get-GithubToken {
    if (Test-Path $TokenPath) {
        return (Import-Clixml -Path $TokenPath).GetNetworkCredential().Password
    }
    $secure = Read-Host -Prompt 'GitHub Personal Access Token (repo read access)' -AsSecureString
    $credential = New-Object System.Management.Automation.PSCredential('github', $secure)
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $TokenPath) | Out-Null
    $credential | Export-Clixml -Path $TokenPath
    $credential.GetNetworkCredential().Password
}

$Token = Get-GithubToken

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    if ($PSCommandPath) {
        $inner = "& '$PSCommandPath' -Version '$Version'"
    } else {
        $inner = "& ([scriptblock]::Create((irm '$RawUrl'))) -Version '$Version'"
    }
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($inner))
    $proc = Start-Process -FilePath 'powershell.exe' -Verb RunAs -PassThru -Wait `
        -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', $encoded)
    exit $proc.ExitCode
}

$headers = @{ Authorization = "token $Token"; Accept = 'application/vnd.github+json' }
$releaseUrl = if ($Version -eq 'latest') {
    "https://api.github.com/repos/$RepoOwner/$RepoName/releases/latest"
} else {
    "https://api.github.com/repos/$RepoOwner/$RepoName/releases/tags/$Version"
}
$release = Invoke-RestMethod -Headers $headers -Uri $releaseUrl

function Get-ReleaseAsset {
    param([string]$Name, [string]$Dest)
    $asset = $release.assets | Where-Object { $_.name -eq $Name }
    if (-not $asset) {
        Write-Host "ERROR: asset '$Name' not found in release $($release.tag_name)." -ForegroundColor Red
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
Write-Host "DumbVPN $($release.tag_name) is installed at $InstallDir and running as a Windows service."
Write-Host 'Uninstall with uninstall.ps1'
