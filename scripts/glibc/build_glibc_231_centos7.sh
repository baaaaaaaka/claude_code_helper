#!/usr/bin/env bash
set -euo pipefail

# Build glibc 2.31 in a CentOS 7 container without replacing system glibc.
# The result is an isolated runtime bundle that can be used as a compat layer.
# After the CentOS 7 build completes, augment the bundle with Rocky Linux 8
# libstdc++/libgcc runtime libraries so Node/npm can also run on EL7 through
# the same launcher.
#
# Example:
#   bash scripts/glibc/build_glibc_231_centos7.sh
#   OUT_DIR=/tmp/out JOBS=4 bash scripts/glibc/build_glibc_231_centos7.sh

GLIBC_VERSION="${GLIBC_VERSION:-2.31}"
CENTOS_IMAGE="${CENTOS_IMAGE:-centos:7}"
CPP_RUNTIME_IMAGE="${CPP_RUNTIME_IMAGE:-rockylinux:8}"
JOBS="${JOBS:-4}"
OUT_DIR="${OUT_DIR:-$(pwd)/dist/glibc-compat}"
BUNDLE_BASE="glibc-${GLIBC_VERSION}-centos7-runtime-cxx-x86_64"
BUNDLE_PATH="${OUT_DIR}/${BUNDLE_BASE}.tar.xz"
BUNDLE_SHA_PATH="${BUNDLE_PATH}.sha256"
CPP_RUNTIME_MIN_GLIBCXX="${CPP_RUNTIME_MIN_GLIBCXX:-GLIBCXX_3.4.21}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi
if ! command -v tar >/dev/null 2>&1; then
  echo "tar is required" >&2
  exit 1
fi
if ! command -v sha256sum >/dev/null 2>&1; then
  echo "sha256sum is required" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"

echo "Building glibc ${GLIBC_VERSION} in ${CENTOS_IMAGE}"
echo "Vendoring C++ runtime from ${CPP_RUNTIME_IMAGE}"
echo "Output directory: ${OUT_DIR}"

docker run --rm \
  -e GLIBC_VERSION="$GLIBC_VERSION" \
  -e JOBS="$JOBS" \
  -v "$OUT_DIR:/out" \
  "$CENTOS_IMAGE" \
  bash -lc '
set -euo pipefail

patch_base_repos() {
  if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
    sed -i "s/^mirrorlist=/#mirrorlist=/g" /etc/yum.repos.d/CentOS-Base.repo || true
    sed -i "s|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g" /etc/yum.repos.d/CentOS-Base.repo || true
  fi
}

patch_scl_repos() {
  sed -i "s/^mirrorlist=/#mirrorlist=/g" /etc/yum.repos.d/CentOS-SCLo-*.repo || true
  sed -i "s|mirror.centos.org|vault.centos.org|g" /etc/yum.repos.d/CentOS-SCLo-*.repo || true
  sed -i "s|^#baseurl=http://vault.centos.org|baseurl=http://vault.centos.org|g" /etc/yum.repos.d/CentOS-SCLo-*.repo || true
  # centos-sclo-sclo sometimes has mirrorlist only; add explicit baseurl.
  if ! awk "/^\[centos-sclo-sclo\]/{flag=1;next} /^\[/{flag=0} flag && /^baseurl=/{found=1} END{exit found?0:1}" /etc/yum.repos.d/CentOS-SCLo-scl.repo; then
    sed -i "/^\[centos-sclo-sclo\]/a baseurl=http://vault.centos.org/centos/7/sclo/\$basearch/sclo/" /etc/yum.repos.d/CentOS-SCLo-scl.repo
  fi
}

install_build_deps() {
  patch_base_repos
  yum -y install centos-release-scl python3 bison gawk tar xz wget gettext texinfo file
  patch_scl_repos
  yum -y install devtoolset-9-gcc devtoolset-9-gcc-c++ devtoolset-9-binutils devtoolset-9-make
}

build_glibc() {
  local src="glibc-${GLIBC_VERSION}"
  local tarball="${src}.tar.xz"
  local prefix="/opt/glibc-${GLIBC_VERSION}"
  local stage="/tmp/glibc-stage"
  local build_dir="/tmp/glibc-build"
  local src_dir="/tmp/glibc-src"

  rm -rf "$src_dir" "$build_dir" "$stage"
  mkdir -p "$src_dir" "$build_dir" "$stage"
  cd "$src_dir"
  wget -q "https://ftp.gnu.org/gnu/glibc/${tarball}"
  tar -xf "$tarball"

  # devtoolset activation can fail with nounset when MANPATH is undefined.
  set +u
  source /opt/rh/devtoolset-9/enable
  set -u

  cd "$build_dir"
  "${src_dir}/${src}/configure" \
    --prefix="$prefix" \
    --enable-kernel=3.10 \
    --disable-werror

  make -j"${JOBS}"
  make install DESTDIR="$stage"

  "${stage}${prefix}/lib/ld-linux-x86-64.so.2" \
    --library-path "${stage}${prefix}/lib" \
    "${stage}${prefix}/lib/libc.so.6" | awk "NR==1{print}"
  "${stage}${prefix}/lib/ld-linux-x86-64.so.2" \
    --library-path "${stage}${prefix}/lib" \
    /bin/echo "glibc_${GLIBC_VERSION}_runtime_ok"
}

package_runtime() {
  local prefix="/opt/glibc-${GLIBC_VERSION}"
  local stage="/tmp/glibc-stage"
  local runtime_root="/tmp/glibc-runtime"
  local runtime_dir="${runtime_root}/glibc-${GLIBC_VERSION}"
  local bundle_base="glibc-${GLIBC_VERSION}-centos7-runtime-cxx-x86_64"
  local tar_path="/out/${bundle_base}.tar.xz"

  rm -rf "$runtime_root"
  mkdir -p "$runtime_dir"

  # Keep the full installed tree to avoid missing indirect runtime deps.
  cp -a "${stage}${prefix}/." "$runtime_dir/"

  cat > "${runtime_root}/run-with-glibc-${GLIBC_VERSION}.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ "\$#" -lt 1 ]]; then
  echo "usage: \$0 <binary> [args...]" >&2
  exit 2
fi
SCRIPT_DIR="\$(cd "\$(dirname "\${BASH_SOURCE[0]}")" && pwd)"
GLIBC_ROOT="\${SCRIPT_DIR}/glibc-${GLIBC_VERSION}"
exec -a "\$1" "\${GLIBC_ROOT}/lib/ld-linux-x86-64.so.2" --library-path "\${GLIBC_ROOT}/lib" "\$@"
EOF
  chmod +x "${runtime_root}/run-with-glibc-${GLIBC_VERSION}.sh"

  tar -C "$runtime_root" -cJf "$tar_path" .
  (
    cd /out
    sha256sum "${bundle_base}.tar.xz" > "${bundle_base}.tar.xz.sha256"
  )

  echo "Built runtime bundle:"
  ls -lh "$tar_path" "${tar_path}.sha256"
}

install_build_deps
build_glibc
package_runtime
'

augment_bundle_with_cpp_runtime() {
  local stage_dir="${OUT_DIR}/.bundle-stage-${GLIBC_VERSION}"
  local runtime_root="${stage_dir}/glibc-${GLIBC_VERSION}"
  local lib_dir="${runtime_root}/lib"

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir"
  tar -C "$stage_dir" -xJf "$BUNDLE_PATH"
  if [[ ! -d "$lib_dir" ]]; then
    echo "missing extracted runtime lib dir: $lib_dir" >&2
    rm -rf "$stage_dir"
    exit 1
  fi

  docker run --rm \
    -v "$lib_dir:/bundle-lib:rw" \
    "$CPP_RUNTIME_IMAGE" \
    bash -lc '
set -euo pipefail
test -f /lib64/libstdc++.so.6
test -f /lib64/libgcc_s.so.1
cp -L /lib64/libstdc++.so.6 /bundle-lib/libstdc++.so.6
cp -L /lib64/libgcc_s.so.1 /bundle-lib/libgcc_s.so.1
chmod 0644 /bundle-lib/libstdc++.so.6 /bundle-lib/libgcc_s.so.1
'

  if ! grep -aq "$CPP_RUNTIME_MIN_GLIBCXX" "${lib_dir}/libstdc++.so.6"; then
    echo "vendored libstdc++.so.6 does not expose ${CPP_RUNTIME_MIN_GLIBCXX}" >&2
    rm -rf "$stage_dir"
    exit 1
  fi
  if [[ ! -f "${lib_dir}/libgcc_s.so.1" ]]; then
    echo "vendored libgcc_s.so.1 is missing from bundle" >&2
    rm -rf "$stage_dir"
    exit 1
  fi

  rm -f "$BUNDLE_PATH" "$BUNDLE_SHA_PATH"
  tar -C "$stage_dir" -cJf "$BUNDLE_PATH" .
  (
    cd "$OUT_DIR"
    sha256sum "${BUNDLE_BASE}.tar.xz" > "${BUNDLE_BASE}.tar.xz.sha256"
  )
  rm -rf "$stage_dir"

  echo "Vendored Rocky Linux 8 C++ runtime into bundle:"
  ls -lh "$BUNDLE_PATH" "$BUNDLE_SHA_PATH"
}

augment_bundle_with_cpp_runtime

echo
echo "Done. Bundle files:"
ls -lh "$OUT_DIR"/"${BUNDLE_BASE}".tar.xz*
