#!/usr/bin/env bash
set -euo pipefail

PATCHELF_SOURCE_URL="${PATCHELF_SOURCE_URL:-https://github.com/NixOS/patchelf/releases/download/0.18.0/patchelf-0.18.0-x86_64.tar.gz}"
PATCHELF_SOURCE_SHA256="${PATCHELF_SOURCE_SHA256:-ce84f2447fb7a8679e58bc54a20dc2b01b37b5802e12c57eece772a6f14bf3f0}"
OUT_DIR="${OUT_DIR:-$(pwd)/dist/glibc-compat}"
PATCHELF_ASSET="${PATCHELF_ASSET:-patchelf-linux-x86_64-static}"
LDD_BIN="${LDD_BIN:-ldd}"

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi
if ! command -v tar >/dev/null 2>&1; then
  echo "tar is required" >&2
  exit 1
fi
if ! command -v "$LDD_BIN" >/dev/null 2>&1; then
  echo "$LDD_BIN is required" >&2
  exit 1
fi

checksum_cmd=""
if command -v sha256sum >/dev/null 2>&1; then
  checksum_cmd="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  checksum_cmd="shasum -a 256"
else
  echo "sha256sum or shasum is required" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

curl -fsSL -o "$workdir/patchelf.tar.gz" "$PATCHELF_SOURCE_URL"
downloaded_sha="$($checksum_cmd "$workdir/patchelf.tar.gz" | awk '{print tolower($1)}')"
expected_sha="$(printf '%s' "$PATCHELF_SOURCE_SHA256" | tr '[:upper:]' '[:lower:]')"
if [[ -z "$expected_sha" ]]; then
  echo "PATCHELF_SOURCE_SHA256 must not be empty" >&2
  exit 1
fi
if [[ "$downloaded_sha" != "$expected_sha" ]]; then
  echo "PATCHELF_SOURCE_SHA256 mismatch for $PATCHELF_SOURCE_URL" >&2
  echo "expected=$expected_sha" >&2
  echo "actual=$downloaded_sha" >&2
  exit 1
fi
tar -xzf "$workdir/patchelf.tar.gz" -C "$workdir"

source_path="$workdir/bin/patchelf"
if [[ ! -f "$source_path" ]]; then
  echo "patchelf binary not found in archive from $PATCHELF_SOURCE_URL" >&2
  exit 1
fi

target_path="$OUT_DIR/$PATCHELF_ASSET"
install -m 0755 "$source_path" "$target_path"

ldd_output="$("$LDD_BIN" "$target_path" 2>&1 || true)"
if ! grep -Eq 'not a dynamic executable|statically linked' <<<"$ldd_output"; then
  echo "expected $PATCHELF_ASSET to be statically linked" >&2
  exit 1
fi

checksum_path="${target_path}.sha256"
rm -f "$checksum_path"
$checksum_cmd "$target_path" > "$checksum_path"
