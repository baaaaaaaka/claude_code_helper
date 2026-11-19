#!/usr/bin/env bash
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
apt_options=(
  -o Acquire::Retries=0
  -o Acquire::http::Timeout=10
  -o Acquire::https::Timeout=10
)

download_packages() {
  timeout 120s apt-get "${apt_options[@]}" -o APT::Update::Error-Mode=any update &&
    timeout 120s apt-get "${apt_options[@]}" install -y --download-only "$@"
}

if download_packages "$@"; then
  exec apt-get "${apt_options[@]}" install -y --no-download "$@"
fi

# Use separate sources and lists so the Ubuntu fallback does not change the
# runner's configuration or reuse indexes from an unavailable mirror.
source /etc/os-release
if [[ "$ID" != ubuntu || -z "${VERSION_CODENAME:-}" ]]; then
  exit 1
fi

scratch="$(mktemp -d)"
trap 'rm -rf -- "$scratch"' EXIT
chmod 755 "$scratch"
mkdir "$scratch/sources.list.d"
apt_options+=(
  -o "Dir::Etc::sourcelist=$scratch/sources.list"
  -o "Dir::Etc::sourceparts=$scratch/sources.list.d"
  -o Acquire::Languages=none
)

scheme=https
# Minimal Ubuntu images may still need to install ca-certificates. APT
# verifies the Ubuntu archive signature and package hashes with either scheme.
if [[ ! -s /etc/ssl/certs/ca-certificates.crt ]]; then
  scheme=http
fi

for mirror in mirrors.edge.kernel.org/ubuntu mirror.math.princeton.edu/pub/ubuntu; do
  echo "APT download failed. Trying $scheme://$mirror."
  for suite in "$VERSION_CODENAME"{,-updates,-security,-backports}; do
    printf 'deb [signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] %s://%s %s main restricted universe multiverse\n' \
      "$scheme" "$mirror" "$suite"
  done > "$scratch/sources.list"

  lists="$(mktemp -d "$scratch/lists.XXXXXX")"
  chmod 755 "$lists"
  if (apt_options+=(-o "Dir::State::lists=$lists"); download_packages "$@"); then
    apt-get "${apt_options[@]}" -o "Dir::State::lists=$lists" install -y --no-download "$@"
    exit 0
  fi
done

echo "APT packages could not be downloaded from the configured sources or Ubuntu mirrors." >&2
exit 1
