#!/usr/bin/env bash
set -euo pipefail

# Validate that a CentOS 7 container can run Claude with a glibc compat bundle.
#
# Example:
#   BUNDLE=/tmp/glibc-compat-out/glibc-2.31-centos7-runtime-x86_64.tar.xz \
#   bash scripts/glibc/test_centos7_claude_with_glibc_compat.sh

CENTOS_IMAGE="${CENTOS_IMAGE:-centos:7}"
CLAUDE_VERSION="${CLAUDE_VERSION:-2.1.38}"
CLAUDE_BUCKET="${CLAUDE_BUCKET:-https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases}"
BUNDLE="${BUNDLE:-$(pwd)/dist/glibc-compat/glibc-2.31-centos7-runtime-x86_64.tar.xz}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi
if [[ ! -f "$BUNDLE" ]]; then
  echo "missing bundle: $BUNDLE" >&2
  exit 1
fi

claude_url="${CLAUDE_BUCKET}/${CLAUDE_VERSION}/linux-x64/claude"

echo "Testing Claude on ${CENTOS_IMAGE}"
echo "Bundle: ${BUNDLE}"
echo "Claude URL: ${claude_url}"

docker run --rm \
  -e CLAUDE_URL="$claude_url" \
  -v "$BUNDLE:/bundle.tar.xz:ro" \
  "$CENTOS_IMAGE" \
  bash -lc '
set -euo pipefail

if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
  sed -i "s/^mirrorlist=/#mirrorlist=/g" /etc/yum.repos.d/CentOS-Base.repo || true
  sed -i "s|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g" /etc/yum.repos.d/CentOS-Base.repo || true
fi

yum -y install ca-certificates curl >/dev/null

mkdir -p /tmp/compat /tmp/claude
tar -C /tmp/compat -xJf /bundle.tar.xz
curl -fsSL "$CLAUDE_URL" -o /tmp/claude/claude
chmod +x /tmp/claude/claude

set +e
direct_out=$(/tmp/claude/claude --version 2>&1)
direct_ec=$?
wrapper_out=$(/tmp/compat/run-with-glibc-2.31.sh /tmp/claude/claude --version 2>&1)
wrapper_ec=$?
set -e

echo "[direct exit=${direct_ec}]"
echo "$direct_out"
echo "[wrapper exit=${wrapper_ec}]"
echo "$wrapper_out"

if [[ "$direct_ec" -eq 0 ]]; then
  echo "expected direct execution to fail on CentOS 7 but it succeeded" >&2
  exit 1
fi
if ! grep -q "GLIBC_" <<<"$direct_out"; then
  echo "expected direct failure to mention missing GLIBC symbols" >&2
  exit 1
fi
if [[ "$wrapper_ec" -ne 0 ]]; then
  echo "wrapper execution failed" >&2
  exit 1
fi
if [[ -z "$(echo "$wrapper_out" | tr -d "[:space:]")" ]]; then
  echo "wrapper output is empty" >&2
  exit 1
fi

echo "PASS: glibc compat wrapper can run Claude on CentOS 7."
'
