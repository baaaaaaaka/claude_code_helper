#!/usr/bin/env bash
set -euo pipefail

test_bin="${TEST_BIN_PATH:-/dist/claude_cli_test}"
test_name="${CLAUDE_INSTALL_TEST_NAME:-TestClaudeInstallLaunchIntegration}"
needs_patchelf="${CLAUDE_INSTALL_NEEDS_PATCHELF:-0}"
needs_tar="${CLAUDE_INSTALL_NEEDS_TAR:-0}"
patchelf_helper_path="${CLAUDE_PROXY_PATCHELF_PATH:-}"
helper_bin_dir=""

cleanup() {
  if [[ -n "$helper_bin_dir" ]]; then
    rm -rf "$helper_bin_dir"
  fi
}
trap cleanup EXIT

prepare_patchelf_helper() {
  if [[ -z "$patchelf_helper_path" ]]; then
    return
  fi
  if [[ ! -x "$patchelf_helper_path" ]]; then
    echo "Configured CLAUDE_PROXY_PATCHELF_PATH is not executable: ${patchelf_helper_path}" >&2
    exit 1
  fi
  helper_bin_dir="$(mktemp -d)"
  ln -sf "$patchelf_helper_path" "$helper_bin_dir/patchelf"
  export PATH="$helper_bin_dir:$PATH"
}

install_deps() {
  local install_system_patchelf=0
  if [[ "$needs_patchelf" == "1" && -z "$patchelf_helper_path" ]]; then
    install_system_patchelf=1
  fi
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    pkgs=(ca-certificates curl wget)
    if [[ "$install_system_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    if [[ "$needs_tar" == "1" ]]; then
      pkgs+=(tar)
    fi
    apt-get update
    apt-get install -y --no-install-recommends "${pkgs[@]}"
    return
  fi

  if command -v dnf >/dev/null 2>&1; then
    pkgs=(ca-certificates curl wget)
    if [[ "$install_system_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    if [[ "$needs_tar" == "1" ]]; then
      pkgs+=(tar)
    fi
    dnf -y install "${pkgs[@]}"
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
    yum -y install "${pkgs[@]}"
    if [[ "$install_system_patchelf" == "1" ]]; then
      yum -y install epel-release
      yum -y install patchelf
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

prepare_patchelf_helper
install_deps
apply_proxy_env

export CLAUDE_INSTALL_TEST=1
if [[ -z "${CI:-}" ]]; then
  export CI=true
fi

"$test_bin" -test.run "$test_name" -test.count=1 -test.v
