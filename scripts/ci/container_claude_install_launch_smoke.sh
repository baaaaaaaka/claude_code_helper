#!/usr/bin/env bash
set -euo pipefail

test_bin="${TEST_BIN_PATH:-/dist/claude_cli_test}"
test_name="${CLAUDE_INSTALL_TEST_NAME:-TestClaudeInstallLaunchIntegration}"
needs_patchelf="${CLAUDE_INSTALL_NEEDS_PATCHELF:-0}"
needs_tar="${CLAUDE_INSTALL_NEEDS_TAR:-0}"

retry_cmd() {
  local max_attempts="${CI_RETRY_ATTEMPTS:-5}"
  local delay="${CI_RETRY_DELAY_SECONDS:-5}"
  local attempt=1

  while true; do
    if "$@"; then
      return 0
    fi
    if [[ "$attempt" -ge "$max_attempts" ]]; then
      echo "Command failed after ${attempt} attempts: $*" >&2
      return 1
    fi
    echo "Command failed (attempt ${attempt}/${max_attempts}): $*" >&2
    sleep "$delay"
    attempt=$((attempt + 1))
  done
}

install_deps() {
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    pkgs=(ca-certificates curl wget)
    if [[ "$needs_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    if [[ "$needs_tar" == "1" ]]; then
      pkgs+=(tar)
    fi
    retry_cmd apt-get \
      -o Acquire::Retries=3 \
      -o Acquire::http::Timeout=30 \
      -o Acquire::https::Timeout=30 \
      update
    retry_cmd apt-get \
      -o Acquire::Retries=3 \
      -o Acquire::http::Timeout=30 \
      -o Acquire::https::Timeout=30 \
      install -y --no-install-recommends "${pkgs[@]}"
    return
  fi

  if command -v dnf >/dev/null 2>&1; then
    pkgs=(ca-certificates curl wget)
    if [[ "$needs_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    if [[ "$needs_tar" == "1" ]]; then
      pkgs+=(tar)
    fi
    retry_cmd dnf -y --setopt=retries=3 install "${pkgs[@]}"
    return
  fi

  if command -v yum >/dev/null 2>&1; then
    # CentOS 7 is EOL; make sure yum uses vault if mirrorlist is broken.
    if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
      sed -i 's/^mirrorlist=/#mirrorlist=/g' /etc/yum.repos.d/CentOS-Base.repo || true
      sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-Base.repo || true
    fi
    pkgs=(ca-certificates curl wget)
    if [[ "$needs_tar" == "1" ]]; then
      pkgs+=(tar)
    fi
    retry_cmd yum -y --setopt=retries=3 install "${pkgs[@]}"
    if [[ "$needs_patchelf" == "1" ]]; then
      retry_cmd yum -y --setopt=retries=3 install epel-release
      retry_cmd yum -y --setopt=retries=3 install patchelf
    fi
    return
  fi

  echo "No supported package manager found inside container" >&2
  exit 1
}

apply_proxy_env() {
  local proxy_url="${CLAUDE_INSTALL_TEST_PROXY_URL:-}"
  if [[ -z "$proxy_url" ]]; then
    return
  fi

  export HTTP_PROXY="$proxy_url"
  export HTTPS_PROXY="$proxy_url"
  export http_proxy="$proxy_url"
  export https_proxy="$proxy_url"

  local base_no_proxy="${NO_PROXY:-${no_proxy:-}}"
  local required="localhost,127.0.0.1,::1"
  if [[ -n "$base_no_proxy" ]]; then
    export NO_PROXY="${base_no_proxy},${required}"
  else
    export NO_PROXY="$required"
  fi
  export no_proxy="$NO_PROXY"

  echo "Container smoke uses install proxy: ${proxy_url}"
}

if [[ ! -x "$test_bin" ]]; then
  echo "Missing or non-executable test binary: ${test_bin}" >&2
  exit 1
fi

echo "Running Claude install+launch smoke in container"

install_deps
apply_proxy_env

export CLAUDE_INSTALL_TEST=1
if [[ -z "${CI:-}" ]]; then
  export CI=true
fi

"$test_bin" -test.run "$test_name" -test.count=1 -test.v
