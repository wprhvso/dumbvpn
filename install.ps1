#Requires -Version 5.1
<#
DumbVPN installer for Windows.

Builds dumbvpn.exe from source (this is a private repo, so there are no
published release binaries) and registers it as an auto-starting Windows
service.

Usage (PowerShell, as any user):
    $env:GITHUB_TOKEN = "ghp_xxx"
    irm -Headers @{Authorization="token $($env:GITHUB_TOKEN)"} `
      https://raw.githubusercontent.com/wprhvso/dumbvpn/main/install.ps1 | iex
#>

$ErrorActionPreference = 'Stop'

$RepoOwner     = 'wprhvso'
$RepoName      = 'dumbvpn'
$RawUrl        = "https://raw.githubusercontent.com/$RepoOwner/$RepoName/main/install.ps1"
$InstallDir    = 'C:\Program Files\DumbVPN'
$SrcDir        = Join-Path $env:LOCALAPPDATA 'DumbVPN\src'
$WintunVersion = '0.14.1'
$WintunUrl     = "https://www.wintun.net/builds/wintun-$WintunVersion.zip"

function Write-Step {
    param([string]$Message)
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Write-Warn {
    param([string]$Message)
    Write-Host "!! $Message" -ForegroundColor Yellow
}

function Fail {
    param([string]$Message)
    Write-Host "ERROR: $Message" -ForegroundColor Red
    exit 1
}

function Test-CommandExists {
    param([string]$Name)
    return [bool](Get-Command $Name -ErrorAction SilentlyContinue)
}

function Invoke-Checked {
    # Native commands (git, go, winget, sc.exe...) signal failure via
    # $LASTEXITCODE, not a terminating exception -- $ErrorActionPreference
    # doesn't stop the script for those, so check explicitly.
    param(
        [Parameter(Mandatory)][scriptblock]$Command,
        [Parameter(Mandatory)][string]$FailureMessage
    )
    & $Command
    if ($LASTEXITCODE -ne 0) {
        Fail "$FailureMessage (exit code $LASTEXITCODE)"
    }
}

function Update-SessionPath {
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user    = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = @($machine, $user) -join ';'
}

$Token = $env:GITHUB_TOKEN
if (-not $Token) {
    Fail ("GITHUB_TOKEN is not set. dumbvpn is a private repo, so a GitHub " +
          "Personal Access Token with 'repo' read access is required. Run:`n" +
          "  `$env:GITHUB_TOKEN = 'ghp_xxx'`n" +
          "and re-run this installer.")
}

# --- Self-elevate to Administrator (needed for winget installs + service registration) ---
$currentPrincipal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isAdmin = $currentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
    Write-Step 'Requesting Administrator privileges...'

    # Environment variables set in this session are not reliably inherited
    # across the UAC elevation boundary, so the token is re-injected by value.
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

Write-Step 'Running as Administrator.'

# --- Prerequisites: Git + Go, installed via winget if missing ---
$haveGit = Test-CommandExists 'git'
$haveGo  = Test-CommandExists 'go'

if ((-not $haveGit -or -not $haveGo) -and -not (Test-CommandExists 'winget')) {
    Fail ('winget is not available and Git/Go are missing. Install Git ' +
          '(https://git-scm.com) and Go (https://go.dev/dl) manually, then re-run this installer.')
}

if (-not $haveGit) {
    Write-Step 'Installing Git...'
    winget install --id Git.Git -e --source winget --accept-package-agreements --accept-source-agreements | Out-Host
    Update-SessionPath
    if (-not (Test-CommandExists 'git')) {
        Fail 'Git installation did not complete successfully. Open a new terminal and re-run the installer.'
    }
}

if (-not $haveGo) {
    Write-Step 'Installing Go...'
    winget install --id GoLang.Go -e --source winget --accept-package-agreements --accept-source-agreements | Out-Host
    Update-SessionPath
    if (-not (Test-CommandExists 'go')) {
        Fail 'Go installation did not complete successfully. Open a new terminal and re-run the installer.'
    }
}

# --- Credential injection for git (used both for cloning dumbvpn and for the
#     private github.com/wprhvso/gost-x replace dependency that `go build`
#     fetches directly, since it can't be served by the public module proxy).
#     Set via env vars scoped to this process only -- nothing touches disk. ---
$env:GIT_CONFIG_COUNT   = '1'
$env:GIT_CONFIG_KEY_0   = "url.https://$Token@github.com/.insteadOf"
$env:GIT_CONFIG_VALUE_0 = 'https://github.com/'
$env:GOPRIVATE          = "github.com/$RepoOwner/*"

# --- Fetch/update source ---
Write-Step 'Fetching source...'
$repoUrl = "https://github.com/$RepoOwner/$RepoName.git"
New-Item -ItemType Directory -Force -Path (Split-Path $SrcDir) | Out-Null

if (Test-Path (Join-Path $SrcDir '.git')) {
    Invoke-Checked -FailureMessage 'git fetch failed -- check GITHUB_TOKEN has repo read access' `
        -Command { git -C $SrcDir fetch --depth 1 origin main }
    Invoke-Checked -FailureMessage 'git reset failed' `
        -Command { git -C $SrcDir reset --hard origin/main }
} else {
    if (Test-Path $SrcDir) { Remove-Item -Path $SrcDir -Recurse -Force }
    Invoke-Checked -FailureMessage 'git clone failed -- check GITHUB_TOKEN has repo read access' `
        -Command { git clone --depth 1 --branch main $repoUrl $SrcDir }
}

# --- Build ---
Write-Step 'Building dumbvpn.exe (this downloads Go modules on first run, may take a minute)...'
$goCoreDir = Join-Path $SrcDir 'go-core'
$builtExe  = Join-Path $goCoreDir 'dumbvpn.exe'
if (Test-Path $builtExe) { Remove-Item $builtExe -Force }

$env:CGO_ENABLED = '0'
Push-Location $goCoreDir
try {
    Invoke-Checked -FailureMessage 'go build failed' -Command { go build -o $builtExe . }
} finally {
    Pop-Location
}

if (-not (Test-Path $builtExe)) { Fail 'Build failed: dumbvpn.exe was not produced.' }

# --- Fetch the matching wintun.dll (a signed driver DLL, not buildable from Go source) ---
Write-Step 'Fetching wintun driver...'
$archMap = @{ 'AMD64' = 'amd64'; 'ARM64' = 'arm64' }
$procArch = $env:PROCESSOR_ARCHITECTURE
if (-not $archMap.ContainsKey($procArch)) {
    Fail "Unsupported architecture: $procArch (only AMD64 and ARM64 are supported)."
}
$wintunArch = $archMap[$procArch]

$tmpZip = Join-Path $env:TEMP "wintun-$WintunVersion.zip"
Invoke-WebRequest -Uri $WintunUrl -OutFile $tmpZip

$tmpExtract = Join-Path $env:TEMP "wintun-$WintunVersion-extract"
if (Test-Path $tmpExtract) { Remove-Item $tmpExtract -Recurse -Force }
Expand-Archive -Path $tmpZip -DestinationPath $tmpExtract -Force

$wintunDll = Join-Path $tmpExtract "wintun\bin\$wintunArch\wintun.dll"
if (-not (Test-Path $wintunDll)) {
    Fail "wintun.dll not found for architecture '$wintunArch' inside the downloaded archive."
}

# --- Install ---
Write-Step "Installing to $InstallDir..."
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

$installedExe = Join-Path $InstallDir 'dumbvpn.exe'
$existingService = Get-Service -Name 'DumbVPN' -ErrorAction SilentlyContinue
if ($existingService) {
    Write-Step 'Existing DumbVPN service found, removing before reinstall...'
    if (Test-Path $installedExe) {
        & $installedExe uninstall | Out-Host
        if ($LASTEXITCODE -ne 0) { Write-Warn "Previous service removal reported exit code $LASTEXITCODE, continuing anyway." }
    } else {
        Stop-Service -Name 'DumbVPN' -Force -ErrorAction SilentlyContinue
        sc.exe delete DumbVPN | Out-Null
    }
    Start-Sleep -Seconds 1
}

Copy-Item -Path $builtExe -Destination $installedExe -Force
Copy-Item -Path $wintunDll -Destination (Join-Path $InstallDir 'wintun.dll') -Force

Write-Step 'Registering and starting the DumbVPN service...'
Invoke-Checked -FailureMessage 'Service install failed' -Command { & $installedExe install | Out-Host }

Write-Step 'Done.'
Get-Service -Name 'DumbVPN' | Format-Table -AutoSize
Write-Host "DumbVPN is installed at $InstallDir and running as an auto-start Windows service."
Write-Host 'Check status any time with: Get-Service DumbVPN'
Write-Host "Uninstall with uninstall.ps1, or manually: & '$installedExe' uninstall"
