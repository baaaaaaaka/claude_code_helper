#!/usr/bin/env bash
set -euo pipefail

OUT_DIR="${OUT_DIR:-$(pwd)/dist/glibc-release-verify}"
GLIBC_COMPAT_REPO="${GLIBC_COMPAT_REPO:-baaaaaaaka/claude_code_helper}"
GLIBC_COMPAT_TAG="${GLIBC_COMPAT_TAG:-glibc-compat-v2.31}"
GLIBC_COMPAT_ASSET="${GLIBC_COMPAT_ASSET:-glibc-2.31-centos7-runtime-x86_64.tar.xz}"
PATCHELF_REPO="${PATCHELF_REPO:-${GLIBC_COMPAT_REPO}}"
PATCHELF_TAG="${PATCHELF_TAG:-${GLIBC_COMPAT_TAG}}"
PATCHELF_ASSET="${PATCHELF_ASSET:-patchelf-linux-x86_64-static}"
RELEASE_BASE_URL="${RELEASE_BASE_URL:-https://github.com}"

download_asset() {
  local repo="$1"
  local tag="$2"
  local asset="$3"
  local target="$4"
  local url="${RELEASE_BASE_URL%/}/${repo}/releases/download/${tag}/${asset}"

  curl -fsSL "$url" -o "$target"
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
  local asset="$1"
  local checksum="${asset}.sha256"
  local expected actual

  expected="$(checksum_token "$checksum")"
  if [[ -z "$expected" ]]; then
    echo "missing checksum token in ${checksum}" >&2
    exit 1
  fi
  actual="$(sha256_file "$asset")"
  if [[ "$actual" != "$expected" ]]; then
    echo "checksum mismatch for ${asset}" >&2
    echo "expected=${expected}" >&2
    echo "actual=${actual}" >&2
    exit 1
  fi
}

mkdir -p "$OUT_DIR"

glibc_path="${OUT_DIR}/${GLIBC_COMPAT_ASSET}"
patchelf_path="${OUT_DIR}/${PATCHELF_ASSET}"

download_asset "$GLIBC_COMPAT_REPO" "$GLIBC_COMPAT_TAG" "$GLIBC_COMPAT_ASSET" "$glibc_path"
download_asset "$GLIBC_COMPAT_REPO" "$GLIBC_COMPAT_TAG" "${GLIBC_COMPAT_ASSET}.sha256" "${glibc_path}.sha256"
verify_asset_checksum "$glibc_path"

download_asset "$PATCHELF_REPO" "$PATCHELF_TAG" "$PATCHELF_ASSET" "$patchelf_path"
download_asset "$PATCHELF_REPO" "$PATCHELF_TAG" "${PATCHELF_ASSET}.sha256" "${patchelf_path}.sha256"
verify_asset_checksum "$patchelf_path"

chmod +x "$patchelf_path"
if ! "$patchelf_path" --version >/dev/null 2>&1; then
  echo "downloaded patchelf helper failed --version: ${patchelf_path}" >&2
  exit 1
fi

echo "verified glibc asset: ${glibc_path}"
echo "verified patchelf asset: ${patchelf_path}"
