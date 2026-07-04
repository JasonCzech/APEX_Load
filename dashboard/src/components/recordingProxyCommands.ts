export const DEFAULT_MACOS_NETWORK_SERVICE = 'Wi-Fi'
const DEFAULT_RECORDER_PROXY_PORT = '8081'
const DEFAULT_RECORDER_PROXY_HOST = 'localhost'

function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

function powerShellQuote(value: string) {
  return `'${value.replace(/'/g, `''`)}'`
}

function normalizeProxyPort(value: string) {
  const trimmed = value.trim()
  return /^\d+$/.test(trimmed) ? trimmed : DEFAULT_RECORDER_PROXY_PORT
}

function normalizeProxyHost(value: string) {
  const trimmed = value.trim()
  return trimmed && !/\s/.test(trimmed) ? trimmed : DEFAULT_RECORDER_PROXY_HOST
}

function hostFromAddress(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return ''
  try {
    const url = new URL(trimmed)
    return url.hostname
  } catch {
    // Fall through to host:port parsing for bare listener addresses.
  }
  if (trimmed.startsWith(':')) return ''
  if (trimmed.startsWith('[')) {
    const end = trimmed.indexOf(']')
    return end > 0 ? trimmed.slice(1, end) : ''
  }
  const colon = trimmed.indexOf(':')
  return colon >= 0 ? trimmed.slice(0, colon) : trimmed
}

export function recorderProxyHost(proxyAddr: string, dashboardOrigin: string = '') {
  const explicitHost = hostFromAddress(proxyAddr)
  if (explicitHost && !['0.0.0.0', '::'].includes(explicitHost)) {
    return explicitHost === '127.0.0.1' ? DEFAULT_RECORDER_PROXY_HOST : explicitHost
  }
  const dashboardHost = hostFromAddress(dashboardOrigin)
  if (!dashboardHost || dashboardHost === '127.0.0.1' || dashboardHost === '::1') {
    return DEFAULT_RECORDER_PROXY_HOST
  }
  return dashboardHost
}

export function recorderProxyHostIsLocal(proxyHost: string) {
  const normalized = proxyHost.trim().toLowerCase()
  return normalized === '' || normalized === 'localhost' || normalized === '127.0.0.1' || normalized === '::1'
}

export function recorderProxyPort(proxyAddr: string) {
  return normalizeProxyPort((proxyAddr || `:${DEFAULT_RECORDER_PROXY_PORT}`).split(':').pop() || DEFAULT_RECORDER_PROXY_PORT)
}

export function buildMacOSStartProxyCommand(proxyPort: string, networkService: string = DEFAULT_MACOS_NETWORK_SERVICE, proxyHost: string = DEFAULT_RECORDER_PROXY_HOST) {
  const service = networkService.trim() || DEFAULT_MACOS_NETWORK_SERVICE
  const port = normalizeProxyPort(proxyPort)
  const host = normalizeProxyHost(proxyHost)
  return [
    `SERVICE=${shellQuote(service)}`,
    `PROXY_HOST=${shellQuote(host)}`,
    `PROXY_PORT=${shellQuote(port)}`,
    `STATE_DIR="\${TMPDIR:-/tmp}/apexload-recorder-proxy-state"`,
    `STATE_KEY="$(printf '%s' "$SERVICE" | tr -c 'A-Za-z0-9._-' '_')"`,
    `mkdir -p "$STATE_DIR"`,
    `networksetup -getwebproxy "$SERVICE" > "$STATE_DIR/$STATE_KEY.http"`,
    `networksetup -getsecurewebproxy "$SERVICE" > "$STATE_DIR/$STATE_KEY.https"`,
    `networksetup -setwebproxy "$SERVICE" "$PROXY_HOST" "$PROXY_PORT"`,
    `networksetup -setsecurewebproxy "$SERVICE" "$PROXY_HOST" "$PROXY_PORT"`,
    `networksetup -setwebproxystate "$SERVICE" on`,
    `networksetup -setsecurewebproxystate "$SERVICE" on`,
  ].join('\n')
}

export function buildMacOSRestoreProxyCommand(networkService: string = DEFAULT_MACOS_NETWORK_SERVICE) {
  const service = networkService.trim() || DEFAULT_MACOS_NETWORK_SERVICE
  return [
    `SERVICE=${shellQuote(service)}`,
    `STATE_DIR="\${TMPDIR:-/tmp}/apexload-recorder-proxy-state"`,
    `STATE_KEY="$(printf '%s' "$SERVICE" | tr -c 'A-Za-z0-9._-' '_')"`,
    `restore_proxy() {`,
    `  FILE="$1"`,
    `  SET_CMD="$2"`,
    `  STATE_CMD="$3"`,
    `  ENABLED="$(awk -F': ' '/^Enabled:/ {print $2}' "$FILE" 2>/dev/null)"`,
    `  SERVER="$(awk -F': ' '/^Server:/ {print $2}' "$FILE" 2>/dev/null)"`,
    `  PORT="$(awk -F': ' '/^Port:/ {print $2}' "$FILE" 2>/dev/null)"`,
    `  if [ "$ENABLED" = "Yes" ] && [ -n "$SERVER" ] && [ "$PORT" != "0" ]; then`,
    `    networksetup "$SET_CMD" "$SERVICE" "$SERVER" "$PORT"`,
    `    networksetup "$STATE_CMD" "$SERVICE" on`,
    `  else`,
    `    networksetup "$STATE_CMD" "$SERVICE" off`,
    `  fi`,
    `}`,
    `restore_proxy "$STATE_DIR/$STATE_KEY.http" -setwebproxy -setwebproxystate`,
    `restore_proxy "$STATE_DIR/$STATE_KEY.https" -setsecurewebproxy -setsecurewebproxystate`,
    `rm -f "$STATE_DIR/$STATE_KEY.http" "$STATE_DIR/$STATE_KEY.https"`,
  ].join('\n')
}

function windowsProxyChangeNotifierCommand() {
  return [
    `function Notify-ProxySettingsChanged {`,
    `  $signature = '[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)] public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);'`,
    `  try { $native = [ApexLoad.NativeMethods] } catch { $native = Add-Type -MemberDefinition $signature -Name NativeMethods -Namespace ApexLoad -PassThru }`,
    `  $result = [UIntPtr]::Zero`,
    `  $null = $native::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings', 0x2, 5000, [ref]$result)`,
    `}`,
  ]
}

export function buildWindowsStartProxyCommand(proxyPort: string, proxyHost: string = DEFAULT_RECORDER_PROXY_HOST) {
  const port = normalizeProxyPort(proxyPort)
  const host = normalizeProxyHost(proxyHost)
  return [
    `$ErrorActionPreference = 'Stop'`,
    `$proxyHost = ${powerShellQuote(host)}`,
    `$proxyPort = ${powerShellQuote(port)}`,
    `$proxyServer = 'http={0}:{1};https={0}:{1}' -f $proxyHost, $proxyPort`,
    `$settingsPath = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings'`,
    `$stateDir = Join-Path $env:TEMP 'apexload-recorder-proxy-state'`,
    `$statePath = Join-Path $stateDir 'windows-11-wininet.json'`,
    `New-Item -ItemType Directory -Force -Path $stateDir | Out-Null`,
    `function Get-ProxyRegistryValue {`,
    `  param([string]$Name)`,
    `  try { (Get-ItemProperty -Path $settingsPath -Name $Name -ErrorAction Stop).$Name } catch { $null }`,
    `}`,
    `[pscustomobject]@{`,
    `  ProxyEnable = Get-ProxyRegistryValue 'ProxyEnable'`,
    `  ProxyServer = Get-ProxyRegistryValue 'ProxyServer'`,
    `  ProxyOverride = Get-ProxyRegistryValue 'ProxyOverride'`,
    `  AutoConfigURL = Get-ProxyRegistryValue 'AutoConfigURL'`,
    `  AutoDetect = Get-ProxyRegistryValue 'AutoDetect'`,
    `} | ConvertTo-Json | Set-Content -Path $statePath -Encoding UTF8`,
    `New-ItemProperty -Path $settingsPath -Name ProxyEnable -PropertyType DWord -Value 1 -Force | Out-Null`,
    `New-ItemProperty -Path $settingsPath -Name ProxyServer -PropertyType String -Value $proxyServer -Force | Out-Null`,
    `Remove-ItemProperty -Path $settingsPath -Name AutoConfigURL -ErrorAction SilentlyContinue`,
    `New-ItemProperty -Path $settingsPath -Name AutoDetect -PropertyType DWord -Value 0 -Force | Out-Null`,
    ...windowsProxyChangeNotifierCommand(),
    `Notify-ProxySettingsChanged`,
    `Write-Host ('APEX Load Windows proxy set to {0}:{1}. Saved previous state to {2}' -f $proxyHost, $proxyPort, $statePath)`,
  ].join('\n')
}

export function buildWindowsRestoreProxyCommand() {
  return [
    `$ErrorActionPreference = 'Stop'`,
    `$settingsPath = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings'`,
    `$stateDir = Join-Path $env:TEMP 'apexload-recorder-proxy-state'`,
    `$statePath = Join-Path $stateDir 'windows-11-wininet.json'`,
    `if (!(Test-Path $statePath)) {`,
    `  Write-Warning 'No saved APEX Load Windows proxy state was found.'`,
    `  return`,
    `}`,
    `$state = Get-Content -Path $statePath -Raw | ConvertFrom-Json`,
    `function Has-SavedValue {`,
    `  param([string]$Name)`,
    `  return $state.PSObject.Properties.Name -contains $Name -and $null -ne $state.$Name`,
    `}`,
    `function Restore-StringRegistryValue {`,
    `  param([string]$Name)`,
    `  if (Has-SavedValue $Name -and -not [string]::IsNullOrWhiteSpace([string]$state.$Name)) {`,
    `    New-ItemProperty -Path $settingsPath -Name $Name -PropertyType String -Value ([string]$state.$Name) -Force | Out-Null`,
    `  } else {`,
    `    Remove-ItemProperty -Path $settingsPath -Name $Name -ErrorAction SilentlyContinue`,
    `  }`,
    `}`,
    `function Restore-DwordRegistryValue {`,
    `  param([string]$Name, [int]$DefaultValue = 0)`,
    `  $value = if (Has-SavedValue $Name) { [int]$state.$Name } else { $DefaultValue }`,
    `  New-ItemProperty -Path $settingsPath -Name $Name -PropertyType DWord -Value $value -Force | Out-Null`,
    `}`,
    `Restore-DwordRegistryValue -Name ProxyEnable -DefaultValue 0`,
    `Restore-StringRegistryValue -Name ProxyServer`,
    `Restore-StringRegistryValue -Name ProxyOverride`,
    `Restore-StringRegistryValue -Name AutoConfigURL`,
    `if (Has-SavedValue 'AutoDetect') { Restore-DwordRegistryValue -Name AutoDetect -DefaultValue 0 }`,
    ...windowsProxyChangeNotifierCommand(),
    `Notify-ProxySettingsChanged`,
    `Remove-Item -Path $statePath -ErrorAction SilentlyContinue`,
    `Write-Host 'APEX Load Windows proxy settings restored.'`,
  ].join('\n')
}
