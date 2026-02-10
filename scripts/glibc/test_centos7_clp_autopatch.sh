#!/usr/bin/env bash
set -euo pipefail

# End-to-end smoke for CentOS 7:
# - starts local sshd
# - runs claude-proxy run ... claude --version
# - verifies claude-proxy auto-downloads and applies glibc compat patch via patchelf

CLAUDE_PROXY_BIN="${CLAUDE_PROXY_BIN:-/dist/claude-proxy}"
CLAUDE_VERSION="${CLAUDE_VERSION:-2.1.38}"
CLAUDE_BUCKET="${CLAUDE_BUCKET:-https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases}"
TEST_USER="${TEST_USER:-testuser}"
SSHD_PORT="${SSHD_PORT:-2222}"
GLIBC_COMPAT_REPO="${GLIBC_COMPAT_REPO:-}"
GLIBC_COMPAT_TAG="${GLIBC_COMPAT_TAG:-}"

sshd_pid=""

patch_base_repo() {
  if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
    sed -i 's/^mirrorlist=/#mirrorlist=/g' /etc/yum.repos.d/CentOS-Base.repo || true
    sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-Base.repo || true
  fi
}

install_deps() {
  patch_base_repo
  yum -y install ca-certificates curl openssh-server openssh-clients epel-release
  yum -y install patchelf
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
  cat /tmp/claude_proxy_test_key.pub > "/home/$TEST_USER/.ssh/authorized_keys"
  chown -R "$TEST_USER:$TEST_USER" "/home/$TEST_USER/.ssh" /tmp/claude_proxy_test_key /tmp/claude_proxy_test_key.pub
  chmod 700 "/home/$TEST_USER/.ssh"
  chmod 600 "/home/$TEST_USER/.ssh/authorized_keys" /tmp/claude_proxy_test_key
  chmod 644 /tmp/claude_proxy_test_key.pub

  cat > /tmp/sshd_config <<EOF
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

prepare_runtime() {
  if [[ ! -x "$CLAUDE_PROXY_BIN" ]]; then
    echo "missing claude-proxy binary at $CLAUDE_PROXY_BIN" >&2
    exit 1
  fi

  mkdir -p /tmp/claude

  CLAUDE_URL="${CLAUDE_BUCKET}/${CLAUDE_VERSION}/linux-x64/claude"
  curl -fsSL "$CLAUDE_URL" -o /tmp/claude/claude
  chmod +x /tmp/claude/claude
}

run_clp_smoke() {
  cat > /tmp/config.json <<EOF
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
      "createdAt": "2026-02-10T00:00:00Z"
    }
  ],
  "instances": []
}
EOF

  local -a run_env=("CLAUDE_PROXY_GLIBC_COMPAT=1")
  if [[ -n "$GLIBC_COMPAT_REPO" ]]; then
    run_env+=("CLAUDE_PROXY_GLIBC_COMPAT_REPO=$GLIBC_COMPAT_REPO")
  fi
  if [[ -n "$GLIBC_COMPAT_TAG" ]]; then
    run_env+=("CLAUDE_PROXY_GLIBC_COMPAT_TAG=$GLIBC_COMPAT_TAG")
  fi

  set +e
  run_out="$(
    env "${run_env[@]}" \
      timeout 180s "$CLAUDE_PROXY_BIN" --config /tmp/config.json run p1 -- /tmp/claude/claude --version 2>&1
  )"
  run_ec=$?
  set -e

  echo "[clp run exit=${run_ec}]"
  echo "$run_out"

  if [[ "$run_ec" -ne 0 ]]; then
    echo "claude-proxy run failed" >&2
    exit 1
  fi
  if ! grep -q "(Claude Code)" <<<"$run_out"; then
    echo "expected Claude Code version output, got: $run_out" >&2
    exit 1
  fi
  if grep -q "GLIBC_" <<<"$run_out"; then
    echo "unexpected GLIBC symbol errors after clp patch" >&2
    exit 1
  fi

  interp="$(patchelf --print-interpreter /tmp/claude/claude)"
  rpath="$(patchelf --print-rpath /tmp/claude/claude)"
  echo "[patched interpreter] ${interp}"
  echo "[patched rpath] ${rpath}"

  if [[ "$interp" != */glibc-2.31/lib/ld-linux-x86-64.so.2 ]]; then
    echo "unexpected patched interpreter: $interp" >&2
    exit 1
  fi
  local glibc_lib_dir
  glibc_lib_dir="$(dirname "$interp")"
  if [[ ! -f "${glibc_lib_dir}/libc.so.6" ]]; then
    echo "patched glibc runtime missing libc.so.6 in ${glibc_lib_dir}" >&2
    exit 1
  fi
  if ! grep -q "${glibc_lib_dir}" <<<"$rpath"; then
    echo "patched rpath missing glibc lib dir ${glibc_lib_dir}: $rpath" >&2
    exit 1
  fi

  echo "PASS: claude-proxy auto-downloaded and patched Claude glibc successfully on CentOS 7."
}

cleanup() {
  if [[ -n "$sshd_pid" ]]; then
    kill "$sshd_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

install_deps
setup_sshd
prepare_runtime
run_clp_smoke
