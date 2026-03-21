#!/usr/bin/env bash
set -euo pipefail

CLAUDE_PROXY_BIN="${CLAUDE_PROXY_BIN:-$(pwd)/dist/claude-proxy}"
CLAUDE_VERSION="${CLAUDE_VERSION:-2.1.38}"
CLAUDE_BUCKET="${CLAUDE_BUCKET:-https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases}"
GLIBC_COMPAT_REPO="${GLIBC_COMPAT_REPO:-baaaaaaaka/claude_code_helper}"
GLIBC_COMPAT_TAG="${GLIBC_COMPAT_TAG:-glibc-compat-v2.31}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi
if [[ ! -x "$CLAUDE_PROXY_BIN" ]]; then
  echo "missing claude-proxy binary at $CLAUDE_PROXY_BIN" >&2
  exit 1
fi

tmp_dirs=()

cleanup() {
  local dir
  for dir in "${tmp_dirs[@]}"; do
    rm -rf "$dir"
  done
}
trap cleanup EXIT

prepare_shared_dir() {
  local shared_dir="$1"
  local claude_url="${CLAUDE_BUCKET}/${CLAUDE_VERSION}/linux-x64/claude"

  mkdir -p "${shared_dir}/claude" "${shared_dir}/home"
  curl -fsSL "$claude_url" -o "${shared_dir}/claude/claude"
  chmod +x "${shared_dir}/claude/claude"
}

run_host() {
  local image="$1"
  local host_id="$2"
  local expect_mode="$3"
  local shared_dir="$4"

  echo "==> ${image} (${host_id}, ${expect_mode})"
  docker run --rm \
    -v "${repo_root}/dist:/dist:ro" \
    -v "${repo_root}/scripts/glibc:/scripts:ro" \
    -v "${shared_dir}:/shared" \
    -e CLAUDE_PROXY_BIN=/dist/claude-proxy \
    -e CLAUDE_PATH=/shared/claude/claude \
    -e SHARED_HOME=/shared/home \
    -e EXPECT_MODE="${expect_mode}" \
    -e CLAUDE_PROXY_HOST_ID="${host_id}" \
    -e GLIBC_COMPAT_REPO="${GLIBC_COMPAT_REPO}" \
    -e GLIBC_COMPAT_TAG="${GLIBC_COMPAT_TAG}" \
    "${image}" bash /scripts/shared_storage_host_smoke.sh
}

verify_shared_source_unchanged() {
  local shared_dir="$1"
  local expected_sha="$2"
  local actual_sha

  actual_sha="$(sha256sum "${shared_dir}/claude/claude" | awk '{print $1}')"
  echo "[shared source sha] ${actual_sha}"
  if [[ "${actual_sha}" != "${expected_sha}" ]]; then
    echo "shared Claude source binary changed unexpectedly" >&2
    exit 1
  fi
}

run_sequence() {
  local name="$1"
  local first_image="$2"
  local first_host="$3"
  local first_mode="$4"
  local second_image="$5"
  local second_host="$6"
  local second_mode="$7"

  local shared_dir
  shared_dir="$(mktemp -d "${TMPDIR:-/tmp}/claude-shared-storage.XXXXXX")"
  tmp_dirs+=("$shared_dir")

  echo "=== ${name} ==="
  prepare_shared_dir "$shared_dir"

  local source_sha
  source_sha="$(sha256sum "${shared_dir}/claude/claude" | awk '{print $1}')"

  run_host "$first_image" "$first_host" "$first_mode" "$shared_dir"
  verify_shared_source_unchanged "$shared_dir" "$source_sha"

  run_host "$second_image" "$second_host" "$second_mode" "$shared_dir"
  verify_shared_source_unchanged "$shared_dir" "$source_sha"

  echo "PASS: ${name}"
}

run_sequence "centos7 -> rocky8" centos:7 centos7-host mirror rockylinux:8 rocky8-host direct
run_sequence "rocky8 -> centos7" rockylinux:8 rocky8-host direct centos:7 centos7-host mirror

echo "PASS: shared-storage mixed-host smoke completed."
