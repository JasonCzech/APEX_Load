#requires -Version 5.1
<#
.SYNOPSIS
Compiles a Java Vuser Actions source file into a jar and optionally wires the
local runner environment.

.DESCRIPTION
Builds a jar from an Actions-style Java source file using a JDK and a
configurable dependency classpath, so a LoadRunner Java Vuser can be exercised
against a local runner without assembling the javac/jar invocation by hand.

Optionally it can:
- Probe whether the imported code actually runs on Java 17+, and fall back to a
  Java 8 runtime plus the Java 8-targeted harness when it does not.
- Persist the APEXLOAD_JAVA_VUSER_* user environment variables for local runs.
- Restart the local controller and wait for it to report healthy.

All site-specific paths are parameters with no defaults: pass them explicitly or
export the matching APEXLOAD_JAVA_VUSER_* variables. Do not commit local paths
into this file — it ships in a public repository.

.EXAMPLE
.\scripts\build-java-vuser-actions.ps1 -SourceFile .\artifacts\Actions.java `
  -OutputJar C:\runner\Actions.jar -DependencyClasspath C:\jars\util.jar,C:\jars\app.jar

.EXAMPLE
.\scripts\build-java-vuser-actions.ps1 -SourceFile .\artifacts\Actions.java `
  -OutputJar C:\runner\Actions.jar -AutoDetectLegacyJava `
  -LegacyProbeClasses org.w3c.dom.ls.DocumentLS -LegacyJavaHome 'C:\Program Files\Java\jre1.8.0_351' `
  -ConfigureUserRunnerEnv -RestartController
#>

[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$SourceFile,

  [Parameter(Mandatory = $true)]
  [string]$OutputJar,

  [string]$JdkHome = '',

  [string]$HarnessJar = '',

  [ValidateSet('8', '17')]
  [string]$JavaRelease = '17',

  # Probe the imported code on the selected JDK and fall back to Java 8 when a
  # dependency (typically legacy middleware) will not load on Java 17+.
  [switch]$AutoDetectLegacyJava,

  [string]$LegacyJavaHome = '',

  [string]$LegacyHarnessJar = '',

  # Class names that must load for the Java 17+ path to be considered viable —
  # typically a legacy middleware class you know breaks on a modern JVM.
  #
  # Deliberately EMPTY by default. A default such as org.w3c.dom.ls.DocumentLS is
  # in no modern JDK's own class library, so unless the caller's
  # DependencyClasspath happens to ship it the probe always fails and the script
  # silently downgrades the whole runner to Java 8 — turning a security-relevant
  # floor opt-in into the default outcome. With no classes named there is nothing
  # to disprove, so auto-detect keeps the modern JDK.
  [string[]]$LegacyProbeClasses = @(),

  [string[]]$DependencyClasspath = @(),

  [switch]$ConfigureUserRunnerEnv,
  [switch]$RestartController,

  [string]$ControllerPath = '',
  [string]$ControllerHealthUrl = 'http://127.0.0.1:8080/api/v1/desktop/status'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Resolve-RepoRoot {
  return (Split-Path -Parent $PSScriptRoot)
}

function Resolve-JdkHome {
  param([string]$Preferred)

  if (-not [string]::IsNullOrWhiteSpace($Preferred)) {
    return $Preferred.Trim()
  }
  if (-not [string]::IsNullOrWhiteSpace($env:APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME)) {
    return $env:APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME.Trim()
  }
  if (-not [string]::IsNullOrWhiteSpace($env:JAVA_HOME)) {
    return $env:JAVA_HOME.Trim()
  }
  throw 'JdkHome was not provided and neither APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME nor JAVA_HOME is set.'
}

function Assert-PathExists {
  param(
    [Parameter(Mandatory = $true)][string]$Path,
    [Parameter(Mandatory = $true)][string]$Description
  )
  if (-not (Test-Path -LiteralPath $Path)) {
    throw "$Description not found: $Path"
  }
}

function Resolve-HarnessJar {
  param(
    [string]$Preferred,
    [string]$RepoRoot
  )

  if (-not [string]::IsNullOrWhiteSpace($Preferred)) {
    return $Preferred.Trim()
  }
  if (-not [string]::IsNullOrWhiteSpace($env:APEXLOAD_JAVA_VUSER_HARNESS_JAR)) {
    return $env:APEXLOAD_JAVA_VUSER_HARNESS_JAR.Trim()
  }
  return (Join-Path $RepoRoot 'bin\java_vuser\apexload-java-vuser-runner.jar')
}

function Test-ClasspathEntry {
  param([string]$Entry)

  return (Test-Path -LiteralPath $Entry)
}

# NOTE: the parameter is deliberately not named $Args — that is a PowerShell
# automatic variable, and shadowing it fails under Set-StrictMode.
function Invoke-External {
  param(
    [Parameter(Mandatory = $true)][string]$Exe,
    [Parameter(Mandatory = $true)][string[]]$Arguments,
    [Parameter(Mandatory = $true)][string]$FailureMessage
  )
  & $Exe @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "$FailureMessage (exit code $LASTEXITCODE)"
  }
}

function Wait-ControllerHealth {
  param(
    [Parameter(Mandatory = $true)][string]$HealthUrl,
    [int]$MaxAttempts = 15
  )

  for ($i = 0; $i -lt $MaxAttempts; $i++) {
    try {
      $response = Invoke-WebRequest -Uri $HealthUrl -UseBasicParsing -TimeoutSec 5
      if ($response.StatusCode -eq 200) {
        return $true
      }
    } catch {
      # Ignore until the retries are exhausted; the controller is still starting.
    }
    Start-Sleep -Milliseconds 700
  }
  return $false
}

function Join-UniqueClasspath {
  param([string[]]$Entries)

  $seen = @{}
  $ordered = New-Object System.Collections.Generic.List[string]
  foreach ($entry in $Entries) {
    if ([string]::IsNullOrWhiteSpace($entry)) {
      continue
    }
    $trimmed = $entry.Trim()
    if (-not $seen.ContainsKey($trimmed)) {
      $seen[$trimmed] = $true
      $ordered.Add($trimmed)
    }
  }
  return [string]::Join(';', $ordered)
}

function Write-Utf8NoBomFile {
  param(
    [Parameter(Mandatory = $true)][string]$Path,
    [Parameter(Mandatory = $true)][string]$Content
  )
  $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
  [System.IO.File]::WriteAllText($Path, $Content, $utf8NoBom)
}

# The shipped harness is compiled --release 17 and cannot load on a Java 8 JVM
# at all, so the legacy lane needs its own jar. `make java-vuser-runner-legacy`
# is the single source of truth for how it is built; this only invokes it.
function Build-LegacyHarnessJar {
  param(
    [Parameter(Mandatory = $true)][string]$RepoRoot,
    [string]$DestinationJar
  )

  $builtJar = Join-Path $RepoRoot 'bin\java_vuser\apexload-java-vuser-runner-java8.jar'
  Write-Host 'Building the Java 8 harness jar via `make java-vuser-runner-legacy`.'
  Push-Location $RepoRoot
  try {
    Invoke-External -Exe 'make' -Arguments @('java-vuser-runner-legacy') -FailureMessage 'legacy harness build failed'
  } finally {
    Pop-Location
  }
  Assert-PathExists -Path $builtJar -Description 'Java 8 harness jar'

  if ([string]::IsNullOrWhiteSpace($DestinationJar)) {
    return $builtJar
  }
  $destination = [System.IO.Path]::GetFullPath($DestinationJar)
  New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
  Copy-Item -LiteralPath $builtJar -Destination $destination -Force
  return $destination
}

# Compiles a tiny probe to Java 8 bytecode (so it runs on either runtime) and
# reports whether every required class, plus Actions itself, actually loads.
function Test-LegacyJavaCompatibility {
  param(
    [Parameter(Mandatory = $true)][string]$RepoRoot,
    [Parameter(Mandatory = $true)][string]$JavaExe,
    [Parameter(Mandatory = $true)][string]$JavacExe,
    [Parameter(Mandatory = $true)][string]$ClasspathValue,
    [Parameter(Mandatory = $true)][string[]]$ProbeClasses
  )

  # Never reuse a repository or caller-controlled directory for compiler
  # scratch. The old fixed artifacts/classprobe-auto path was recursively
  # deleted on every run and could destroy unrelated diagnostic evidence.
  $probeDir = Join-Path ([System.IO.Path]::GetTempPath()) ('apexload-java-vuser-probe-' + [Guid]::NewGuid().ToString('N'))
  $probePath = Join-Path $probeDir 'ClassProbeAuto.java'
  $probeClassDir = Join-Path $probeDir 'classes'
  $probeNames = ($ProbeClasses | ForEach-Object { '"' + $_ + '"' }) -join ', '
  $probeSource = @"
public class ClassProbeAuto {
  public static void main(String[] args) {
    String[] names = new String[] {$probeNames};
    for (String n : names) {
      try {
        Class.forName(n);
        System.out.println("OK " + n);
      } catch (Throwable t) {
        System.out.println("FAIL " + n + " -> " + t.getClass().getName() + ": " + t.getMessage());
      }
    }
  }
}
"@
  try {
    New-Item -ItemType Directory -Force -Path $probeClassDir | Out-Null
    Write-Utf8NoBomFile -Path $probePath -Content $probeSource

    Invoke-External -Exe $JavacExe -Arguments @('--release', '8', '-nowarn', '-cp', $ClasspathValue, '-d', $probeClassDir, $probePath) -FailureMessage 'probe compile failed'

    $probeClasspath = [string]::Join(';', @($ClasspathValue, $probeClassDir))
    # Under Windows PowerShell 5.1, `2>&1` turns native stderr lines into
    # ErrorRecords, and with the script-wide $ErrorActionPreference = 'Stop' the
    # first JVM warning on stderr (deprecation notices, -verbose noise) would
    # become a terminating NativeCommandError before the probe result is read.
    # Relax the preference for the invocation only; the exit-code path below is
    # unaffected.
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
      $output = & $JavaExe -cp $probeClasspath ClassProbeAuto 2>&1
    } finally {
      $ErrorActionPreference = $previousErrorActionPreference
    }
    $text = (($output | Out-String).Trim())
    Write-Host 'Probe output:'
    Write-Host $text

    # Match whole lines, not substrings: "OK com.x.Foo" is a prefix of
    # "OK com.x.FooBar", so a Contains() check would report Foo loadable when
    # only FooBar loaded.
    $okLines = @($text -split "`r?`n" | ForEach-Object { $_.Trim() })
    foreach ($name in $ProbeClasses) {
      if ($okLines -notcontains "OK $name") {
        return $false
      }
    }
    return $true
  } finally {
    Remove-Item -Recurse -Force -LiteralPath $probeDir -ErrorAction SilentlyContinue
  }
}

$repoRoot = Resolve-RepoRoot
$sourcePath = Resolve-Path -LiteralPath $SourceFile
$resolvedJdkHome = [System.IO.Path]::GetFullPath((Resolve-JdkHome -Preferred $JdkHome))
$resolvedHarnessJar = [System.IO.Path]::GetFullPath((Resolve-HarnessJar -Preferred $HarnessJar -RepoRoot $repoRoot))
$resolvedOutputJar = [System.IO.Path]::GetFullPath($OutputJar)
$outputDir = Split-Path -Parent $resolvedOutputJar
# A caller may place OutputJar beside an unrelated directory named "classes".
# Compiler scratch therefore lives in a unique OS-temporary directory; never
# recursively delete a path derived from the output destination.
$classesDir = Join-Path ([System.IO.Path]::GetTempPath()) ('apexload-java-vuser-classes-' + [Guid]::NewGuid().ToString('N'))

Assert-PathExists -Path $sourcePath -Description 'Source file'
Assert-PathExists -Path $resolvedJdkHome -Description 'JDK home'
Assert-PathExists -Path $resolvedHarnessJar -Description 'Harness jar'

$javacExe = Join-Path $resolvedJdkHome 'bin\javac.exe'
Assert-PathExists -Path $javacExe -Description 'javac executable'

$wildcardClasspathEntries = @($DependencyClasspath | Where-Object {
  $_ -match '[*?\[]'
})
if ($wildcardClasspathEntries.Count -gt 0) {
  throw ('Wildcard classpath entries are not supported; enumerate exact files or directories: ' + [string]::Join(', ', $wildcardClasspathEntries))
}
$DependencyClasspath = @($DependencyClasspath | ForEach-Object {
  if ([string]::IsNullOrWhiteSpace($_)) {
    throw 'Classpath entries must not be empty.'
  }
  [System.IO.Path]::GetFullPath($_.Trim())
})

$missing = @()
$classpathEntries = @($resolvedHarnessJar) + $DependencyClasspath
foreach ($entry in $classpathEntries) {
  if (-not (Test-ClasspathEntry -Entry $entry)) {
    $missing += $entry
  }
}
if ($missing.Count -gt 0) {
  throw ("Missing classpath entries:`n- " + ($missing -join "`n- "))
}

New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

$probeClasspathValue = [string]::Join(';', $classpathEntries)

$effectiveRelease = $JavaRelease
$effectiveRunnerHome = $resolvedJdkHome
$effectiveRunnerCommand = Join-Path $resolvedJdkHome 'bin\java.exe'
$effectiveAllowLegacyJava = 'false'
$effectiveHarnessJar = $resolvedHarnessJar

if ($AutoDetectLegacyJava -and $LegacyProbeClasses.Count -eq 0) {
  # Fail loudly rather than fall back. Downgrading on an empty probe would make
  # "no evidence" mean "incompatible", quietly enabling the Java 8 floor opt-in.
  throw '-AutoDetectLegacyJava requires -LegacyProbeClasses: name the middleware class(es) that must load on the modern JDK. With none supplied there is nothing to probe and the modern JDK is kept.'
}

if ($AutoDetectLegacyJava) {
  Write-Host ('Auto-detect: probing {0} dependency class(es) on the selected JDK.' -f $LegacyProbeClasses.Count)
  $probeJava = Join-Path $resolvedJdkHome 'bin\java.exe'
  # Probe the DEPENDENCY classes only. Actions is deliberately not probed: it is
  # not compiled until after this block (its release target depends on the probe
  # result) and its output directory is never on $probeClasspathValue, so
  # including it made Class.forName always fail — the probe could never report
  # compatible and the script always took the Java 8 fallback, defeating
  # auto-detection.
  $compatible = Test-LegacyJavaCompatibility -RepoRoot $repoRoot -JavaExe $probeJava -JavacExe $javacExe -ClasspathValue $probeClasspathValue -ProbeClasses $LegacyProbeClasses
  if ($compatible) {
    Write-Host 'Auto-detect: the selected JDK is compatible; keeping the current runtime.'
  } else {
    if ([string]::IsNullOrWhiteSpace($LegacyJavaHome)) {
      throw 'Auto-detect found an incompatible dependency but -LegacyJavaHome was not supplied.'
    }
    $resolvedLegacyJavaHome = [System.IO.Path]::GetFullPath($LegacyJavaHome.Trim())
    $candidateLegacyJava = Join-Path $resolvedLegacyJavaHome 'bin\java.exe'
    Assert-PathExists -Path $candidateLegacyJava -Description 'Legacy Java runtime'
    Write-Host 'Auto-detect: falling back to the legacy Java runtime and Java 8 bytecode.'
    $effectiveRelease = '8'
    $effectiveRunnerHome = $resolvedLegacyJavaHome
    $effectiveRunnerCommand = $candidateLegacyJava
    $effectiveAllowLegacyJava = 'true'
    $effectiveHarnessJar = Build-LegacyHarnessJar -RepoRoot $repoRoot -DestinationJar $LegacyHarnessJar
  }
}

# The legacy fallback replaces both the runtime and the harness. Compile against
# that effective harness too: retaining the Java 17 harness in javac's
# classpath makes a --release 8 compile fail on its class-file version before
# the legacy lane can produce a runnable jar.
$compileClasspathEntries = @($effectiveHarnessJar) + $DependencyClasspath
$classpathValue = [string]::Join(';', $compileClasspathEntries)

$jarExe = Join-Path $resolvedJdkHome 'bin\jar.exe'
Assert-PathExists -Path $jarExe -Description 'jar executable'

Write-Host "Compiling source: $sourcePath"
try {
  New-Item -ItemType Directory -Force -Path $classesDir | Out-Null
  Invoke-External -Exe $javacExe -Arguments @('--release', $effectiveRelease, '-nowarn', '-cp', $classpathValue, '-d', $classesDir, $sourcePath) -FailureMessage 'javac compilation failed'

  if (Test-Path -LiteralPath $resolvedOutputJar) {
    Remove-Item -Force -LiteralPath $resolvedOutputJar
  }

  Write-Host "Packaging jar: $resolvedOutputJar"
  Invoke-External -Exe $jarExe -Arguments @('--create', '--file', $resolvedOutputJar, '-C', $classesDir, '.') -FailureMessage 'jar creation failed'
} finally {
  Remove-Item -Recurse -Force -LiteralPath $classesDir -ErrorAction SilentlyContinue
}

if ($ConfigureUserRunnerEnv) {
  # Runtime validation rejects wildcard entries, so persist an explicit, complete
  # shared classpath: the rebuilt Actions jar plus every legacy dependency.
  $sharedClasspath = Join-UniqueClasspath -Entries (@($resolvedOutputJar) + $DependencyClasspath)
  $runnerEnv = [ordered]@{
    'APEXLOAD_JAVA_VUSER_RUNNER_ENABLED'    = 'true'
    'APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME'  = $effectiveRunnerHome
    'APEXLOAD_JAVA_VUSER_RUNNER_COMMAND'    = $effectiveRunnerCommand
    'APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA' = $effectiveAllowLegacyJava
    'APEXLOAD_JAVA_VUSER_HARNESS_JAR'       = $effectiveHarnessJar
    'APEXLOAD_JAVA_VUSER_CLASSPATH'         = $sharedClasspath
  }
  foreach ($name in $runnerEnv.Keys) {
    [Environment]::SetEnvironmentVariable($name, $runnerEnv[$name], 'User')
    # Also set the process variables so an immediate restart picks them up.
    Set-Item -Path "env:$name" -Value $runnerEnv[$name]
    Write-Host ("Configured {0}={1}" -f $name, $runnerEnv[$name])
  }
  $env:JAVA_HOME = $effectiveRunnerHome
  $env:JRE_HOME = $effectiveRunnerHome
}

if ($RestartController) {
  $resolvedControllerPath = $ControllerPath
  if ([string]::IsNullOrWhiteSpace($resolvedControllerPath)) {
    $resolvedControllerPath = Join-Path $repoRoot 'bin\controller.exe'
  }
  $resolvedControllerPath = [System.IO.Path]::GetFullPath($resolvedControllerPath)
  Assert-PathExists -Path $resolvedControllerPath -Description 'Controller executable'

  # A generic process name is not an ownership boundary. Developer machines can
  # have unrelated products named controller.exe; stop only the exact binary
  # this invocation is about to replace.
  $controllerProcessName = [System.IO.Path]::GetFileNameWithoutExtension($resolvedControllerPath)
  $controllers = @(Get-Process -Name $controllerProcessName -ErrorAction SilentlyContinue | Where-Object {
    $candidatePath = ''
    try {
      $candidatePath = [System.IO.Path]::GetFullPath($_.Path)
    } catch {
      # Access-denied or already-exited processes are not safe stop targets.
    }
    -not [string]::IsNullOrWhiteSpace($candidatePath) -and
      [System.StringComparer]::OrdinalIgnoreCase.Equals($candidatePath, $resolvedControllerPath)
  })
  if ($controllers) {
    $controllers | Stop-Process -Force
  }

  Start-Process -FilePath $resolvedControllerPath -WorkingDirectory $repoRoot | Out-Null
  if (-not (Wait-ControllerHealth -HealthUrl $ControllerHealthUrl)) {
    throw "Controller restart timed out waiting for $ControllerHealthUrl to return 200"
  }
  Write-Host 'Controller restarted and healthy (200).'
}

$jarInfo = Get-Item -LiteralPath $resolvedOutputJar
Write-Host ('Done. Jar: {0} ({1} bytes), Java release target={2}' -f $jarInfo.FullName, $jarInfo.Length, $effectiveRelease)
