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

$dst = Join-Path $InstallDir "claude-proxy.exe"
Move-Item -Force -Path $tmp -Destination $dst

Write-Host "Installed: $dst"
Write-Host "Hint: add to PATH for current session:"
Write-Host "  `$env:Path = `"$InstallDir;`$env:Path`""

