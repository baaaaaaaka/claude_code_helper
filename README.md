# claude-proxy

Run `claude` (or any command) through an SSH-backed local proxy stack:

- **Upstream**: `ssh -D 127.0.0.1:<port>` SOCKS5 tunnel
- **Downstream**: local **HTTP CONNECT** proxy (Go) that dials via SOCKS5
- **Run supervision**: if the proxy becomes unhealthy and cannot be healed, the target process is terminated to avoid direct connections

This project is designed to ship as a **single binary** per OS/arch.

## Quick start

### 1) **Install**

Linux / macOS:

```bash
sh -c 'url="https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.sh"; if command -v curl >/dev/null 2>&1; then curl -fsSL "$url" | sh; elif command -v wget >/dev/null 2>&1; then wget -qO- "$url" | sh; else echo "need curl or wget" >&2; exit 1; fi'
```

Windows (PowerShell):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "iwr -useb https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.ps1 | iex"
```

The installer drops a `clp` shim alongside `claude-proxy` and tries to add the
install directory to PATH (plus a `clp` alias). Open a new shell if the
command is not found.

### 2) **Run**

```bash
claude-proxy
# or
clp
```

On first run you'll be asked whether to use the SSH proxy. Choose **no** for
direct connections. Choose **yes** to enter SSH host/port/user and let the
tool create a dedicated key if needed. You can toggle proxy mode later with
`Ctrl+P` in the TUI.

### 3) Next steps

- Press Enter to open a Claude Code session.
- If there is no history yet, Enter starts a new session in the current directory.
- If you have multiple profiles, select one with `claude-proxy <profile>`.
- Run any command through the proxy (requires a profile):
  `claude-proxy run <profile> -- <cmd> [args...]`.
- Example: `claude-proxy run pdx -- curl https://example.com`.

### Optional: preconfigure a proxy profile

```bash
claude-proxy init
```

Config is stored under your OS user config directory (Linux typically
`~/.config/claude-proxy/config.json`).

## Requirements (runtime)

- `ssh` (OpenSSH client) is required
- `ssh-keygen` is optional (only needed when proxy mode creates a dedicated key)

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

The installer drops a `clp` shim alongside `claude-proxy` and tries to add
`~/.local/bin` to PATH (plus a `clp` alias). Open a new shell if the command is
not found.
If you need to update PATH manually:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Install a specific version (example):

```bash
curl -fsSL https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.sh | sh -s -- --version v0.0.22
```

### Windows (PowerShell one-liner)

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "iwr -useb https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.ps1 | iex"
```

Install a specific version:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "$u='https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.ps1'; $p=Join-Path $env:TEMP 'claude-proxy-install.ps1'; iwr -useb $u -OutFile $p; & $p -Version v0.0.22; Remove-Item -Force $p"
```

## Claude Code history

Browse Claude Code history in an interactive terminal UI:

```bash
claude-proxy tui
# or
claude-proxy history tui
```

This opens the TUI. Press Enter to open the selected session in Claude Code
using the current proxy mode (direct or SSH proxy). Toggle proxy mode with
`Ctrl+P`; if proxy is enabled but not configured, you will be prompted to
enter SSH host/port/user. If no history exists yet, Enter starts a new
session in the current directory.

The Projects column always includes your current working directory and marks
it as `[current]`. The Sessions column always includes a `(New Agent)` entry.

If you have multiple proxy profiles:

```bash
claude-proxy tui --profile <profile>
```

Default data dir is `~/.claude`. You can override it with:

```bash
claude-proxy history --claude-dir /path/to/.claude tui
```

Controls:

- Navigation: Up/Down, PageUp/PageDown (also `j`/`k`)
- Switch pane: Tab / Left / Right (also `h`/`l`)
- Search: `/` then type, Enter apply, Esc cancel (`n`/`N` next/prev in preview)
- Open: Enter (opens in Claude Code and sets cwd)
- New session: `(New Agent)` entry or `Ctrl+N` (in selected project or current dir)
- Proxy mode: `Ctrl+P` toggle (status shows `Proxy mode (Ctrl+P): on/off`)
- YOLO mode: `Ctrl+Y` toggle (`--permission-mode bypassPermissions`, status shows warning)
- Refresh: `r` (or `Ctrl+R`)
- Quit: `q`, `Esc`, `Ctrl+C`
- In-app update: `Ctrl+U` (when an update is available)

If the update check fails, the status bar shows the error.

List sessions as JSON:

```bash
claude-proxy history list --pretty
```

Print a full session by id:

```bash
claude-proxy history show <session-id>
```

Open a session directly in Claude Code:

```bash
claude-proxy history open <session-id>
```

This uses the current proxy mode (direct or SSH proxy). If proxy mode is
enabled but no profile exists, you will be prompted to configure SSH.

If `claude` is not in PATH:

```bash
claude-proxy history --claude-path /path/to/claude tui
```

If Claude Code is not installed, `claude-proxy` will automatically run the
official installer and then continue.

## Upgrade

Upgrade from GitHub Releases:

```bash
claude-proxy upgrade
```

Optional flags:

- `--repo owner/name` (override GitHub repo)
- `--version vX.Y.Z` (install a specific version)
- `--install-path /path/to/claude-proxy` (override install path)

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

