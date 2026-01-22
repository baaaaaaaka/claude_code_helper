#!/usr/bin/env bash
set -euo pipefail

repo="${REPO:?}"
tag="${TAG:?}"
fetcher="${FETCHER:?}"

install_deps() {
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    local pkgs=(ca-certificates openssh-client openssh-server)
    if [[ "$fetcher" == "curl" ]]; then
      pkgs+=(curl)
    else
      pkgs+=(wget)
    fi
    apt-get install -y --no-install-recommends "${pkgs[@]}"
    return
  fi

  if command -v dnf >/dev/null 2>&1; then
    local pkgs=(ca-certificates openssh-server openssh-clients)
    if [[ "$fetcher" == "curl" ]]; then
      pkgs+=(curl)
    else
      pkgs+=(wget)
    fi
    dnf -y install "${pkgs[@]}"
    return
  fi

  if command -v yum >/dev/null 2>&1; then
    # CentOS 7 is EOL; make sure yum uses vault if mirrorlist is broken.
    if [[ -f /etc/yum.repos.d/CentOS-Base.repo ]]; then
      sed -i 's/^mirrorlist=/#mirrorlist=/g' /etc/yum.repos.d/CentOS-Base.repo || true
      sed -i 's|^#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-Base.repo || true
    fi
    local pkgs=(ca-certificates openssh-server openssh-clients)
    if [[ "$fetcher" == "curl" ]]; then
      pkgs+=(curl)
    else
      pkgs+=(wget)
    fi
    yum -y install "${pkgs[@]}"
    return
  fi

  echo "No supported package manager found" >&2
  exit 1
}

force_fetcher() {
  case "$fetcher" in
    curl)
      command -v curl >/dev/null 2>&1 || { echo "curl not found" >&2; exit 1; }
      if command -v wget >/dev/null 2>&1; then
        mv "$(command -v wget)" "$(command -v wget).disabled" || true
      fi
      ;;
    wget)
      command -v wget >/dev/null 2>&1 || { echo "wget not found" >&2; exit 1; }
      if command -v curl >/dev/null 2>&1; then
        mv "$(command -v curl)" "$(command -v curl).disabled" || true
      fi
      ;;
    *)
      echo "Unsupported FETCHER=$fetcher (expected curl or wget)" >&2
      exit 1
      ;;
  esac
}

setup_sshd() {
  local user="testuser"
  if ! id "$user" >/dev/null 2>&1; then
    useradd -m "$user"
  fi
  # Some distros treat newly created users as "locked" until a password is set.
  if command -v chpasswd >/dev/null 2>&1; then
    echo "$user:ci-password" | chpasswd
  fi

  mkdir -p /run/sshd || true
  ssh-keygen -A

  ssh-keygen -t ed25519 -f /tmp/claude_proxy_test_key -N "" -C "claude-proxy-ci" >/dev/null
  chown "$user:$user" /tmp/claude_proxy_test_key /tmp/claude_proxy_test_key.pub
  chmod 600 /tmp/claude_proxy_test_key
  chmod 644 /tmp/claude_proxy_test_key.pub

  mkdir -p "/home/$user/.ssh"
  cat /tmp/claude_proxy_test_key.pub >"/home/$user/.ssh/authorized_keys"
  chown -R "$user:$user" "/home/$user/.ssh"
  chmod 700 "/home/$user/.ssh"
  chmod 600 "/home/$user/.ssh/authorized_keys"

  cat >/tmp/sshd_config <<'EOF'
Port 2222
ListenAddress 127.0.0.1
HostKey /etc/ssh/ssh_host_ed25519_key
PidFile /tmp/sshd.pid
PermitRootLogin no
PasswordAuthentication no
PubkeyAuthentication yes
AuthorizedKeysFile %h/.ssh/authorized_keys
StrictModes no
Subsystem sftp internal-sftp
LogLevel VERBOSE
EOF

  /usr/sbin/sshd -t -f /tmp/sshd_config

  /usr/sbin/sshd -D -e -f /tmp/sshd_config &
  sshd_pid=$!
  export sshd_pid

  # Wait for sshd to become ready (non-interactive, no hostkey prompts).
  for _ in $(seq 1 50); do
    if ssh -p 2222 -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i /tmp/claude_proxy_test_key "$user@127.0.0.1" exit; then
      return
    fi
    if ! kill -0 "$sshd_pid" 2>/dev/null; then
      echo "sshd exited early" >&2
      wait "$sshd_pid" || true
      exit 1
    fi
    sleep 0.1
  done

  echo "sshd did not become ready in time" >&2
  exit 1
}

run_install_and_smoke_as_user() {
  local user="testuser"

  cat >/tmp/claude-proxy-user-smoke.sh <<'EOS'
set -euo pipefail

repo="${REPO:?}"
tag="${TAG:?}"
fetcher="${FETCHER:?}"

mkdir -p "$HOME/.local/bin"

script_url="https://github.com/${repo}/releases/download/${tag}/install.sh"
if [[ "$fetcher" == "curl" ]]; then
  curl -fsSL "$script_url" | sh -s -- --repo "$repo" --version "$tag" --dir "$HOME/.local/bin"
else
  wget -qO- "$script_url" | sh -s -- --repo "$repo" --version "$tag" --dir "$HOME/.local/bin"
fi

"$HOME/.local/bin/claude-proxy" --version | grep -q "${tag#v}"
"$HOME/.local/bin/claude-proxy" proxy doctor || true

cfg="$HOME/claude-proxy-config.json"
cat >"$cfg" <<EOF
{
  "version": 1,
  "profiles": [
    {
      "id": "local",
      "name": "local",
      "host": "127.0.0.1",
      "port": 2222,
      "user": "testuser",
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
      "createdAt": "2026-01-01T00:00:00Z"
    }
  ],
  "instances": []
}
EOF

"$HOME/.local/bin/claude-proxy" --config "$cfg" run -- true
EOS

  chmod +x /tmp/claude-proxy-user-smoke.sh
  su -s /bin/bash - "$user" -c "REPO=$repo TAG=$tag FETCHER=$fetcher bash /tmp/claude-proxy-user-smoke.sh"
}

cleanup() {
  if [[ -n "${sshd_pid:-}" ]]; then
    kill "$sshd_pid" 2>/dev/null || true
  fi
}

trap cleanup EXIT

echo "Container install smoke: repo=$repo tag=$tag fetcher=$fetcher"

install_deps
force_fetcher
setup_sshd
run_install_and_smoke_as_user

