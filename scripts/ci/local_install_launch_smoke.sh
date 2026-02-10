#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
artifacts_dir=""
test_bin=""
cleanup_artifacts_dir=false

detect_local_clp_proxy_port() {
  local bin
  local port
  for bin in clp claude-proxy; do
    if ! command -v "$bin" >/dev/null 2>&1; then
      continue
    fi
    port="$("$bin" proxy list 2>/dev/null | awk 'NR>1 && $6=="alive" && $4 ~ /^[0-9]+$/ { print $4; exit }')"
    if [[ -n "$port" ]]; then
      echo "$port"
      return 0
    fi
  done
  return 1
}

resolve_proxy_url() {
  if [[ -n "${CLAUDE_INSTALL_TEST_PROXY_URL:-}" ]]; then
    echo "$CLAUDE_INSTALL_TEST_PROXY_URL"
    return 0
  fi

  local default_host="host.docker.internal"
  if [[ "${LOCAL_INSTALL_SMOKE_USE_HOST_NETWORK:-}" == "1" ]]; then
    default_host="127.0.0.1"
  fi
  local host="${CLP_PROXY_HOST:-${default_host}}"
  if [[ -n "${CLP_PROXY_PORT:-}" ]]; then
    echo "http://${host}:${CLP_PROXY_PORT}"
    return 0
  fi

  local detected_port
  if detected_port="$(detect_local_clp_proxy_port)"; then
    echo "http://${host}:${detected_port}"
    return 0
  fi

  return 1
}

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for local install smoke" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required for local install smoke" >&2
  exit 1
fi

if [[ -n "${LOCAL_INSTALL_SMOKE_ARTIFACT_DIR:-}" ]]; then
  artifacts_dir="${LOCAL_INSTALL_SMOKE_ARTIFACT_DIR}"
else
  artifacts_dir="$(mktemp -d "${TMPDIR:-/tmp}/claude-install-smoke.XXXXXX")"
  cleanup_artifacts_dir=true
fi
test_bin="${artifacts_dir}/claude_cli_test"

cleanup() {
  if [[ "$cleanup_artifacts_dir" == "true" ]]; then
    rm -rf "$artifacts_dir"
  fi
}
trap cleanup EXIT

proxy_url=""
if ! proxy_url="$(resolve_proxy_url)"; then
  cat <<'EOF' >&2
Could not determine local clp proxy URL.

Use one of:
  1) Start a local proxy instance so `clp proxy list` (or `claude-proxy proxy list`) shows an alive HTTP port
  2) Export CLP_PROXY_PORT=<port>
  3) Export CLAUDE_INSTALL_TEST_PROXY_URL=http://127.0.0.1:<port>
EOF
  exit 1
fi

echo "Using local install proxy: ${proxy_url}"

mkdir -p "$artifacts_dir"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test ./internal/cli -c -o "$test_bin"
)
chmod +x "$test_bin"

images="${LOCAL_INSTALL_SMOKE_IMAGES:-rockylinux:8 ubuntu:20.04}"
docker_network_args=()
if [[ "${LOCAL_INSTALL_SMOKE_USE_HOST_NETWORK:-}" == "1" ]]; then
  docker_network_args+=(--network host)
else
  docker_network_args+=(--add-host host.docker.internal:host-gateway)
fi
for img in $images; do
  echo "==> ${img}"
  docker run --rm "${docker_network_args[@]}" \
    -v "${artifacts_dir}:/dist:ro" \
    -v "${repo_root}/scripts/ci:/ci:ro" \
    -e CLAUDE_INSTALL_TEST=1 \
    -e CLAUDE_INSTALL_TEST_ALLOW_LOCAL=1 \
    -e CLAUDE_INSTALL_TEST_PROXY_URL="${proxy_url}" \
    "$img" bash /ci/container_claude_install_launch_smoke.sh
done

echo "Local container install+launch smoke completed."
