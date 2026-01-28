#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/claude_release_info.sh [options]

Fetch Claude Code install URLs and the GCS release bucket. Falls back to
claude-proxy SSH proxy when direct access fails.

Options:
  --json                 Output JSON (default: text)
  --install-sh URL       Override install.sh URL
  --install-ps1 URL       Override install.ps1 URL
  --proxy-bin PATH        Path to claude-proxy binary (default: claude-proxy)
  --proxy-config PATH     Config path for claude-proxy
  --proxy-profile ID      Profile id/name for claude-proxy
  --no-proxy              Disable proxy fallback
  -h, --help              Show help

Environment overrides:
  CLAUDE_INSTALL_SH_URL
  CLAUDE_INSTALL_PS1_URL
  CLAUDE_PROXY_BIN
  CLAUDE_PROXY_CONFIG
  CLAUDE_PROXY_PROFILE
  CLAUDE_PROXY_DISABLE=1  (same as --no-proxy)
EOF
}

json=0
install_sh_url="${CLAUDE_INSTALL_SH_URL:-https://claude.ai/install.sh}"
install_ps1_url="${CLAUDE_INSTALL_PS1_URL:-https://claude.ai/install.ps1}"
proxy_bin="${CLAUDE_PROXY_BIN:-claude-proxy}"
proxy_config="${CLAUDE_PROXY_CONFIG:-}"
proxy_profile="${CLAUDE_PROXY_PROFILE:-}"
use_proxy=1

while [ $# -gt 0 ]; do
  case "$1" in
    --json) json=1; shift ;;
    --install-sh) install_sh_url="$2"; shift 2 ;;
    --install-ps1) install_ps1_url="$2"; shift 2 ;;
    --proxy-bin) proxy_bin="$2"; shift 2 ;;
    --proxy-config) proxy_config="$2"; shift 2 ;;
    --proxy-profile) proxy_profile="$2"; shift 2 ;;
    --no-proxy) use_proxy=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ "${CLAUDE_PROXY_DISABLE:-}" = "1" ]; then
  use_proxy=0
fi

downloader=""
downloader_args=()
curl_timeouts=(--connect-timeout 5 --max-time 20)
wget_timeouts=(--timeout=20 --tries=1)
if command -v curl >/dev/null 2>&1; then
  downloader="curl"
  downloader_args=(-fsSL "${curl_timeouts[@]}")
elif command -v wget >/dev/null 2>&1; then
  downloader="wget"
  downloader_args=(-qO- "${wget_timeouts[@]}")
else
  echo "Need curl or wget to fetch URLs" >&2
  exit 1
fi

default_proxy_config() {
  if [ -n "$proxy_config" ]; then
    echo "$proxy_config"
    return 0
  fi
  case "$(uname -s)" in
    Darwin) echo "${HOME}/Library/Application Support/claude-proxy/config.json" ;;
    Linux) echo "${XDG_CONFIG_HOME:-$HOME/.config}/claude-proxy/config.json" ;;
    *) echo "" ;;
  esac
}

guess_proxy_profile() {
  local cfg="$1"
  if [ -z "$cfg" ] || [ ! -f "$cfg" ]; then
    return 1
  fi
  if command -v jq >/dev/null 2>&1; then
    jq -r '.profiles[0].id // empty' "$cfg"
    return 0
  fi
  awk '
    $0 ~ /"profiles"[[:space:]]*:/ {in_profiles=1}
    in_profiles && $0 ~ /"id"[[:space:]]*:/ {
      gsub(/.*"id"[[:space:]]*:[[:space:]]*"/,"");
      gsub(/".*/,"");
      print;
      exit;
    }
  ' "$cfg"
}

count_proxy_profiles() {
  local cfg="$1"
  if [ -z "$cfg" ] || [ ! -f "$cfg" ]; then
    echo 0
    return 0
  fi
  if command -v jq >/dev/null 2>&1; then
    jq -r '.profiles | length' "$cfg"
    return 0
  fi
  awk '
    $0 ~ /"profiles"[[:space:]]*:/ {in_profiles=1}
    in_profiles && $0 ~ /"id"[[:space:]]*:/ {count++}
    END {print count+0}
  ' "$cfg"
}

prepare_proxy() {
  if [ "$use_proxy" -eq 0 ]; then
    return 1
  fi
  if ! command -v "$proxy_bin" >/dev/null 2>&1; then
    return 1
  fi
  if [ -z "$proxy_config" ]; then
    proxy_config="$(default_proxy_config)"
  fi
  if [ -z "$proxy_config" ] || [ ! -f "$proxy_config" ]; then
    return 1
  fi
  if [ -z "$proxy_profile" ]; then
    local count
    count="$(count_proxy_profiles "$proxy_config")"
    if [ "$count" -ge 1 ]; then
      proxy_profile="$(guess_proxy_profile "$proxy_config" || true)"
      if [ "$count" -gt 1 ] && [ -n "$proxy_profile" ]; then
        echo "Warning: multiple claude-proxy profiles found; using first (${proxy_profile})." >&2
      fi
    else
      return 1
    fi
  fi
  if [ -z "$proxy_profile" ]; then
    return 1
  fi
  return 0
}

fetch_direct() {
  "$downloader" "${downloader_args[@]}" "$1"
}

fetch_via_proxy() {
  local url="$1"
  local -a cmd
  cmd=("$proxy_bin")
  if [ -n "$proxy_config" ]; then
    cmd+=(--config "$proxy_config")
  fi
  if [ -n "$proxy_profile" ]; then
    cmd+=(--exe-patch-enabled=false run "$proxy_profile" -- "$downloader")
  else
    cmd+=(--exe-patch-enabled=false run -- "$downloader")
  fi
  cmd+=("${downloader_args[@]}" "$url")
  "${cmd[@]}"
}

fetch_with_fallback() {
  local url="$1"
  local out
  if out="$(fetch_direct "$url" 2>/dev/null)"; then
    echo "$out"
    return 0
  fi
  if prepare_proxy; then
    if out="$(fetch_via_proxy "$url" 2>/dev/null)"; then
      echo "$out"
      return 0
    fi
  fi
  return 1
}

fetch_proxy_only() {
  local url="$1"
  local out
  if prepare_proxy; then
    if out="$(fetch_via_proxy "$url" 2>/dev/null)"; then
      echo "$out"
      return 0
    fi
  fi
  return 1
}

extract_bucket_from_sh() {
  local script="$1"
  local line
  while IFS= read -r line; do
    case "$line" in
      GCS_BUCKET=*)
        line="${line#GCS_BUCKET=}"
        line="${line#\"}"
        line="${line%\"}"
        printf "%s" "$line"
        return 0
        ;;
    esac
  done <<<"$script"
  return 1
}

extract_bucket_from_ps1() {
  local script="$1"
  local line
  while IFS= read -r line; do
    case "$line" in
      '$GCS_BUCKET'*)
        line="${line#*=}"
        line="${line# }"
        line="${line#\"}"
        line="${line%\"}"
        printf "%s" "$line"
        return 0
        ;;
    esac
  done <<<"$script"
  return 1
}

install_script=""
if ! install_script="$(fetch_with_fallback "$install_sh_url")"; then
  install_script=""
fi

bucket=""
if [ -n "$install_script" ]; then
  bucket="$(extract_bucket_from_sh "$install_script" || true)"
fi

if [ -z "$bucket" ]; then
  if install_script="$(fetch_proxy_only "$install_sh_url" || true)"; then
    bucket="$(extract_bucket_from_sh "$install_script" || true)"
  fi
fi

if [ -z "$bucket" ]; then
  ps1_script="$(fetch_with_fallback "$install_ps1_url" || true)"
  if [ -n "${ps1_script:-}" ]; then
    bucket="$(extract_bucket_from_ps1 "$ps1_script" || true)"
  fi
fi

if [ -z "$bucket" ]; then
  if ps1_script="$(fetch_proxy_only "$install_ps1_url" || true)"; then
    bucket="$(extract_bucket_from_ps1 "$ps1_script" || true)"
  fi
fi

if [ -z "$bucket" ]; then
  echo "Failed to determine GCS bucket from install scripts." >&2
  exit 1
fi

bucket="${bucket%/}"
latest_version="$(fetch_with_fallback "${bucket}/latest" || true)"
latest_version="$(printf "%s" "$latest_version" | tr -d '\r\n' || true)"

if [ "$json" -eq 1 ]; then
  cat <<EOF
{
  "install_sh_url": "$(printf "%s" "$install_sh_url")",
  "install_ps1_url": "$(printf "%s" "$install_ps1_url")",
  "gcs_bucket": "$(printf "%s" "$bucket")",
  "latest_version": "$(printf "%s" "$latest_version")",
  "latest_manifest_url": "$(printf "%s/%s/manifest.json" "$bucket" "$latest_version")",
  "platform_binary_url_template": "$(printf "%s/{version}/{platform}/claude" "$bucket")"
}
EOF
else
  echo "install_sh_url=${install_sh_url}"
  echo "install_ps1_url=${install_ps1_url}"
  echo "gcs_bucket=${bucket}"
  if [ -n "$latest_version" ]; then
    echo "latest_version=${latest_version}"
    echo "latest_manifest_url=${bucket}/${latest_version}/manifest.json"
  fi
  echo "platform_binary_url_template=${bucket}/{version}/{platform}/claude"
fi
