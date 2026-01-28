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
  ./install.sh --version v0.0.22
  ./install.sh --dir "$HOME/.local/bin"
  ./install.sh --repo baaaaaaaka/claude_code_helper --version v0.0.22
EOF
}

repo="${CLAUDE_PROXY_REPO:-baaaaaaaka/claude_code_helper}"
version="${CLAUDE_PROXY_VERSION:-latest}"
install_dir="${CLAUDE_PROXY_INSTALL_DIR:-${HOME:-}/.local/bin}"
api_base="${CLAUDE_PROXY_API_BASE:-https://api.github.com}"
release_base="${CLAUDE_PROXY_RELEASE_BASE:-https://github.com}"
api_base="${api_base%/}"
release_base="${release_base%/}"

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

shell_name="$(basename "${SHELL:-}")"
if [ -z "${shell_name:-}" ]; then
  shell_name="sh"
fi

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

CONFIG_UPDATED=0
SOURCE_FILES=""

add_source_file() {
  file="$1"
  case " $SOURCE_FILES " in
    *" $file "*) ;;
    *) SOURCE_FILES="$SOURCE_FILES $file" ;;
  esac
}

ensure_line() {
  file="$1"
  line="$2"
  if [ -z "${file:-}" ] || [ -z "${line:-}" ]; then
    return 0
  fi
  dir="$(dirname "$file")"
  if [ -n "${dir:-}" ]; then
    mkdir -p "$dir" 2>/dev/null || true
  fi
  if [ ! -f "$file" ]; then
    : > "$file"
  fi
  if ! grep -Fqx "$line" "$file" 2>/dev/null; then
    printf "\n%s\n" "$line" >> "$file"
    CONFIG_UPDATED=1
    add_source_file "$file"
  fi
}

path_has_dir() {
  target="${1%/}"
  old_ifs="$IFS"
  IFS=":"
  for part in $PATH; do
    part="${part%/}"
    if [ "$part" = "$target" ]; then
      IFS="$old_ifs"
      return 0
    fi
  done
  IFS="$old_ifs"
  return 1
}

source_config_file() {
  file="$1"
  if [ -z "${file:-}" ] || [ ! -f "$file" ]; then
    return 0
  fi
  case "$shell_name" in
    bash)
      if have_cmd bash; then
        bash -c ". \"$file\"" >/dev/null 2>&1 || true
      fi
      ;;
    zsh)
      if have_cmd zsh; then
        zsh -c "source \"$file\"" >/dev/null 2>&1 || true
      fi
      ;;
    fish)
      if have_cmd fish; then
        fish -c "source \"$file\"" >/dev/null 2>&1 || true
      fi
      ;;
    *)
      . "$file" >/dev/null 2>&1 || true
      ;;
  esac
}

update_shell_config() {
  if [ -z "${HOME:-}" ]; then
    return 0
  fi

  install_dir_resolved="$install_dir"
  if [ -d "$install_dir" ]; then
    resolved="$(cd "$install_dir" 2>/dev/null && pwd -P || true)"
    if [ -n "${resolved:-}" ]; then
      install_dir_resolved="$resolved"
    fi
  fi

  path_needs_update=1
  if path_has_dir "$install_dir" || path_has_dir "$install_dir_resolved"; then
    path_needs_update=0
  fi

  path_line="export PATH=\"$install_dir:\$PATH\""
  alias_line="alias clp='claude-proxy'"
  path_file=""
  alias_file=""

  case "$shell_name" in
    bash)
      if [ "$os" = "darwin" ]; then
        path_file="$HOME/.bash_profile"
      else
        if [ -f "$HOME/.bashrc" ]; then
          path_file="$HOME/.bashrc"
        elif [ -f "$HOME/.bash_profile" ]; then
          path_file="$HOME/.bash_profile"
        else
          path_file="$HOME/.bashrc"
        fi
      fi
      alias_file="$path_file"
      ;;
    zsh)
      if [ "$os" = "darwin" ] || [ -f "$HOME/.zprofile" ]; then
        path_file="$HOME/.zprofile"
      else
        path_file="$HOME/.zshrc"
      fi
      alias_file="$HOME/.zshrc"
      ;;
    fish)
      path_file="$HOME/.config/fish/config.fish"
      alias_file="$path_file"
      path_line="set -gx PATH \"$install_dir\" \$PATH"
      alias_line="alias clp \"claude-proxy\""
      ;;
    *)
      path_file="$HOME/.profile"
      alias_file="$path_file"
      ;;
  esac

  if [ "$path_needs_update" -eq 1 ]; then
    ensure_line "$path_file" "$path_line"
  fi
  ensure_line "$alias_file" "$alias_line"

  if [ "$CONFIG_UPDATED" -eq 1 ]; then
    for file in $SOURCE_FILES; do
      source_config_file "$file"
    done
  fi
}

get_latest_tag_from_redirect() {
  url="$release_base/$repo/releases/latest"
  tag=""

  if have_cmd curl; then
    if final="$(curl -fsSL -o /dev/null -w '%{url_effective}' "$url")"; then
      tag="${final##*/}"
      if [ -n "${tag:-}" ] && [ "$tag" != "latest" ]; then
        printf "%s" "$tag"
        return 0
      fi
    fi
  fi

  if have_cmd wget; then
    headers="$(wget -qO /dev/null --max-redirect=0 --server-response "$url" 2>&1 || true)"
    if [ -n "${headers:-}" ]; then
      if have_cmd awk; then
        location="$(printf "%s" "$headers" | awk '/^  Location: /{print $2}' | head -n 1)"
      elif have_cmd sed; then
        location="$(printf "%s" "$headers" | sed -n 's/^  Location: //p' | head -n 1)"
      else
        location=""
      fi
      location="$(printf "%s" "$location" | tr -d '\r')"
      case "$location" in
        http*) final="$location" ;;
        /*) final="https://github.com$location" ;;
        *) final="" ;;
      esac
      tag="${final##*/}"
      if [ -n "${tag:-}" ] && [ "$tag" != "latest" ]; then
        printf "%s" "$tag"
        return 0
      fi
    fi
  fi

  return 1
}

get_latest_tag() {
  tmp="$1"
  tag=""
  if http_get "$api_base/repos/$repo/releases/latest" "$tmp"; then
    if have_cmd sed; then
      tag="$(sed -n 's/.*\"tag_name\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p' "$tmp" | head -n 1 || true)"
    fi
  fi
  if [ -n "${tag:-}" ]; then
    printf "%s" "$tag"
    return 0
  fi
  if tag="$(get_latest_tag_from_redirect)"; then
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
url="$release_base/$repo/releases/download/$version/$asset"
checksums_url="$release_base/$repo/releases/download/$version/checksums.txt"

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

clp_dst="$install_dir/clp"
if have_cmd ln; then
  ln -sf "$dst" "$clp_dst" 2>/dev/null || true
fi
if [ ! -f "$clp_dst" ]; then
  cp -f "$dst" "$clp_dst" 2>/dev/null || true
fi
chmod 0755 "$clp_dst" 2>/dev/null || true

echo "Installed: $dst"
update_shell_config
echo "Run: $dst proxy doctor"
echo "Shell config checked for PATH and alias 'clp' (reload attempted)"

