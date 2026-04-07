[CmdletBinding()]
param(
  [Parameter(Mandatory = $false)]
  [string]$Repo = "baaaaaaaka/claude_code_helper",

  [Parameter(Mandatory = $false)]
  [string]$Version = "latest",

  [Parameter(Mandatory = $false)]
  [string]$InstallDir = "$env:USERPROFILE\\.local\\bin"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$apiBase = $env:CLAUDE_PROXY_API_BASE
if ([string]::IsNullOrWhiteSpace($apiBase)) {
  $apiBase = "https://api.github.com"
}
$apiBase = $apiBase.TrimEnd("/")

$releaseBase = $env:CLAUDE_PROXY_RELEASE_BASE
if ([string]::IsNullOrWhiteSpace($releaseBase)) {
  $releaseBase = "https://github.com"
}
$releaseBase = $releaseBase.TrimEnd("/")
$script:ShellSetupWarnings = New-Object System.Collections.Generic.List[string]

function Add-ShellSetupWarning([string]$message) {
  if ([string]::IsNullOrWhiteSpace($message)) {
    return
  }
  foreach ($existing in $script:ShellSetupWarnings) {
    if ($existing -ceq $message) {
      return
    }
  }
  $script:ShellSetupWarnings.Add($message)
}

function Get-SHA256Hex([string]$path) {
  $fileHashCmd = Get-Command Get-FileHash -ErrorAction SilentlyContinue
  if ($fileHashCmd) {
    return (Get-FileHash -Algorithm SHA256 -Path $path).Hash.ToLowerInvariant()
  }

  $stream = [System.IO.File]::OpenRead($path)
  try {
    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
      $hashBytes = $sha256.ComputeHash($stream)
    } finally {
      if ($sha256) {
        $sha256.Dispose()
      }
    }
  } finally {
    if ($stream) {
      $stream.Dispose()
    }
  }

  return ([System.BitConverter]::ToString($hashBytes) -replace "-", "").ToLowerInvariant()
}

function Ensure-ProfileLine([string]$path, [string]$line) {
  if ([string]::IsNullOrWhiteSpace($path) -or [string]::IsNullOrWhiteSpace($line)) {
    return $false
  }
  try {
    $dir = Split-Path -Parent $path
    if (-not [string]::IsNullOrWhiteSpace($dir) -and -not (Test-Path -LiteralPath $dir)) {
      New-Item -ItemType Directory -Force -Path $dir | Out-Null
    }
    if (-not (Test-Path -LiteralPath $path)) {
      New-Item -ItemType File -Force -Path $path | Out-Null
    }
    if (-not (Select-String -Path $path -SimpleMatch -Quiet -Pattern $line)) {
      Add-Content -Path $path -Value $line
      return $true
    }
    return $false
  } catch {
    Add-ShellSetupWarning "Could not update PowerShell profile: $path"
    return $false
  }
}

function Remove-ProfileLine([string]$path, [string]$line) {
  if ([string]::IsNullOrWhiteSpace($path) -or [string]::IsNullOrWhiteSpace($line)) {
    return $false
  }
  try {
    if (-not (Test-Path -LiteralPath $path)) {
      return $false
    }
    $lines = Get-Content -LiteralPath $path
    $kept = New-Object System.Collections.Generic.List[string]
    $removed = $false
    foreach ($existing in $lines) {
      if ($existing -ceq $line) {
        $removed = $true
        continue
      }
      $kept.Add($existing)
    }
    if (-not $removed) {
      return $false
    }
    Set-Content -LiteralPath $path -Value $kept -Encoding UTF8
    return $true
  } catch {
    Add-ShellSetupWarning "Could not update PowerShell profile: $path"
    return $false
  }
}

function Ensure-ProfileBlock([string]$path, [string]$marker, [string]$block) {
  if ([string]::IsNullOrWhiteSpace($path) -or [string]::IsNullOrWhiteSpace($marker) -or [string]::IsNullOrWhiteSpace($block)) {
    return $false
  }
  try {
    $dir = Split-Path -Parent $path
    if (-not [string]::IsNullOrWhiteSpace($dir) -and -not (Test-Path -LiteralPath $dir)) {
      New-Item -ItemType Directory -Force -Path $dir | Out-Null
    }
    if (-not (Test-Path -LiteralPath $path)) {
      New-Item -ItemType File -Force -Path $path | Out-Null
    }
    if (-not (Select-String -Path $path -SimpleMatch -Quiet -Pattern $marker)) {
      Add-Content -Path $path -Value $block
      return $true
    }
    return $false
  } catch {
    Add-ShellSetupWarning "Could not update PowerShell profile: $path"
    return $false
  }
}

function Resolve-FullPath([string]$pathValue) {
  if ([string]::IsNullOrWhiteSpace($pathValue)) {
    return ""
  }
  try {
    return [IO.Path]::GetFullPath($pathValue)
  } catch {
    return $pathValue
  }
}

function Normalize-PathForCompare([string]$pathValue) {
  $resolved = Resolve-FullPath -pathValue $pathValue
  if ([string]::IsNullOrWhiteSpace($resolved)) {
    return ""
  }
  return $resolved.TrimEnd("\", "/")
}

function Split-PathEntries([string]$pathList) {
  if ([string]::IsNullOrWhiteSpace($pathList)) {
    return @()
  }
  return @($pathList -split [regex]::Escape([IO.Path]::PathSeparator))
}

function Test-PathListContains([string]$pathList, [string]$pathValue) {
  $target = Normalize-PathForCompare -pathValue $pathValue
  if ([string]::IsNullOrWhiteSpace($target)) {
    return $false
  }
  foreach ($part in Split-PathEntries -pathList $pathList) {
    if ([string]::IsNullOrWhiteSpace($part)) { continue }
    if ((Normalize-PathForCompare -pathValue $part) -ieq $target) {
      return $true
    }
  }
  return $false
}

function Test-PathInEnv([string]$pathValue) {
  return Test-PathListContains -pathList $env:Path -pathValue $pathValue
}

function Get-UniquePathTargets([string[]]$pathValues) {
  $seen = @{}
  $result = New-Object System.Collections.Generic.List[string]
  foreach ($pathValue in $pathValues) {
    $resolved = Resolve-FullPath -pathValue $pathValue
    $normalized = Normalize-PathForCompare -pathValue $resolved
    if ([string]::IsNullOrWhiteSpace($normalized)) {
      continue
    }
    $key = $normalized.ToLowerInvariant()
    if ($seen.ContainsKey($key)) {
      continue
    }
    $seen[$key] = $true
    $result.Add($resolved)
  }
  return $result.ToArray()
}

function Prepend-PathForCurrentSession([string[]]$pathValues) {
  $missing = New-Object System.Collections.Generic.List[string]
  foreach ($pathValue in $pathValues) {
    if (-not (Test-PathInEnv -pathValue $pathValue)) {
      $missing.Add($pathValue)
    }
  }
  if ($missing.Count -eq 0) {
    return
  }
  $currentParts = Split-PathEntries -pathList $env:Path
  $env:Path = (@($missing.ToArray()) + $currentParts) -join [IO.Path]::PathSeparator
}

function Add-PathPersistent([string[]]$pathValues) {
  if (-not $pathValues -or $pathValues.Count -eq 0) {
    return
  }
  if ($env:CLAUDE_PROXY_SKIP_PATH_UPDATE -eq "1") {
    return
  }
  $current = [Environment]::GetEnvironmentVariable("Path", "User")
  $newEntries = New-Object System.Collections.Generic.List[string]
  foreach ($pathValue in $pathValues) {
    if (Test-PathListContains -pathList $current -pathValue $pathValue) {
      continue
    }
    $newEntries.Add($pathValue)
  }
  if ($newEntries.Count -eq 0) {
    return
  }
  $existing = Split-PathEntries -pathList $current
  $newPath = (@($newEntries.ToArray()) + $existing) -join [IO.Path]::PathSeparator
  try {
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
  } catch {
    Add-ShellSetupWarning "Could not persist PATH in the user environment."
  }
}

function New-ProfilePathBlock([string]$pathValue) {
  $resolved = Resolve-FullPath -pathValue $pathValue
  $escaped = $resolved.Replace("'", "''")
  $marker = "# claude-proxy PATH $resolved"
  $block = @"
$marker
`$claudeProxyPathEntry = '$escaped'
`$claudeProxyTrimChars = [char[]]@('\', '/')
if (-not ((`$env:Path -split [regex]::Escape([IO.Path]::PathSeparator)) | Where-Object { -not [string]::IsNullOrWhiteSpace(`$_) -and `$_.TrimEnd(`$claudeProxyTrimChars) -ieq `$claudeProxyPathEntry.TrimEnd(`$claudeProxyTrimChars) })) {
  `$env:Path = `$claudeProxyPathEntry + [IO.Path]::PathSeparator + `$env:Path
}
Remove-Variable claudeProxyPathEntry, claudeProxyTrimChars -ErrorAction SilentlyContinue
"@
  return @{
    Marker = $marker
    Block = $block
  }
}

function New-ProfileClpFunctionBlock([string]$installDir) {
  $resolved = Resolve-FullPath -pathValue $installDir
  $exePath = Join-Path $resolved "claude-proxy.exe"
  $escapedExePath = $exePath.Replace("'", "''")
  $marker = "# claude-proxy command clp $exePath"
  $block = @"
$marker
if (Test-Path Alias:clp) {
  Remove-Item Alias:clp -Force -ErrorAction SilentlyContinue
}
function global:clp {
  & '$escapedExePath' @args
}
"@
  return @{
    Marker = $marker
    Block = $block
  }
}

function Get-LatestTag([string]$repo) {
  $apiUri = "$apiBase/repos/$repo/releases/latest"
  try {
    $resp = Invoke-RestMethod -Uri $apiUri -Headers @{ "User-Agent" = "claude-proxy-install" }
    if ($resp.tag_name) { return [string]$resp.tag_name }
  } catch {
    # Fall back to parsing the redirect URL below.
  }

  $latestUri = "$releaseBase/$repo/releases/latest"
  try {
    $resp = Invoke-WebRequest -Uri $latestUri -Headers @{ "User-Agent" = "claude-proxy-install" } -UseBasicParsing
    $finalUri = $resp.BaseResponse.ResponseUri
    if ($finalUri -and $finalUri.AbsolutePath) {
      $tag = ($finalUri.AbsolutePath.TrimEnd("/") -split "/")[-1]
      if (-not [string]::IsNullOrWhiteSpace($tag) -and $tag -ne "latest") {
        return [string]$tag
      }
    }
  } catch {
    throw "Failed to determine latest tag from $latestUri"
  }

  throw "Failed to determine latest tag from $apiUri"
}

function Write-InstallSuccess([string]$dst, [string]$clpCmd, [string]$clpSh, [string]$installDirResolved, [string]$profilePath, [string[]]$shellSetupWarnings) {
  Write-Host ""
  Write-Host "==================== INSTALL SUCCESS ===================="
  Write-Host "Installed: $dst"
  Write-Host "Installed: $clpCmd"
  Write-Host "Installed: $clpSh"
  Write-Host "Run: & `"$dst`" proxy doctor"
  if ($shellSetupWarnings -and $shellSetupWarnings.Count -gt 0) {
    Write-Host "Attention: automatic PowerShell setup was incomplete."
    foreach ($warning in $shellSetupWarnings) {
      if (-not [string]::IsNullOrWhiteSpace($warning)) {
        Write-Host "  - $warning"
      }
    }
    Write-Host "To use 'clp' automatically, fix the PowerShell profile/PATH warnings above, then open a new shell."
  } else {
    Write-Host "PowerShell profile checked for PATH entries and a 'clp' command override:"
    Write-Host "  $profilePath"
    Write-Host "If 'clp' is not found in this shell, open a new shell."
  }
}

function Write-InstallFailure($err) {
  Write-Host ""
  Write-Host "==================== INSTALL FAILED ===================="
  Write-Host "claude-proxy install did not complete."
  if ($null -ne $err -and -not [string]::IsNullOrWhiteSpace([string]$err.Exception.Message)) {
    Write-Host ([string]$err.Exception.Message)
  }
}

try {
  $tag = $Version
  if ([string]::IsNullOrWhiteSpace($tag) -or $tag -eq "latest") {
    $tag = Get-LatestTag -repo $Repo
  }

  $verNoV = $tag.TrimStart("v")
  $arch = "amd64"
  $asset = "claude-proxy_${verNoV}_windows_${arch}.exe"
  $url = "$releaseBase/$Repo/releases/download/$tag/$asset"
  $checksumsUrl = "$releaseBase/$Repo/releases/download/$tag/checksums.txt"

  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  $installDirResolved = [IO.Path]::GetFullPath($InstallDir)

  $tmp = Join-Path $env:TEMP "$asset"
  Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing

  # Best-effort fetch/parse; mismatch still fails installation.
  $expected = $null
  try {
    $checksumsTmp = Join-Path $env:TEMP "checksums.txt"
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsTmp -UseBasicParsing
    $match = Select-String -Path $checksumsTmp -Pattern ("\s{1}" + [regex]::Escape($asset) + "$") | Select-Object -First 1
    if ($match) {
      $parts = $match.Line.Split(" ", [System.StringSplitOptions]::RemoveEmptyEntries)
      if ($parts.Length -gt 0) {
        $expected = $parts[0]
      }
    }
  } catch {
    $expected = $null
  }
  if ($expected) {
    $actual = Get-SHA256Hex -path $tmp
    if ($expected.ToLowerInvariant() -ne $actual) {
      throw "Checksum mismatch for $asset (expected $expected, got $actual)"
    }
  }

  $dst = Join-Path $installDirResolved "claude-proxy.exe"
  Move-Item -Force -Path $tmp -Destination $dst

  $clpCmd = Join-Path $installDirResolved "clp.cmd"
  $clpContent = "@echo off`r`n`"%~dp0claude-proxy.exe`" %*`r`n"
  Set-Content -Path $clpCmd -Value $clpContent -Encoding ASCII

  $clpSh = Join-Path $installDirResolved "clp"
  $clpShContent = @'
#!/usr/bin/env sh
exec "$(dirname "$0")/claude-proxy.exe" "$@"
'@
  Set-Content -Path $clpSh -Value $clpShContent -Encoding ASCII

  $homeDir = ""
  if (-not [string]::IsNullOrWhiteSpace($env:USERPROFILE)) {
    $homeDir = $env:USERPROFILE
  } elseif (-not [string]::IsNullOrWhiteSpace($env:HOME)) {
    $homeDir = $env:HOME
  }
  $claudeBinDir = ""
  if (-not [string]::IsNullOrWhiteSpace($homeDir)) {
    $claudeBinDir = Resolve-FullPath -pathValue (Join-Path $homeDir ".local\bin")
  }
  $pathTargets = Get-UniquePathTargets @($installDirResolved, $claudeBinDir)
  $missingPathTargets = New-Object System.Collections.Generic.List[string]
  foreach ($pathTarget in $pathTargets) {
    if (-not (Test-PathInEnv -pathValue $pathTarget)) {
      $missingPathTargets.Add($pathTarget)
    }
  }
  Prepend-PathForCurrentSession -pathValues $pathTargets
  Add-PathPersistent -pathValues $pathTargets

  $profilePath = $env:CLAUDE_PROXY_PROFILE_PATH
  if ([string]::IsNullOrWhiteSpace($profilePath)) {
    $profilePath = $PROFILE
  }
  $legacyAliasLine = 'Set-Alias -Name clp -Value claude-proxy'
  $clpFunctionBlock = New-ProfileClpFunctionBlock -installDir $installDirResolved

  $profileUpdated = $false
  if ($missingPathTargets.Count -gt 0) {
    $profilePathTargets = $missingPathTargets.ToArray()
    [array]::Reverse($profilePathTargets)
    foreach ($pathTarget in $profilePathTargets) {
      $pathBlock = New-ProfilePathBlock -pathValue $pathTarget
      if (Ensure-ProfileBlock -path $profilePath -marker $pathBlock.Marker -block $pathBlock.Block) {
        $profileUpdated = $true
      }
    }
  }
  if (Remove-ProfileLine -path $profilePath -line $legacyAliasLine) {
    $profileUpdated = $true
  }
  if (Ensure-ProfileBlock -path $profilePath -marker $clpFunctionBlock.Marker -block $clpFunctionBlock.Block) {
    $profileUpdated = $true
  }

  if ($profileUpdated) {
    try {
      . $profilePath
    } catch {
      Add-ShellSetupWarning "Could not reload PowerShell profile: $profilePath"
    }
  }

  $shellSetupWarnings = $script:ShellSetupWarnings.ToArray()
  Write-InstallSuccess -dst $dst -clpCmd $clpCmd -clpSh $clpSh -installDirResolved $installDirResolved -profilePath $profilePath -shellSetupWarnings $shellSetupWarnings
} catch {
  Write-InstallFailure $_
  throw
}
