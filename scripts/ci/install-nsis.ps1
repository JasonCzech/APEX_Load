$ErrorActionPreference = "Stop"

$nsisVersion = "3.10"
$nsisArchiveSha256 = "fcdce3229717a2a148e7cda0ab5bdb667f39d8fb33ede1da8dabc336bd5ad110"
$archiveName = "nsis-$nsisVersion.zip"
$downloadedArchive = Join-Path $env:RUNNER_TEMP "apexload-$archiveName"
$toolDirectory = Join-Path $env:RUNNER_TEMP "apexload-nsis-$nsisVersion"

$archivePath = $env:NSIS_ARCHIVE_PATH
if (-not $archivePath) {
  if ($env:APEXLOAD_USE_PREINSTALLED_TOOLS -eq "true") {
    throw "NSIS_ARCHIVE_PATH must point to the reviewed NSIS archive on preinstalled-tool runners"
  }
  $baseUrl = $env:NSIS_RELEASE_BASE_URL
  if (-not $baseUrl) {
    $baseUrl = "https://downloads.sourceforge.net/project/nsis/NSIS%203/$nsisVersion"
  }
  if (-not $baseUrl.StartsWith("https://", [StringComparison]::OrdinalIgnoreCase)) {
    throw "NSIS_RELEASE_BASE_URL must use https"
  }
  $downloadUrl = "$($baseUrl.TrimEnd('/'))/$archiveName"
  for ($attempt = 1; $attempt -le 3; $attempt++) {
    & curl.exe --fail --location --silent --show-error --retry 3 --retry-all-errors `
      --output $downloadedArchive $downloadUrl
    if ($LASTEXITCODE -ne 0) {
      throw "NSIS archive download failed with curl exit code $LASTEXITCODE"
    }
    $downloadedSha256 = (Get-FileHash -LiteralPath $downloadedArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($downloadedSha256 -eq $nsisArchiveSha256) {
      break
    }
    Remove-Item -LiteralPath $downloadedArchive -Force
    if ($attempt -eq 3) {
      throw "NSIS archive checksum mismatch after $attempt download attempts (last hash: $downloadedSha256)"
    }
    Start-Sleep -Seconds (2 * $attempt)
  }
  $archivePath = $downloadedArchive
}

if (-not (Test-Path -LiteralPath $archivePath -PathType Leaf)) {
  throw "reviewed NSIS archive does not exist: $archivePath"
}
$actualSha256 = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSha256 -ne $nsisArchiveSha256) {
  throw "NSIS archive checksum mismatch"
}

Remove-Item -LiteralPath $toolDirectory -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $toolDirectory | Out-Null
Expand-Archive -LiteralPath $archivePath -DestinationPath $toolDirectory
$makensis = Join-Path $toolDirectory "nsis-$nsisVersion\makensis.exe"
if (-not (Test-Path -LiteralPath $makensis -PathType Leaf)) {
  throw "checksum-verified NSIS archive did not contain makensis.exe"
}
$reportedVersion = (& $makensis /VERSION).Trim()
if ($reportedVersion -ne "v$nsisVersion") {
  throw "unexpected NSIS version: $reportedVersion"
}
(Split-Path -Parent $makensis) | Out-File -FilePath $env:GITHUB_PATH -Append
