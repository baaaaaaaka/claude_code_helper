# claude-proxy

Run `claude` (or any command) through an SSH-backed local proxy stack:

- **Upstream**: `ssh -D 127.0.0.1:<port>` SOCKS5 tunnel
- **Downstream**: local **HTTP CONNECT** proxy (Go) that dials via SOCKS5
- **Run supervision**: if the proxy becomes unhealthy and cannot be healed, the target process is terminated to avoid direct connections

This project is designed to ship as a **single binary** per OS/arch.

## Requirements (runtime)

- `ssh` (OpenSSH client) is required
- `ssh-keygen` is optional (only needed if you generate keys via `claude-proxy init`)

Check your environment:

```bash
claude-proxy proxy doctor
```

## Install (no root)

### Linux / macOS (one-liner, auto-detects curl/wget)

```bash
sh -c 'url="https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.sh"; if command -v curl >/dev/null 2>&1; then curl -fsSL "$url" | sh; elif command -v wget >/dev/null 2>&1; then wget -qO- "$url" | sh; else echo "need curl or wget" >&2; exit 1; fi'
```

By default it installs to `~/.local/bin/claude-proxy`.

If `~/.local/bin` is not on your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Install a specific version (example):

```bash
curl -fsSL https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.sh | sh -s -- --version v0.0.2
```

### Windows (PowerShell one-liner)

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "iwr -useb https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.ps1 | iex"
```

Install a specific version:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "$u='https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.ps1'; $p=Join-Path $env:TEMP 'claude-proxy-install.ps1'; iwr -useb $u -OutFile $p; & $p -Version v0.0.2; Remove-Item -Force $p"
```

## Quick start

### 1) Create a profile (optional)

```bash
claude-proxy init
```

Config is stored under your OS user config directory (Linux typically `~/.config/claude-proxy/config.json`).

If you skip this step, running `claude-proxy` will start the init flow automatically when no profiles exist.

### 2) Run `claude` through the proxy

```bash
claude-proxy
```

If you have multiple profiles:

```bash
claude-proxy <profile>
```

### 3) Run any command through the proxy

```bash
claude-proxy <profile> -- <cmd> [args...]
```

Example:

```bash
claude-proxy pdx -- curl https://example.com
```

## Long-lived instances (optional)

Start a reusable daemon instance:

```bash
claude-proxy proxy start <profile>
claude-proxy proxy list
```

Stop an instance:

```bash
claude-proxy proxy stop <instance-id>
```

