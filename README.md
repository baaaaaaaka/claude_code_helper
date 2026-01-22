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

### Linux / macOS

```bash
git clone https://github.com/baaaaaaaka/claude_code_helper.git
cd claude_code_helper

./install.sh
```

Install a specific version:

```bash
./install.sh --version v0.0.1
```

By default it installs to `~/.local/bin/claude-proxy`.

### Windows (PowerShell)

```powershell
git clone https://github.com/baaaaaaaka/claude_code_helper.git
cd claude_code_helper

.\install.ps1
```

Install a specific version:

```powershell
.\install.ps1 -Version v0.0.1
```

## Quick start

### 1) Create a profile

```bash
claude-proxy init
```

Config is stored under your OS user config directory (Linux typically `~/.config/claude-proxy/config.json`).

### 2) Run `claude` through the proxy

```bash
claude-proxy run
```

If you have multiple profiles:

```bash
claude-proxy run <profile>
```

### 3) Run any command through the proxy

```bash
claude-proxy run <profile> -- <cmd> [args...]
```

Example:

```bash
claude-proxy run pdx -- curl https://example.com
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

