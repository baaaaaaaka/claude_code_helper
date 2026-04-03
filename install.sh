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
  ./install.sh --version v0.0.28
  ./install.sh --dir "$HOME/.local/bin"
  ./install.sh --repo baaaaaaaka/claude_code_helper --version v0.0.28
EOF
}

repo="${CLAUDE_PROXY_REPO:-baaaaaaaka/claude_code_helper}"
version="${CLAUDE_PROXY_VERSION:-latest}"
install_dir="${CLAUDE_PROXY_INSTALL_DIR:-${HOME:-}/.local/bin}"
api_base="${CLAUDE_PROXY_API_BASE:-https://api.github.com}"
release_base="${CLAUDE_PROXY_RELEASE_BASE:-https://github.com}"
api_base="${api_base%/}"
release_base="${release_base%/}"
tmpdir=""
install_dir_resolved="$install_dir"

cleanup() {
  if [ -n "${tmpdir:-}" ] && [ -d "$tmpdir" ]; then
    rm -rf "$tmpdir"
  fi
}

on_exit() {
  code=$?
  trap - EXIT
  cleanup
  if [ "$code" -ne 0 ]; then
    echo >&2
    echo "==================== INSTALL FAILED ====================" >&2
    echo "claude-proxy install did not complete." >&2
  fi
  exit "$code"
}

print_success() {
  echo
  echo "==================== INSTALL SUCCESS ===================="
  echo "Installed: $dst"
  echo "Installed: $clp_dst"
  echo "Run: \"$dst\" proxy doctor"
  if [ -n "${CONFIG_WARNINGS:-}" ]; then
    echo "Attention: automatic shell setup was incomplete."
    old_ifs="$IFS"
    IFS='
'
    for warning in $CONFIG_WARNINGS; do
      [ -n "${warning:-}" ] || continue
      echo "  - $warning"
    done
    IFS="$old_ifs"
    echo "To use 'clp', add \"$install_dir_resolved\" to PATH manually, then open a new shell."
  else
    echo "Shell setup checked for PATH entries and alias 'clp'."
    echo "If 'clp' is not found in this shell, open a new shell."
  fi
}

trap 'on_exit' EXIT

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
    if curl -fsSL -o "$out" "$url"; then
      return 0
    fi
  fi
  if have_cmd wget; then
    if wget -q -O "$out" "$url"; then
      return 0
    fi
  fi
  if ! have_cmd curl && ! have_cmd wget; then
    echo "Missing downloader: need curl or wget" >&2
  fi
  return 1
}

CONFIG_UPDATED=0
SOURCE_FILES=""
CONFIG_WARNINGS=""

add_source_file() {
  file="$1"
  case "
$SOURCE_FILES
" in
    *"
$file
"*) ;;
    *)
      if [ -n "$SOURCE_FILES" ]; then
        SOURCE_FILES="$SOURCE_FILES
$file"
      else
        SOURCE_FILES="$file"
      fi
      ;;
  esac
}

record_config_warning() {
  message="$1"
  if [ -z "${message:-}" ]; then
    return 0
  fi
  case "
$CONFIG_WARNINGS
" in
    *"
$message
"*) ;;
    *)
      if [ -n "$CONFIG_WARNINGS" ]; then
        CONFIG_WARNINGS="$CONFIG_WARNINGS
$message"
      else
        CONFIG_WARNINGS="$message"
      fi
      ;;
  esac
}

ensure_line() {
  file="$1"
  line="$2"
  if [ -z "${file:-}" ] || [ -z "${line:-}" ]; then
    return 0
  fi
  dir="$(dirname "$file")"
  if [ -n "${dir:-}" ] && [ ! -d "$dir" ]; then
    if ! mkdir -p "$dir" 2>/dev/null; then
      record_config_warning "Could not create shell config directory: $dir"
      return 0
    fi
  fi
  if [ ! -f "$file" ]; then
    if ! ( : > "$file" ) 2>/dev/null; then
      record_config_warning "Could not create shell config file: $file"
      return 0
    fi
  fi
  if ! grep -Fqx "$line" "$file" 2>/dev/null; then
    if ( printf "\n%s\n" "$line" >> "$file" ) 2>/dev/null; then
      CONFIG_UPDATED=1
      add_source_file "$file"
    else
      record_config_warning "Could not update shell config: $file"
    fi
  fi
}

ensure_block() {
  file="$1"
  marker="$2"
  block="$3"
  if [ -z "${file:-}" ] || [ -z "${marker:-}" ] || [ -z "${block:-}" ]; then
    return 0
  fi
  dir="$(dirname "$file")"
  if [ -n "${dir:-}" ] && [ ! -d "$dir" ]; then
    if ! mkdir -p "$dir" 2>/dev/null; then
      record_config_warning "Could not create shell config directory: $dir"
      return 0
    fi
  fi
  if [ ! -f "$file" ]; then
    if ! ( : > "$file" ) 2>/dev/null; then
      record_config_warning "Could not create shell config file: $file"
      return 0
    fi
  fi
  if ! grep -Fqx "$marker" "$file" 2>/dev/null; then
    if ( printf "\n%s\n" "$block" >> "$file" ) 2>/dev/null; then
      CONFIG_UPDATED=1
      add_source_file "$file"
    else
      record_config_warning "Could not update shell config: $file"
    fi
  fi
}

trim_trailing_slash() {
  value="$1"
  while [ -n "$value" ] && [ "$value" != "/" ] && [ "${value%/}" != "$value" ]; do
    value="${value%/}"
  done
  printf "%s" "$value"
}

same_path_string() {
  a="$(trim_trailing_slash "$1")"
  b="$(trim_trailing_slash "$2")"
  [ "$a" = "$b" ]
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

ensure_posix_path_entry() {
  file="$1"
  dir="$2"
  if [ -z "${file:-}" ] || [ -z "${dir:-}" ]; then
    return 0
  fi
  marker="# claude-proxy PATH $dir"
  block="$marker
case \":\$PATH:\" in
  *:\"$dir\":*) ;;
  *) export PATH=\"$dir:\$PATH\" ;;
esac"
  ensure_block "$file" "$marker" "$block"
}

ensure_fish_path_entry() {
  file="$1"
  dir="$2"
  if [ -z "${file:-}" ] || [ -z "${dir:-}" ]; then
    return 0
  fi
  marker="# claude-proxy PATH $dir"
  block="$marker
if not contains -- \"$dir\" \$PATH
  set -gx PATH \"$dir\" \$PATH
end"
  ensure_block "$file" "$marker" "$block"
}

ensure_csh_path_entry() {
  file="$1"
  dir="$2"
  if [ -z "${file:-}" ] || [ -z "${dir:-}" ]; then
    return 0
  fi
  marker="# claude-proxy PATH $dir"
  block="$marker
if ( \":\$PATH:\" !~ \"*:$dir:*\" ) setenv PATH \"$dir:\$PATH\""
  ensure_block "$file" "$marker" "$block"
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
    csh)
      if have_cmd csh; then
        csh -f -c "source \"$file\"" >/dev/null 2>&1 || true
      fi
      ;;
    tcsh)
      if have_cmd tcsh; then
        tcsh -f -c "source \"$file\"" >/dev/null 2>&1 || true
      elif have_cmd csh; then
        csh -f -c "source \"$file\"" >/dev/null 2>&1 || true
      fi
      ;;
    *)
      . "$file" >/dev/null 2>&1 || true
      ;;
  esac
}

update_shell_config() {
  if [ -z "${HOME:-}" ]; then
    record_config_warning "HOME is not set; skipped automatic shell setup."
    return 0
  fi

  if [ -d "$install_dir" ]; then
    resolved="$(cd "$install_dir" 2>/dev/null && pwd -P || true)"
    if [ -n "${resolved:-}" ]; then
      install_dir_resolved="$resolved"
    fi
  fi
  claude_bin_dir="$HOME/.local/bin"
  alias_line="alias clp='claude-proxy'"

  ensure_posix_path_targets() {
    file="$1"
    if [ -z "${file:-}" ]; then
      return 0
    fi
    if ! same_path_string "$claude_bin_dir" "$install_dir_resolved" && ! path_has_dir "$claude_bin_dir"; then
      ensure_posix_path_entry "$file" "$claude_bin_dir"
    fi
    if ! path_has_dir "$install_dir_resolved"; then
      ensure_posix_path_entry "$file" "$install_dir_resolved"
    fi
  }

  ensure_fish_path_targets() {
    file="$1"
    if [ -z "${file:-}" ]; then
      return 0
    fi
    if ! same_path_string "$claude_bin_dir" "$install_dir_resolved" && ! path_has_dir "$claude_bin_dir"; then
      ensure_fish_path_entry "$file" "$claude_bin_dir"
    fi
    if ! path_has_dir "$install_dir_resolved"; then
      ensure_fish_path_entry "$file" "$install_dir_resolved"
    fi
  }

  ensure_csh_path_targets() {
    file="$1"
    if [ -z "${file:-}" ]; then
      return 0
    fi
    if ! same_path_string "$claude_bin_dir" "$install_dir_resolved" && ! path_has_dir "$claude_bin_dir"; then
      ensure_csh_path_entry "$file" "$claude_bin_dir"
    fi
    if ! path_has_dir "$install_dir_resolved"; then
      ensure_csh_path_entry "$file" "$install_dir_resolved"
    fi
  }

  ensure_posix_path_targets "$HOME/.profile"
  ensure_line "$HOME/.profile" "$alias_line"

  wants_bash=0
  if [ "$shell_name" = "bash" ] || [ -f "$HOME/.bashrc" ] || [ -f "$HOME/.bash_profile" ]; then
    wants_bash=1
  fi
  if [ "$wants_bash" -eq 1 ]; then
    if [ "$os" = "darwin" ]; then
      ensure_posix_path_targets "$HOME/.bash_profile"
      ensure_line "$HOME/.bash_profile" "$alias_line"
      if [ -f "$HOME/.bashrc" ]; then
        ensure_posix_path_targets "$HOME/.bashrc"
        ensure_line "$HOME/.bashrc" "$alias_line"
      fi
    else
      ensure_posix_path_targets "$HOME/.bashrc"
      ensure_line "$HOME/.bashrc" "$alias_line"
      if [ -f "$HOME/.bash_profile" ]; then
        ensure_posix_path_targets "$HOME/.bash_profile"
        ensure_line "$HOME/.bash_profile" "$alias_line"
      fi
    fi
  fi

  wants_zsh=0
  if [ "$shell_name" = "zsh" ] || [ -f "$HOME/.zprofile" ] || [ -f "$HOME/.zshrc" ]; then
    wants_zsh=1
  fi
  if [ "$wants_zsh" -eq 1 ]; then
    ensure_posix_path_targets "$HOME/.zprofile"
    ensure_posix_path_targets "$HOME/.zshrc"
    ensure_line "$HOME/.zshrc" "$alias_line"
  fi

  wants_fish=0
  if [ "$shell_name" = "fish" ] || [ -f "$HOME/.config/fish/config.fish" ]; then
    wants_fish=1
  fi
  if [ "$wants_fish" -eq 1 ]; then
    fish_config="$HOME/.config/fish/config.fish"
    ensure_fish_path_targets "$fish_config"
    ensure_line "$fish_config" "alias clp \"claude-proxy\""
  fi

  wants_csh=0
  if [ "$shell_name" = "csh" ] || [ "$shell_name" = "tcsh" ] || [ -f "$HOME/.cshrc" ] || [ -f "$HOME/.tcshrc" ] || [ -f "$HOME/.login" ]; then
    wants_csh=1
  fi
  if [ "$wants_csh" -eq 1 ]; then
    ensure_csh_path_targets "$HOME/.login"
    if [ "$shell_name" = "csh" ] || [ -f "$HOME/.cshrc" ] || [ "$shell_name" = "tcsh" ]; then
      ensure_csh_path_targets "$HOME/.cshrc"
      ensure_line "$HOME/.cshrc" "alias clp claude-proxy"
    fi
    if [ -f "$HOME/.tcshrc" ]; then
      ensure_csh_path_targets "$HOME/.tcshrc"
      ensure_line "$HOME/.tcshrc" "alias clp claude-proxy"
    fi
  fi

  if [ "$CONFIG_UPDATED" -eq 1 ] && [ -n "$SOURCE_FILES" ]; then
    old_ifs="$IFS"
    IFS='
'
    for file in $SOURCE_FILES; do
      source_config_file "$file"
    done
    IFS="$old_ifs"
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

if [ -d "$install_dir" ]; then
  resolved="$(cd "$install_dir" 2>/dev/null && pwd -P || true)"
  if [ -n "${resolved:-}" ]; then
    install_dir_resolved="$resolved"
  fi
fi

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

update_shell_config
print_success
