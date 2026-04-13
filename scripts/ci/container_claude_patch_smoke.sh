#!/usr/bin/env bash
set -euo pipefail

test_bin="${TEST_BIN_PATH:-/dist/claude_cli_test}"
needs_patchelf="${CLAUDE_PATCH_NEEDS_PATCHELF:-0}"
patchelf_helper_path="${CLAUDE_PROXY_PATCHELF_PATH:-}"
assert_release_assets="${CLAUDE_PATCH_ASSERT_RELEASE_ASSETS:-0}"
cache_root="${CLAUDE_PATCH_CACHE_ROOT:-${XDG_CACHE_HOME:-}}"
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

prepare_cache_root() {
  if [[ -z "$cache_root" && "$assert_release_assets" == "1" ]]; then
    cache_root="/tmp/claude-proxy-cache"
  fi
  if [[ -z "$cache_root" ]]; then
    return
  fi
  mkdir -p "$cache_root"
  export XDG_CACHE_HOME="$cache_root"
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print tolower($1)}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print tolower($1)}'
    return
  fi
  echo "sha256 tool not found" >&2
  exit 1
}

checksum_token() {
  awk '{
    for (i = 1; i <= NF; i++) {
      if ($i ~ /^[0-9A-Fa-f]{64}$/) {
        print tolower($i)
        exit
      }
    }
  }' "$1"
}

verify_asset_checksum() {
  local asset_path="$1"
  local checksum_path="${asset_path}.sha256"
  if [[ ! -f "$checksum_path" ]]; then
    echo "missing checksum for ${asset_path}" >&2
    exit 1
  fi
  local expected actual
  expected="$(checksum_token "$checksum_path")"
  if [[ -z "$expected" ]]; then
    echo "missing checksum token in ${checksum_path}" >&2
    exit 1
  fi
  actual="$(sha256_file "$asset_path")"
  if [[ "$actual" != "$expected" ]]; then
    echo "checksum mismatch for ${asset_path}" >&2
    echo "expected=${expected}" >&2
    echo "actual=${actual}" >&2
    exit 1
  fi
}

assert_release_assets_downloaded() {
  if [[ "$assert_release_assets" != "1" ]]; then
    return
  fi

  local effective_cache_root="${XDG_CACHE_HOME:-${HOME:-/root}/.cache}"
  local patchelf_asset=""
  local glibc_bundle=""
  local glibc_runtime=""
  local glibc_lib_dir=""
  local compat_mirror_root=""

  patchelf_asset="$(find "$effective_cache_root/claude-proxy/tools/patchelf" -type f -name 'patchelf-linux-x86_64-static' | head -n 1 || true)"
  if [[ -z "$patchelf_asset" ]]; then
    echo "expected downloaded patchelf release asset under ${effective_cache_root}/claude-proxy/tools/patchelf" >&2
    exit 1
  fi
  verify_asset_checksum "$patchelf_asset"
  if ! "$patchelf_asset" --version >/dev/null 2>&1; then
    echo "downloaded patchelf helper is not executable: ${patchelf_asset}" >&2
    exit 1
  fi

  if [[ -n "${CLAUDE_PROXY_HOST_ID:-}" ]]; then
    compat_mirror_root="${effective_cache_root}/claude-proxy/hosts/${CLAUDE_PROXY_HOST_ID}"
  else
    compat_mirror_root="${effective_cache_root}/claude-proxy/hosts"
  fi

  glibc_bundle="$(find "$compat_mirror_root" -type f -name 'glibc-*-runtime-x86_64.tar.xz' | head -n 1 || true)"
  if [[ -z "$glibc_bundle" ]]; then
    echo "expected downloaded glibc release asset under ${compat_mirror_root}" >&2
    exit 1
  fi
  verify_asset_checksum "$glibc_bundle"

  glibc_runtime="$(find "$compat_mirror_root" -type d -path '*/glibc-compat/*/runtime' | head -n 1 || true)"
  if [[ -z "$glibc_runtime" ]]; then
    echo "expected extracted glibc runtime under ${compat_mirror_root}" >&2
    exit 1
  fi
  if [[ -f "${glibc_runtime}/lib/ld-linux-x86-64.so.2" && -f "${glibc_runtime}/lib/libc.so.6" ]]; then
    glibc_lib_dir="${glibc_runtime}/lib"
  elif [[ -f "${glibc_runtime}/glibc-2.31/lib/ld-linux-x86-64.so.2" && -f "${glibc_runtime}/glibc-2.31/lib/libc.so.6" ]]; then
    glibc_lib_dir="${glibc_runtime}/glibc-2.31/lib"
  else
    echo "expected extracted glibc loader/libc under ${glibc_runtime}" >&2
    exit 1
  fi

  if ! find "${compat_mirror_root}/claude" -type f | grep -q .; then
    echo "expected glibc compat mirror under ${compat_mirror_root}/claude" >&2
    exit 1
  fi

  echo "[release-assets] patchelf=${patchelf_asset}"
  echo "[release-assets] glibc-bundle=${glibc_bundle}"
  echo "[release-assets] glibc-lib-dir=${glibc_lib_dir}"
}

install_deps() {
  local install_system_patchelf=0
  local install_glibc_extract_tools=0
  local install_release_assert_tools=0
  if [[ "$needs_patchelf" == "1" && -z "$patchelf_helper_path" ]]; then
    install_system_patchelf=1
  fi
  if [[ "${CLAUDE_PROXY_GLIBC_COMPAT:-0}" == "1" && -z "${CLAUDE_PROXY_GLIBC_COMPAT_ROOT:-}" ]]; then
    install_glibc_extract_tools=1
  fi
  if [[ "$assert_release_assets" == "1" ]]; then
    install_release_assert_tools=1
  fi
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    pkgs=(ca-certificates)
    if [[ "$install_system_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    if [[ "$install_glibc_extract_tools" == "1" ]]; then
      pkgs+=(tar xz-utils)
    fi
    if [[ "$install_release_assert_tools" == "1" ]]; then
      pkgs+=(findutils)
    fi
    apt-get update
    apt-get install -y --no-install-recommends "${pkgs[@]}"
    return
  fi

  if command -v dnf >/dev/null 2>&1; then
    pkgs=(ca-certificates)
    if [[ "$install_system_patchelf" == "1" ]]; then
      pkgs+=(patchelf)
    fi
    if [[ "$install_glibc_extract_tools" == "1" ]]; then
      pkgs+=(tar xz)
    fi
    if [[ "$install_release_assert_tools" == "1" ]]; then
      pkgs+=(findutils)
    fi
    dnf -y install "${pkgs[@]}"
    return
  fi

  if command -v yum >/dev/null 2>&1; then
    if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
      sed -i 's/^mirrorlist=/#mirrorlist=/g' /etc/yum.repos.d/CentOS-Base.repo || true
      sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-Base.repo || true
    fi
    yum_pkgs=(ca-certificates)
    if [[ "$install_glibc_extract_tools" == "1" ]]; then
      yum_pkgs+=(tar xz)
    fi
    if [[ "$install_release_assert_tools" == "1" ]]; then
      yum_pkgs+=(findutils)
    fi
    yum -y install "${yum_pkgs[@]}"
    if [[ "$install_system_patchelf" == "1" ]]; then
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

prepare_patchelf_helper
prepare_cache_root
install_deps

"$test_bin" -test.run '^TestClaudePatch(Integration(|RetriesKnownFailure)|RulesIntegration|BypassRuntimeIntegration)$' -test.count=1 -test.v
assert_release_assets_downloaded
