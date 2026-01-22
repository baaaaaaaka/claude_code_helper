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

function Get-LatestTag([string]$repo) {
  $uri = "https://api.github.com/repos/$repo/releases/latest"
  $resp = Invoke-RestMethod -Uri $uri -Headers @{ "User-Agent" = "claude-proxy-install" }
  if (-not $resp.tag_name) { throw "Failed to determine latest tag from $uri" }
  return [string]$resp.tag_name
}

$tag = $Version
if ([string]::IsNullOrWhiteSpace($tag) -or $tag -eq "latest") {
  $tag = Get-LatestTag -repo $Repo
}

$verNoV = $tag.TrimStart("v")
$arch = "amd64"
$asset = "claude-proxy_${verNoV}_windows_${arch}.exe"
$url = "https://github.com/$Repo/releases/download/$tag/$asset"
$checksumsUrl = "https://github.com/$Repo/releases/download/$tag/checksums.txt"

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

