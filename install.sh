#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
claude-proxy installer (no root required)

Usage:
  ./install.sh [--repo owner/name] [--version vX.Y.Z|X.Y.Z|latest] [--dir <install-dir>]

Defaults:
  --repo    baaaaaaaka/claude_code_helper
  --version latest
  --dir     $HOME/.local/bin

Examples:
  ./install.sh
  ./install.sh --version v0.0.1
  ./install.sh --dir "$HOME/.local/bin"
  ./install.sh --repo baaaaaaaka/claude_code_helper --version v0.0.1
EOF
}

repo="${CLAUDE_PROXY_REPO:-baaaaaaaka/claude_code_helper}"
version="${CLAUDE_PROXY_VERSION:-latest}"
install_dir="${CLAUDE_PROXY_INSTALL_DIR:-${HOME:-}/.local/bin}"

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --repo)
      repo="$2"
      shift 2
      ;;
    --version)
      version="$2"
      shift 2
      ;;
    --dir)
      install_dir="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

os="$(uname -s 2>/dev/null || echo unknown)"
arch="$(uname -m 2>/dev/null || echo unknown)"

case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *)
    echo "Unsupported OS: $os" >&2
    exit 1
    ;;
esac

case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *)
    echo "Unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

have_cmd() { command -v "$1" >/dev/null 2>&1; }

http_get() {
  url="$1"
  out="$2"
  if have_cmd curl; then
    curl -fsSL -o "$out" "$url"
    return 0
  fi
  if have_cmd wget; then
    wget -q -O "$out" "$url"
    return 0
  fi
  echo "Missing downloader: need curl or wget" >&2
  return 1
}

get_latest_tag() {
  tmp="$1"
  http_get "https://api.github.com/repos/$repo/releases/latest" "$tmp"
  if have_cmd sed; then
    tag="$(sed -n 's/.*\"tag_name\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p' "$tmp" | head -n 1 || true)"
    if [ -n "${tag:-}" ]; then
      printf "%s" "$tag"
      return 0
    fi
  fi
  echo "Failed to determine latest version automatically; pass --version vX.Y.Z" >&2
  return 1
}

tmpdir="$(mktemp -d 2>/dev/null || mktemp -d -t claude-proxy)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

if [ "$version" = "latest" ] || [ -z "${version:-}" ]; then
  version="$(get_latest_tag "$tmpdir/latest.json")"
fi

ver_nov="${version#v}"
asset="claude-proxy_${ver_nov}_${os}_${arch}"
url="https://github.com/$repo/releases/download/$version/$asset"
checksums_url="https://github.com/$repo/releases/download/$version/checksums.txt"

bin_tmp="$tmpdir/$asset"
http_get "$url" "$bin_tmp"

# Optional checksum verification.
if have_cmd sha256sum || have_cmd shasum; then
  http_get "$checksums_url" "$tmpdir/checksums.txt" || true
  if [ -s "$tmpdir/checksums.txt" ] && have_cmd awk; then
    expected="$(awk -v a="$asset" '$2==a {print $1}' "$tmpdir/checksums.txt" | head -n 1 || true)"
    if [ -n "${expected:-}" ]; then
      if have_cmd sha256sum; then
        actual="$(sha256sum "$bin_tmp" | awk '{print $1}')"
      else
        actual="$(shasum -a 256 "$bin_tmp" | awk '{print $1}')"
      fi
      if [ "$expected" != "$actual" ]; then
        echo "Checksum mismatch for $asset" >&2
        echo "Expected: $expected" >&2
        echo "Actual:   $actual" >&2
        exit 1
      fi
    fi
  fi
fi

mkdir -p "$install_dir"
chmod 0755 "$bin_tmp" 2>/dev/null || true

dst="$install_dir/claude-proxy"
mv -f "$bin_tmp" "$dst"

echo "Installed: $dst"
echo "Run: $dst proxy doctor"

