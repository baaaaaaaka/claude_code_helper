#!/usr/bin/env bash
set -euo pipefail

test_bin="${TEST_BIN_PATH:-/dist/claude_cli_test}"
needs_patchelf="${CLAUDE_PATCH_NEEDS_PATCHELF:-0}"

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
    pkgs=(ca-certificates)
    if [[ "$needs_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
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
    pkgs=(ca-certificates)
    if [[ "$needs_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    retry_cmd dnf -y --setopt=retries=3 install "${pkgs[@]}"
    return
  fi

  if command -v yum >/dev/null 2>&1; then
    if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
      sed -i 's/^mirrorlist=/#mirrorlist=/g' /etc/yum.repos.d/CentOS-Base.repo || true
      sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-Base.repo || true
    fi
    retry_cmd yum -y --setopt=retries=3 install ca-certificates
    if [[ "$needs_patchelf" == "1" ]]; then
      retry_cmd yum -y --setopt=retries=3 install epel-release
      retry_cmd yum -y --setopt=retries=3 install patchelf
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

"$test_bin" -test.run '^TestClaudePatch(Integration(|RetriesKnownFailure)|RulesIntegration|BypassRuntimeIntegration)$' -test.count=1 -test.v
