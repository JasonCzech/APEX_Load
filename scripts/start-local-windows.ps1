#requires -Version 5.1
<#
.SYNOPSIS
Starts APEX Load locally on a Windows PC.

.DESCRIPTION
Starts the Go controller and, by default, the Vite dashboard dev server in
separate PowerShell windows. The utility can also prepare the Windows 11
per-user proxy settings for browser recording and restore the previous proxy
state when the utility exits.

.EXAMPLE
.\scripts\start-local-windows.ps1

Starts the controller on 127.0.0.1:8080, starts the dashboard dev server on
127.0.0.1:3000, and opens the dashboard.

.EXAMPLE
.\scripts\start-local-windows.ps1 -ConfigureRecorderProxy -DashboardRoute '#/scripts?mode=create'

Starts the local dev stack, sets the Windows manual proxy to the APEX Load
recorder proxy, opens the recorder authoring route, and restores the previous
proxy settings when you press Enter to stop.

.EXAMPLE
.\scripts\start-local-windows.ps1 -Mode Embedded -BuildDashboard

Builds the dashboard into pkg/api/static and starts only the controller, which
serves the embedded dashboard from 127.0.0.1:8080.

.EXAMPLE
.\scripts\start-local-windows.ps1 -RestoreRecorderProxy

Restores the saved Windows recorder proxy state without starting the app.
#>

[CmdletBinding()]
param(
  [ValidateSet('Dev', 'Embedded')]
  [string]$Mode = 'Dev',

  [string]$ControllerAddr = '127.0.0.1:8080',

  [string]$RecorderProxyAddr = '127.0.0.1:8081',

  [string]$DashboardHost = '127.0.0.1',

  [ValidateRange(1, 65535)]
  [int]$DashboardPort = 3000,

  [string]$DashboardRoute = '',

  [switch]$InstallDashboardDependencies,

  [switch]$BuildDashboard,

  [switch]$ConfigureRecorderProxy,

  [switch]$RestoreRecorderProxy,

  [switch]$NoOpenBrowser,

  [switch]$NoWait,

  [string]$GoCommand = 'go',

  [string]$NpmCommand = 'npm'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:RepoRoot = Split-Path -Parent $PSScriptRoot
$script:DashboardDir = Join-Path $script:RepoRoot 'dashboard'
$script:StartedProcesses = @()
try {
  $script:PowerShellPath = (Get-Process -Id $PID).Path
} catch {
  $script:PowerShellPath = 'powershell.exe'
}
$script:ProxyStateDir = Join-Path $env:TEMP 'apexload-recorder-proxy-state'
$script:WindowsProxyStatePath = Join-Path $script:ProxyStateDir 'windows-11-wininet.json'

function Quote-PowerShellString {
  param([AllowNull()][string]$Value)
  return "'" + (($Value -as [string]) -replace "'", "''") + "'"
}

function New-ExternalCommand {
  param(
    [Parameter(Mandatory = $true)][string]$Command,
    [Parameter(Mandatory = $true)][string[]]$Arguments
  )

  $parts = @('&', (Quote-PowerShellString $Command))
  foreach ($argument in $Arguments) {
    $parts += Quote-PowerShellString $argument
  }
  return $parts -join ' '
}

function Assert-CommandAvailable {
  param([Parameter(Mandatory = $true)][string]$Command)
  if (-not (Get-Command $Command -ErrorAction SilentlyContinue)) {
    throw "Required command '$Command' was not found on PATH."
  }
}

function Convert-ListenAddressToUrl {
  param([Parameter(Mandatory = $true)][string]$Address)

  $trimmed = $Address.Trim()
  if ($trimmed.StartsWith(':')) {
    return "http://127.0.0.1$trimmed"
  }

  if ($trimmed -match '^\[(.+)\]:(\d+)$') {
    $listenHost = $Matches[1]
    $port = $Matches[2]
  } else {
    $separator = $trimmed.LastIndexOf(':')
    if ($separator -lt 0) {
      return "http://127.0.0.1:$trimmed"
    }
    $listenHost = $trimmed.Substring(0, $separator)
    $port = $trimmed.Substring($separator + 1)
  }

  if ([string]::IsNullOrWhiteSpace($listenHost) -or $listenHost -eq '0.0.0.0' -or $listenHost -eq '::') {
    $listenHost = '127.0.0.1'
  }
  return "http://${listenHost}:$port"
}

function New-DashboardUrl {
  param(
    [Parameter(Mandatory = $true)][string]$BaseUrl,
    [string]$Route
  )

  $trimmedRoute = ($Route -as [string]).Trim()
  if ([string]::IsNullOrWhiteSpace($trimmedRoute)) {
    return $BaseUrl
  }
  if ($trimmedRoute.StartsWith('#')) {
    return "$BaseUrl/$trimmedRoute"
  }
  if ($trimmedRoute.StartsWith('/')) {
    return "$BaseUrl$trimmedRoute"
  }
  return "$BaseUrl/#/$trimmedRoute"
}

function Invoke-CheckedCommand {
  param(
    [Parameter(Mandatory = $true)][string]$WorkingDirectory,
    [Parameter(Mandatory = $true)][string]$Command,
    [Parameter(Mandatory = $true)][string[]]$Arguments
  )

  Push-Location $WorkingDirectory
  try {
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
      throw "'$Command $($Arguments -join ' ')' failed with exit code $LASTEXITCODE."
    }
  } finally {
    Pop-Location
  }
}

function Start-ApexLoadWindow {
  param(
    [Parameter(Mandatory = $true)][string]$Title,
    [Parameter(Mandatory = $true)][string]$WorkingDirectory,
    [Parameter(Mandatory = $true)][string]$Command,
    [hashtable]$Environment = @{}
  )

  $statements = @(
    '$Host.UI.RawUI.WindowTitle = ' + (Quote-PowerShellString $Title),
    'Set-Location -LiteralPath ' + (Quote-PowerShellString $WorkingDirectory)
  )
  foreach ($key in $Environment.Keys) {
    $statements += ('$env:{0} = {1}' -f $key, (Quote-PowerShellString ([string]$Environment[$key])))
  }
  $statements += $Command

  $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes(($statements -join '; ')))
  $process = Start-Process -FilePath $script:PowerShellPath -ArgumentList @(
    '-NoExit',
    '-NoProfile',
    '-ExecutionPolicy',
    'Bypass',
    '-EncodedCommand',
    $encoded
  ) -PassThru
  $script:StartedProcesses += $process
  Write-Host ("Started {0} in PID {1}" -f $Title, $process.Id)
}

function Stop-StartedProcesses {
  foreach ($process in $script:StartedProcesses) {
    try {
      if ($null -ne $process -and -not $process.HasExited) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
      }
    } catch {
      Write-Warning ("Failed to stop PID {0}: {1}" -f $process.Id, $_.Exception.Message)
    }
  }
}

function Get-ProxyRegistryValue {
  param([Parameter(Mandatory = $true)][string]$Name)
  $settingsPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
  try {
    return (Get-ItemProperty -Path $settingsPath -Name $Name -ErrorAction Stop).$Name
  } catch {
    return $null
  }
}

function Invoke-ProxySettingsChanged {
  $signature = '[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)] public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);'
  try {
    $native = [ApexLoad.NativeMethods]
  } catch {
    $native = Add-Type -MemberDefinition $signature -Name NativeMethods -Namespace ApexLoad -PassThru
  }
  $result = [UIntPtr]::Zero
  $null = $native::SendMessageTimeout(
    [IntPtr]0xffff,
    0x1A,
    [UIntPtr]::Zero,
    'Software\Microsoft\Windows\CurrentVersion\Internet Settings',
    0x2,
    5000,
    [ref]$result
  )
}

function Set-ApexLoadWindowsRecorderProxy {
  param([Parameter(Mandatory = $true)][string]$ProxyAddress)

  $settingsPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
  New-Item -ItemType Directory -Force -Path $script:ProxyStateDir | Out-Null
  [pscustomobject]@{
    ProxyEnable  = Get-ProxyRegistryValue 'ProxyEnable'
    ProxyServer  = Get-ProxyRegistryValue 'ProxyServer'
    ProxyOverride = Get-ProxyRegistryValue 'ProxyOverride'
    AutoConfigURL = Get-ProxyRegistryValue 'AutoConfigURL'
    AutoDetect   = Get-ProxyRegistryValue 'AutoDetect'
  } | ConvertTo-Json | Set-Content -Path $script:WindowsProxyStatePath -Encoding UTF8

  $proxyServer = 'http={0};https={0}' -f $ProxyAddress
  New-ItemProperty -Path $settingsPath -Name ProxyEnable -PropertyType DWord -Value 1 -Force | Out-Null
  New-ItemProperty -Path $settingsPath -Name ProxyServer -PropertyType String -Value $proxyServer -Force | Out-Null
  Remove-ItemProperty -Path $settingsPath -Name AutoConfigURL -ErrorAction SilentlyContinue
  New-ItemProperty -Path $settingsPath -Name AutoDetect -PropertyType DWord -Value 0 -Force | Out-Null
  Invoke-ProxySettingsChanged
  Write-Host ("Windows recorder proxy set to {0}. Previous state saved to {1}" -f $ProxyAddress, $script:WindowsProxyStatePath)
}

function Restore-ApexLoadWindowsRecorderProxy {
  $settingsPath = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
  if (-not (Test-Path $script:WindowsProxyStatePath)) {
    Write-Warning 'No saved APEX Load Windows proxy state was found.'
    return
  }

  $state = Get-Content -Path $script:WindowsProxyStatePath -Raw | ConvertFrom-Json

  function Get-SavedValue {
    param([Parameter(Mandatory = $true)][string]$Name)
    $property = $state.PSObject.Properties[$Name]
    if ($null -eq $property) {
      return $null
    }
    return $property.Value
  }

  function Has-SavedValue {
    param([Parameter(Mandatory = $true)][string]$Name)
    return $null -ne (Get-SavedValue $Name)
  }

  function Restore-StringRegistryValue {
    param([Parameter(Mandatory = $true)][string]$Name)
    $value = Get-SavedValue $Name
    if ($null -ne $value -and -not [string]::IsNullOrWhiteSpace([string]$value)) {
      New-ItemProperty -Path $settingsPath -Name $Name -PropertyType String -Value ([string]$value) -Force | Out-Null
    } else {
      Remove-ItemProperty -Path $settingsPath -Name $Name -ErrorAction SilentlyContinue
    }
  }

  function Restore-DwordRegistryValue {
    param(
      [Parameter(Mandatory = $true)][string]$Name,
      [int]$DefaultValue = 0
    )
    $value = if (Has-SavedValue $Name) { [int](Get-SavedValue $Name) } else { $DefaultValue }
    New-ItemProperty -Path $settingsPath -Name $Name -PropertyType DWord -Value $value -Force | Out-Null
  }

  Restore-DwordRegistryValue -Name ProxyEnable -DefaultValue 0
  Restore-StringRegistryValue -Name ProxyServer
  Restore-StringRegistryValue -Name ProxyOverride
  Restore-StringRegistryValue -Name AutoConfigURL
  if (Has-SavedValue 'AutoDetect') {
    Restore-DwordRegistryValue -Name AutoDetect -DefaultValue 0
  }

  Invoke-ProxySettingsChanged
  Remove-Item -Path $script:WindowsProxyStatePath -ErrorAction SilentlyContinue
  Write-Host 'Windows recorder proxy settings restored.'
}

if (-not (Test-Path (Join-Path $script:RepoRoot 'go.mod'))) {
  throw "Could not locate go.mod from $PSScriptRoot. Run this script from the APEX Load repository checkout."
}
if (-not (Test-Path (Join-Path $script:DashboardDir 'package.json'))) {
  throw "Could not locate dashboard/package.json from $PSScriptRoot."
}
if ($ConfigureRecorderProxy -and $NoWait) {
  throw '-ConfigureRecorderProxy cannot be combined with -NoWait because proxy restore requires a managed lifecycle.'
}

if ($RestoreRecorderProxy) {
  Restore-ApexLoadWindowsRecorderProxy
  return
}

Assert-CommandAvailable $GoCommand
if ($Mode -eq 'Dev' -or $BuildDashboard -or $InstallDashboardDependencies) {
  Assert-CommandAvailable $NpmCommand
}

$controllerUrl = Convert-ListenAddressToUrl $ControllerAddr
$dashboardBaseUrl = if ($Mode -eq 'Dev') {
  "http://${DashboardHost}:$DashboardPort"
} else {
  $controllerUrl
}
$dashboardUrl = New-DashboardUrl -BaseUrl $dashboardBaseUrl -Route $DashboardRoute
$controllerCommand = New-ExternalCommand -Command $GoCommand -Arguments @('run', './cmd/controller')
$dashboardCommand = New-ExternalCommand -Command $NpmCommand -Arguments @('run', 'dev', '--', '--host', $DashboardHost, '--port', ([string]$DashboardPort))
$controllerEnvironment = @{
  APEXLOAD_ADDR = $ControllerAddr
  APEXLOAD_RECORDER_PROXY_ADDR = $RecorderProxyAddr
}
$dashboardEnvironment = @{
  APEXLOAD_API_PROXY = $controllerUrl
}
$proxyConfigured = $false

try {
  if ($InstallDashboardDependencies) {
    Invoke-CheckedCommand -WorkingDirectory $script:DashboardDir -Command $NpmCommand -Arguments @('install')
  } elseif (($Mode -eq 'Dev' -or $BuildDashboard) -and -not (Test-Path (Join-Path $script:DashboardDir 'node_modules'))) {
    Write-Warning 'dashboard/node_modules was not found. Run with -InstallDashboardDependencies or run npm install in dashboard/.'
  }

  if ($BuildDashboard) {
    Invoke-CheckedCommand -WorkingDirectory $script:DashboardDir -Command $NpmCommand -Arguments @('run', 'build')
  }

  if ($ConfigureRecorderProxy) {
    Set-ApexLoadWindowsRecorderProxy -ProxyAddress $RecorderProxyAddr
    $proxyConfigured = $true
  }

  Start-ApexLoadWindow -Title 'APEX Load Controller' -WorkingDirectory $script:RepoRoot -Command $controllerCommand -Environment $controllerEnvironment
  if ($Mode -eq 'Dev') {
    Start-ApexLoadWindow -Title 'APEX Load Dashboard' -WorkingDirectory $script:DashboardDir -Command $dashboardCommand -Environment $dashboardEnvironment
  }

  Write-Host ''
  Write-Host ("Controller: {0}" -f $controllerUrl)
  Write-Host ("Dashboard:  {0}" -f $dashboardUrl)
  Write-Host ("Recorder proxy: {0}" -f $RecorderProxyAddr)

  if (-not $NoOpenBrowser) {
    Start-Process $dashboardUrl
  }

  if ($NoWait) {
    Write-Host 'Started local APEX Load processes. This utility will not stop them because -NoWait was supplied.'
    return
  }

  Write-Host ''
  Write-Host 'Press Enter here to stop local APEX Load processes.'
  if ($proxyConfigured) {
    Write-Host 'The saved Windows proxy settings will be restored on exit.'
  }
  [void](Read-Host)
} finally {
  if (-not $NoWait) {
    Stop-StartedProcesses
    if ($proxyConfigured) {
      Restore-ApexLoadWindowsRecorderProxy
    }
  }
}
