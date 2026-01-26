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

function Ensure-ProfileLine([string]$path, [string]$line) {
  if ([string]::IsNullOrWhiteSpace($path) -or [string]::IsNullOrWhiteSpace($line)) {
    return $false
  }
  $dir = Split-Path -Parent $path
  if (-not (Test-Path -LiteralPath $dir)) {
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
}

function Test-PathInEnv([string]$pathValue) {
  if ([string]::IsNullOrWhiteSpace($pathValue)) {
    return $false
  }
  $target = $pathValue.TrimEnd("\")
  $parts = $env:Path -split ";"
  foreach ($part in $parts) {
    if ([string]::IsNullOrWhiteSpace($part)) { continue }
    if ($part.TrimEnd("\") -ieq $target) {
      return $true
    }
  }
  return $false
}

function Add-PathPersistent([string]$pathValue) {
  if ([string]::IsNullOrWhiteSpace($pathValue)) {
    return
  }
  $current = [Environment]::GetEnvironmentVariable("Path", "User")
  $paths = @()
  if (-not [string]::IsNullOrWhiteSpace($current)) {
    $paths = $current -split ";"
  }
  if ($paths -contains $pathValue) {
    return
  }
  $newPath = if ([string]::IsNullOrWhiteSpace($current)) { $pathValue } else { "$pathValue;$current" }
  if ($env:CLAUDE_PROXY_SKIP_PATH_UPDATE -eq "1") {
    return
  }
  try {
    & setx PATH "$newPath" | Out-Null
  } catch {
    Write-Warning "Failed to persist PATH update: $_"
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

# Optional checksum verification.
try {
  $checksumsTmp = Join-Path $env:TEMP "checksums.txt"
  Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsTmp -UseBasicParsing
  $expected = (Select-String -Path $checksumsTmp -Pattern ("\s{1}" + [regex]::Escape($asset) + "$") | Select-Object -First 1).Line.Split(" ", [System.StringSplitOptions]::RemoveEmptyEntries)[0]
  if ($expected) {
    $actual = (Get-FileHash -Algorithm SHA256 -Path $tmp).Hash.ToLowerInvariant()
    if ($expected.ToLowerInvariant() -ne $actual) {
      throw "Checksum mismatch for $asset (expected $expected, got $actual)"
    }
  }
} catch {
  # Best-effort only; do not fail installation if checksum fetch/parse fails.
}

$dst = Join-Path $installDirResolved "claude-proxy.exe"
Move-Item -Force -Path $tmp -Destination $dst

$pathInEnv = Test-PathInEnv -pathValue $installDirResolved
if (-not $pathInEnv) {
  $env:Path = "$installDirResolved;$env:Path"
}
if (-not $pathInEnv) {
  Add-PathPersistent -pathValue $installDirResolved
}

$profilePath = $env:CLAUDE_PROXY_PROFILE_PATH
if ([string]::IsNullOrWhiteSpace($profilePath)) {
  $profilePath = $PROFILE
}
$pathLine = '$env:Path = "' + $installDirResolved + ';$env:Path"'
$aliasLine = 'Set-Alias -Name clp -Value claude-proxy'

$profileUpdated = $false
if (-not $pathInEnv) {
  if (Ensure-ProfileLine -path $profilePath -line $pathLine) {
    $profileUpdated = $true
  }
}
if (Ensure-ProfileLine -path $profilePath -line $aliasLine) {
  $profileUpdated = $true
}

if ($profileUpdated) {
  try {
    . $profilePath
  } catch {
    Write-Warning "Failed to reload profile: $_"
  }
}

Write-Host "Installed: $dst"
Write-Host "Hint: add to PATH for current session:"
Write-Host "  `$env:Path = `"$installDirResolved;`$env:Path`""
Write-Host "Shell profile checked for PATH and alias 'clp' (reload attempted):"
Write-Host "  $profilePath"

