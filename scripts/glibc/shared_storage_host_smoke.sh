#!/usr/bin/env bash
set -euo pipefail

CLAUDE_PROXY_BIN="${CLAUDE_PROXY_BIN:-/dist/claude-proxy}"
CLAUDE_PATH="${CLAUDE_PATH:-/shared/claude/claude}"
SHARED_HOME="${SHARED_HOME:-/shared/home}"
EXPECT_MODE="${EXPECT_MODE:?EXPECT_MODE must be compat or direct}"
TEST_USER="${TEST_USER:-testuser}"
SSHD_PORT="${SSHD_PORT:-2222}"
HOST_ID="${CLAUDE_PROXY_HOST_ID:?CLAUDE_PROXY_HOST_ID must be set}"
GLIBC_COMPAT_REPO="${GLIBC_COMPAT_REPO:-baaaaaaaka/claude_code_helper}"
GLIBC_COMPAT_TAG="${GLIBC_COMPAT_TAG:-glibc-compat-v2.31}"
SHARED_UID="${SHARED_UID:-}"
SHARED_GID="${SHARED_GID:-}"

sshd_pid=""

patch_base_repo() {
  if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
    sed -i 's/^mirrorlist=/#mirrorlist=/g' /etc/yum.repos.d/CentOS-Base.repo || true
    sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-Base.repo || true
  fi
}

install_deps() {
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends ca-certificates openssh-server openssh-client patchelf
    return
  fi

  if command -v dnf >/dev/null 2>&1; then
    dnf -y install ca-certificates openssh-server openssh-clients patchelf
    return
  fi

  if command -v yum >/dev/null 2>&1; then
    patch_base_repo
    yum -y install ca-certificates openssh-server openssh-clients epel-release
    yum -y install patchelf
    return
  fi

  echo "No supported package manager found" >&2
  exit 1
}

setup_sshd() {
  if ! id "$TEST_USER" >/dev/null 2>&1; then
    useradd -m -s /bin/bash "$TEST_USER"
  fi
  if command -v chpasswd >/dev/null 2>&1; then
    echo "$TEST_USER:ci-password" | chpasswd
  fi

  mkdir -p /run/sshd || true
  ssh-keygen -A
  ssh-keygen -t ed25519 -N "" -f /tmp/claude_proxy_test_key >/dev/null

  mkdir -p "/home/$TEST_USER/.ssh"
  cat /tmp/claude_proxy_test_key.pub >"/home/$TEST_USER/.ssh/authorized_keys"
  chown -R "$TEST_USER:$TEST_USER" "/home/$TEST_USER/.ssh" /tmp/claude_proxy_test_key /tmp/claude_proxy_test_key.pub
  chmod 700 "/home/$TEST_USER/.ssh"
  chmod 600 "/home/$TEST_USER/.ssh/authorized_keys" /tmp/claude_proxy_test_key
  chmod 644 /tmp/claude_proxy_test_key.pub

  cat >/tmp/sshd_config <<EOF
Port ${SSHD_PORT}
ListenAddress 127.0.0.1
HostKey /etc/ssh/ssh_host_ed25519_key
PidFile /tmp/sshd.pid
PermitRootLogin no
PasswordAuthentication no
ChallengeResponseAuthentication no
KbdInteractiveAuthentication no
UsePAM yes
PubkeyAuthentication yes
AuthorizedKeysFile %h/.ssh/authorized_keys
StrictModes no
Subsystem sftp internal-sftp
AllowUsers ${TEST_USER}
LogLevel ERROR
EOF

  /usr/sbin/sshd -t -f /tmp/sshd_config
  /usr/sbin/sshd -D -e -f /tmp/sshd_config >/tmp/sshd.log 2>&1 &
  sshd_pid=$!

  for _ in $(seq 1 50); do
    if ssh -p "$SSHD_PORT" \
      -o BatchMode=yes \
      -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null \
      -i /tmp/claude_proxy_test_key \
      "${TEST_USER}@127.0.0.1" exit >/dev/null 2>&1; then
      return
    fi
    if ! kill -0 "$sshd_pid" 2>/dev/null; then
      echo "sshd exited early" >&2
      wait "$sshd_pid" || true
      exit 1
    fi
    sleep 0.1
  done

  if [[ -f /tmp/sshd.log ]]; then
    echo "--- sshd log ---" >&2
    sed -n '1,120p' /tmp/sshd.log >&2
  fi
  echo "sshd did not become ready in time" >&2
  exit 1
}

write_config() {
  cat >/tmp/config.json <<EOF
{
  "version": 1,
  "profiles": [
    {
      "id": "p1",
      "name": "p1",
      "host": "127.0.0.1",
      "port": ${SSHD_PORT},
      "user": "${TEST_USER}",
      "sshArgs": [
        "-i",
        "/tmp/claude_proxy_test_key",
        "-o",
        "StrictHostKeyChecking=no",
        "-o",
        "UserKnownHostsFile=/dev/null",
        "-o",
        "IdentitiesOnly=yes",
        "-o",
        "GSSAPIAuthentication=no"
      ],
      "createdAt": "2026-03-21T00:00:00Z"
    }
  ],
  "instances": []
}
EOF
}

run_smoke() {
  if [[ ! -x "$CLAUDE_PROXY_BIN" ]]; then
    echo "missing claude-proxy binary at $CLAUDE_PROXY_BIN" >&2
    exit 1
  fi
  if [[ ! -x "$CLAUDE_PATH" ]]; then
    echo "missing Claude binary at $CLAUDE_PATH" >&2
    exit 1
  fi

  mkdir -p "$SHARED_HOME" "$SHARED_HOME/.cache"

  local source_sha_before
  source_sha_before="$(sha256sum "$CLAUDE_PATH" | awk '{print $1}')"

  local -a run_env=(
    "CLAUDE_PROXY_GLIBC_COMPAT=1"
    "CLAUDE_PROXY_HOST_ID=${HOST_ID}"
    "CLAUDE_PROXY_GLIBC_COMPAT_REPO=${GLIBC_COMPAT_REPO}"
    "CLAUDE_PROXY_GLIBC_COMPAT_TAG=${GLIBC_COMPAT_TAG}"
    "HOME=${SHARED_HOME}"
    "XDG_CACHE_HOME=${SHARED_HOME}/.cache"
  )

  set +e
  run_out="$(
    env "${run_env[@]}" \
      timeout 180s "$CLAUDE_PROXY_BIN" --config /tmp/config.json run p1 -- "$CLAUDE_PATH" --version 2>&1
  )"
  run_ec=$?
  set -e

  echo "[${HOST_ID} ${EXPECT_MODE} exit=${run_ec}]"
  echo "$run_out"

  if [[ "$run_ec" -ne 0 ]]; then
    echo "claude-proxy run failed for ${HOST_ID}" >&2
    exit 1
  fi
  if ! grep -q "(Claude Code)" <<<"$run_out"; then
    echo "expected Claude version output, got: $run_out" >&2
    exit 1
  fi
  if grep -q "GLIBC_" <<<"$run_out"; then
    echo "unexpected GLIBC symbol errors after run" >&2
    exit 1
  fi

  local source_sha_after
  source_sha_after="$(sha256sum "$CLAUDE_PATH" | awk '{print $1}')"
  echo "[source sha before] ${source_sha_before}"
  echo "[source sha after ] ${source_sha_after}"
  if [[ "$source_sha_before" != "$source_sha_after" ]]; then
    echo "shared Claude source binary was modified" >&2
    exit 1
  fi
  if [[ -e "${CLAUDE_PATH}.claude-proxy.bak" ]]; then
    echo "unexpected source-side backup file created at ${CLAUDE_PATH}.claude-proxy.bak" >&2
    exit 1
  fi

  local mirror_root="${SHARED_HOME}/.cache/claude-proxy/hosts/${HOST_ID}/claude"
  local mirror_path=""
  if [[ -d "$mirror_root" ]]; then
    mirror_path="$(find "$mirror_root" -type f -name "$(basename "$CLAUDE_PATH")" | head -n 1 || true)"
  fi

  case "$EXPECT_MODE" in
    compat)
      if [[ -z "$mirror_path" ]]; then
        echo "expected host-local glibc compat artifact under ${mirror_root}" >&2
        exit 1
      fi
      if [[ "$mirror_path" == "$CLAUDE_PATH" ]]; then
        echo "compat path unexpectedly points at the shared source binary" >&2
        exit 1
      fi
      local compat_mode="mirror"
      local interp=""
      local rpath=""
      set +e
      interp="$(patchelf --print-interpreter "$mirror_path" 2>/dev/null)"
      local interp_ec=$?
      if [[ "$interp_ec" -eq 0 ]]; then
        rpath="$(patchelf --print-rpath "$mirror_path" 2>/dev/null || true)"
      fi
      set -e
      echo "[compat path] ${mirror_path}"
      if [[ "$interp" == */glibc-2.31/lib/ld-linux-x86-64.so.2 ]]; then
        echo "[compat mode] mirror"
        echo "[mirror interpreter] ${interp}"
        echo "[mirror rpath] ${rpath}"
        local glibc_lib_dir
        glibc_lib_dir="$(dirname "$interp")"
        if [[ ! -f "${glibc_lib_dir}/libc.so.6" ]]; then
          echo "mirror runtime missing libc.so.6 in ${glibc_lib_dir}" >&2
          exit 1
        fi
        if grep -q "${glibc_lib_dir}" <<<"$rpath"; then
          echo "[mirror launch] rpath includes glibc lib dir"
        else
          echo "[mirror launch] relying on LD_LIBRARY_PATH for glibc lib dir"
        fi
      elif grep -q "using glibc compat wrapper" <<<"$run_out"; then
        compat_mode="wrapper"
        echo "[compat mode] wrapper"
        local wrapper_path
        wrapper_path="$(printf '%s\n' "$run_out" | sed -n 's/.*using glibc compat wrapper \([^ ]*\) for.*/\1/p' | tail -n 1)"
        if [[ -n "$wrapper_path" ]]; then
          echo "[wrapper path] ${wrapper_path}"
          if [[ ! -x "$wrapper_path" ]]; then
            echo "wrapper fallback path is not executable: ${wrapper_path}" >&2
            exit 1
          fi
        fi
      else
        echo "expected mirror patch or wrapper fallback, got interpreter: ${interp}" >&2
        exit 1
      fi
      ;;
    direct)
      if [[ -n "$mirror_path" ]]; then
        echo "did not expect a glibc compat mirror for ${HOST_ID}, found ${mirror_path}" >&2
        exit 1
      fi
      ;;
    *)
      echo "unsupported EXPECT_MODE=${EXPECT_MODE}" >&2
      exit 1
      ;;
  esac

  echo "PASS: ${HOST_ID} completed shared-storage smoke in ${EXPECT_MODE} mode."
}

cleanup() {
  if [[ -n "$SHARED_UID" && -n "$SHARED_GID" && -d /shared ]]; then
    chown -R "${SHARED_UID}:${SHARED_GID}" /shared 2>/dev/null || true
  fi
  if [[ -n "$sshd_pid" ]]; then
    kill "$sshd_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

install_deps
setup_sshd
write_config
run_smoke
