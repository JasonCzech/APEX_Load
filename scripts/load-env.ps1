#requires -Version 5.1
<#!
.SYNOPSIS
Loads key/value pairs from a .env-style file into the current PowerShell session.

.DESCRIPTION
Parses simple KEY=VALUE lines (ignoring blank lines and comments) and sets
process environment variables for the current shell.

.EXAMPLE
.\scripts\load-env.ps1

.EXAMPLE
.\scripts\load-env.ps1 -Path .env.example -OverrideExisting
#>

[CmdletBinding()]
param(
  [string]$Path = '.env.example',
  [switch]$OverrideExisting
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Resolve-EnvFilePath {
  param([Parameter(Mandatory = $true)][string]$RawPath)
  $repoRoot = Split-Path -Parent $PSScriptRoot
  if ([System.IO.Path]::IsPathRooted($RawPath)) {
    return $RawPath
  }
  return Join-Path $repoRoot $RawPath
}

function Parse-EnvLine {
  param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Line)

  $trimmed = $Line.Trim()
  if ([string]::IsNullOrWhiteSpace($trimmed)) { return $null }
  if ($trimmed.StartsWith('#')) { return $null }

  $separator = $trimmed.IndexOf('=')
  if ($separator -lt 1) { return $null }

  $key = $trimmed.Substring(0, $separator).Trim()
  $value = $trimmed.Substring($separator + 1)

  # The length guard matters: for a value that is a single quote character both
  # StartsWith and EndsWith match the SAME char and Substring(1, -1) throws.
  # Mirrors the same guard in pkg/config/dotenv.go.
  if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
    $value = $value.Substring(1, $value.Length - 2)
  }

  return [pscustomobject]@{
    Key = $key
    Value = $value
  }
}

$envFile = Resolve-EnvFilePath -RawPath $Path
if (-not (Test-Path -LiteralPath $envFile)) {
  throw "Environment file not found: $envFile"
}

$loaded = 0
$skipped = 0

Get-Content -LiteralPath $envFile | ForEach-Object {
  $parsed = Parse-EnvLine -Line $_
  if ($null -eq $parsed) { return }

  $current = [Environment]::GetEnvironmentVariable($parsed.Key, 'Process')
  if (-not $OverrideExisting -and -not [string]::IsNullOrEmpty($current)) {
    $skipped++
    return
  }

  [Environment]::SetEnvironmentVariable($parsed.Key, $parsed.Value, 'Process')
  $loaded++
}

Write-Host ("Loaded {0} variables from {1}. Skipped {2} existing variables." -f $loaded, $envFile, $skipped)
