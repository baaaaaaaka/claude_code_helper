#!/usr/bin/env bash
set -euo pipefail

CLAUDE_PROXY_BIN="${CLAUDE_PROXY_BIN:-$(pwd)/dist/claude-proxy}"
CLAUDE_CLI_TEST_BIN="${CLAUDE_CLI_TEST_BIN:-$(pwd)/dist/claude_cli_test}"
GLIBC_COMPAT_REPO="${GLIBC_COMPAT_REPO:-baaaaaaaka/claude_code_helper}"
GLIBC_COMPAT_TAG="${GLIBC_COMPAT_TAG:-glibc-compat-v2.31.1}"
GLIBC_COMPAT_BUNDLE="${GLIBC_COMPAT_BUNDLE:-}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi
if [[ ! -x "$CLAUDE_PROXY_BIN" ]]; then
  echo "missing claude-proxy binary at $CLAUDE_PROXY_BIN" >&2
  exit 1
fi
if [[ ! -x "$CLAUDE_CLI_TEST_BIN" ]]; then
  echo "missing claude_cli_test binary at $CLAUDE_CLI_TEST_BIN" >&2
  exit 1
fi

tmp_dirs=()

cleanup() {
  local dir
  for dir in "${tmp_dirs[@]}"; do
    chmod -R u+rwX "$dir" 2>/dev/null || true
    rm -rf "$dir" || true
  done
}
trap cleanup EXIT

run_centos7_install_recovery() {
  local shared_dir="$1"

  echo "==> centos:7 (install recovery on shared home)"
  docker run --rm \
    -v "${repo_root}/dist:/dist:ro" \
    -v "${repo_root}/scripts/ci:/ci:ro" \
    -v "${shared_dir}:/shared" \
    -e TEST_BIN_PATH=/dist/claude_cli_test \
    -e CLAUDE_INSTALL_TEST=1 \
    -e CLAUDE_INSTALL_TEST_EL7_GLIBC_RECOVERY=1 \
    -e CLAUDE_INSTALL_TEST_NAME=TestClaudeInstallEL7RecoveryIntegration \
    -e CLAUDE_INSTALL_TEST_HOME=/shared/home \
    -e CLAUDE_INSTALL_TEST_HOST_ID=centos7-host \
    -e CLAUDE_INSTALL_NEEDS_PATCHELF=1 \
    -e CLAUDE_INSTALL_NEEDS_TAR=1 \
    -e CLAUDE_PROXY_GLIBC_COMPAT_REPO="${GLIBC_COMPAT_REPO}" \
    -e CLAUDE_PROXY_GLIBC_COMPAT_TAG="${GLIBC_COMPAT_TAG}" \
    -e CLAUDE_INSTALL_GLIBC_COMPAT_BUNDLE="${GLIBC_COMPAT_BUNDLE}" \
    -e CI=true \
    centos:7 bash /ci/container_claude_install_launch_smoke.sh
}

run_rocky8_shared_launcher() {
  local shared_dir="$1"
  local launcher_path="$2"

  echo "==> rockylinux:8 (direct launch via shared CentOS7 recovery launcher)"
  docker run --rm \
    -v "${repo_root}/dist:/dist:ro" \
    -v "${repo_root}/scripts/glibc:/scripts:ro" \
    -v "${shared_dir}:/shared" \
    -e CLAUDE_PROXY_BIN=/dist/claude-proxy \
    -e CLAUDE_PATH="${launcher_path}" \
    -e SHARED_HOME=/shared/home \
    -e EXPECT_MODE=direct \
    -e CLAUDE_PROXY_HOST_ID=rocky8-host \
    -e SHARED_UID="$(id -u)" \
    -e SHARED_GID="$(id -g)" \
    -e GLIBC_COMPAT_REPO="${GLIBC_COMPAT_REPO}" \
    -e GLIBC_COMPAT_TAG="${GLIBC_COMPAT_TAG}" \
    rockylinux:8 bash /scripts/shared_storage_host_smoke.sh
}

find_shared_launcher() {
  local shared_dir="$1"
  local candidate=""
  local -a candidates=(
    "${shared_dir}/home/.cache/claude-proxy/hosts/centos7-host/install-recovery/claude"
    "${shared_dir}/home/.local/bin/claude"
    "${shared_dir}/home/.claude/local/claude"
  )

  for candidate in "${candidates[@]}"; do
    if [[ -e "$candidate" || -L "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done

  return 1
}

shared_dir="$(mktemp -d "${TMPDIR:-/tmp}/claude-install-recovery-shared-storage.XXXXXX")"
tmp_dirs+=("$shared_dir")
mkdir -p "${shared_dir}/home"

run_centos7_install_recovery "$shared_dir"

launcher_host_path="$(find_shared_launcher "$shared_dir" || true)"
if [[ -z "$launcher_host_path" ]]; then
  echo "expected a shared launcher under ${shared_dir}/home after CentOS7 install" >&2
  exit 1
fi
if [[ -d "$launcher_host_path" ]]; then
  echo "shared launcher path is a directory: ${launcher_host_path}" >&2
  exit 1
fi
launcher_container_path="/shared${launcher_host_path#"${shared_dir}"}"
echo "[shared launcher] ${launcher_host_path}"

run_rocky8_shared_launcher "$shared_dir" "$launcher_container_path"

echo "PASS: shared-storage install recovery smoke completed."
