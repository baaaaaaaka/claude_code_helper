#!/usr/bin/env bash
set -euo pipefail

test_bin="${TEST_BIN_PATH:-/dist/claude_cli_test}"

install_deps() {
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends ca-certificates curl wget
    return
  fi

  if command -v dnf >/dev/null 2>&1; then
    dnf -y install ca-certificates curl wget
    return
  fi

  if command -v yum >/dev/null 2>&1; then
    # CentOS 7 is EOL; make sure yum uses vault if mirrorlist is broken.
    if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
      sed -i 's/^mirrorlist=/#mirrorlist=/g' /etc/yum.repos.d/CentOS-Base.repo || true
      sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-Base.repo || true
    fi
    yum -y install ca-certificates curl wget
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

"$test_bin" -test.run TestClaudeInstallLaunchIntegration -test.count=1 -test.v
