#!/usr/bin/env bash
set -euo pipefail

test_bin="${TEST_BIN_PATH:-/dist/claude_cli_test}"
needs_patchelf="${CLAUDE_PATCH_NEEDS_PATCHELF:-0}"

install_deps() {
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    pkgs=(ca-certificates)
    if [[ "$needs_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    apt-get update
    apt-get install -y --no-install-recommends "${pkgs[@]}"
    return
  fi

  if command -v dnf >/dev/null 2>&1; then
    pkgs=(ca-certificates)
    if [[ "$needs_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    dnf -y install "${pkgs[@]}"
    return
  fi

  if command -v yum >/dev/null 2>&1; then
    if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
      sed -i 's/^mirrorlist=/#mirrorlist=/g' /etc/yum.repos.d/CentOS-Base.repo || true
      sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-Base.repo || true
    fi
    yum -y install ca-certificates
    if [[ "$needs_patchelf" == "1" ]]; then
      yum -y install epel-release
      yum -y install patchelf
    fi
    return
  fi

  echo "No supported package manager found inside container" >&2
  exit 1
}

if [[ ! -x "$test_bin" ]]; then
  echo "Missing or non-executable test binary: ${test_bin}" >&2
  exit 1
fi

if [[ -z "${CLAUDE_PATCH_TEST:-}" ]]; then
  echo "CLAUDE_PATCH_TEST must be set" >&2
  exit 1
fi

if [[ -z "${CLAUDE_PATCH_VERSION:-}" ]]; then
  echo "CLAUDE_PATCH_VERSION must be set" >&2
  exit 1
fi

if [[ -z "${CLAUDE_PATCH_BUCKET:-}" ]]; then
  echo "CLAUDE_PATCH_BUCKET must be set" >&2
  exit 1
fi

echo "Running Claude patch+TUI smoke in container for ${CLAUDE_PATCH_VERSION}"

install_deps

"$test_bin" -test.run '^TestClaudePatchIntegration(|RetriesKnownFailure)$' -test.count=1 -test.v
