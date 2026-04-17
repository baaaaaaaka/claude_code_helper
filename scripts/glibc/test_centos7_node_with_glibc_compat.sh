#!/usr/bin/env bash
set -euo pipefail

# Validate that a CentOS 7 container can run Node.js through the glibc compat
# bundle, including the vendored libstdc++/libgcc runtime.
#
# Example:
#   BUNDLE=/tmp/glibc-compat-out/glibc-2.31-centos7-runtime-cxx-x86_64.tar.xz \
#   bash scripts/glibc/test_centos7_node_with_glibc_compat.sh

CENTOS_IMAGE="${CENTOS_IMAGE:-centos:7}"
NODE_VERSION="${NODE_VERSION:-v18.20.8}"
BUNDLE="${BUNDLE:-$(pwd)/dist/glibc-compat/glibc-2.31-centos7-runtime-cxx-x86_64.tar.xz}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi
if [[ ! -f "$BUNDLE" ]]; then
  echo "missing bundle: $BUNDLE" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64)
    node_arch="x64"
    ;;
  aarch64|arm64)
    echo "unsupported architecture for Node smoke: $(uname -m); the compat bundle is x86_64-only" >&2
    exit 1
    ;;
  *)
    echo "unsupported architecture for Node smoke: $(uname -m)" >&2
    exit 1
    ;;
esac

node_url="https://nodejs.org/dist/${NODE_VERSION}/node-${NODE_VERSION}-linux-${node_arch}.tar.xz"

echo "Testing Node.js on ${CENTOS_IMAGE}"
echo "Bundle: ${BUNDLE}"
echo "Node URL: ${node_url}"

docker run --rm \
  -e NODE_URL="$node_url" \
  -v "$BUNDLE:/bundle.tar.xz:ro" \
  "$CENTOS_IMAGE" \
  bash -lc '
set -euo pipefail

if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
  sed -i "s/^mirrorlist=/#mirrorlist=/g" /etc/yum.repos.d/CentOS-Base.repo || true
  sed -i "s|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g" /etc/yum.repos.d/CentOS-Base.repo || true
fi

yum -y install ca-certificates curl xz >/dev/null

mkdir -p /tmp/compat /tmp/node
tar -C /tmp/compat -xJf /bundle.tar.xz
curl -fsSL "$NODE_URL" -o /tmp/node.tar.xz
tar -xJf /tmp/node.tar.xz -C /tmp/node --strip-components=1

set +e
direct_out=$(/tmp/node/bin/node --version 2>&1)
direct_ec=$?
wrapper_out=$(/tmp/compat/run-with-glibc-2.31.sh /tmp/node/bin/node --version 2>&1)
wrapper_ec=$?
set -e

echo "[direct exit=${direct_ec}]"
echo "$direct_out"
echo "[wrapper exit=${wrapper_ec}]"
echo "$wrapper_out"

if [[ "$direct_ec" -eq 0 ]]; then
  echo "expected direct Node execution to fail on CentOS 7 but it succeeded" >&2
  exit 1
fi
if [[ "$wrapper_ec" -ne 0 ]]; then
  echo "glibc compat wrapper failed to run Node.js" >&2
  exit 1
fi
if [[ "$wrapper_out" != v* ]]; then
  echo "unexpected Node version output: $wrapper_out" >&2
  exit 1
fi

echo "PASS: glibc compat wrapper can run Node.js on CentOS 7."
'
