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

The installer drops a `clp` shim alongside `claude-proxy` and tries to prepare
PATH for both the `claude-proxy` install directory and Claude Code's native
launcher directory. On PowerShell it also installs a `clp` function in the
profile, intentionally overriding PowerShell's built-in `clp`
(`Clear-ItemProperty`) alias so `clp` launches `claude-proxy` directly. Open a
new shell if the command is not found.

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
- Run any command using the current direct/proxy mode:
  `claude-proxy run -- <cmd> [args...]`.
- Force SSH proxy mode with a profile:
  `claude-proxy run <profile> -- <cmd> [args...]`.
- Example: `claude-proxy run pdx -- curl https://example.com`.

### Optional: preconfigure a proxy profile

```bash
claude-proxy init
```

Config is stored under your OS user config directory (Linux typically
`~/.config/claude-proxy/config.json`).

## Requirements (runtime)

- Direct mode does not require SSH.
- SSH proxy mode requires `ssh` (OpenSSH client).
- `ssh-keygen` is optional (only needed when proxy mode creates a dedicated key).
- On Linux, `patchelf` is optional and only needed if Claude must be patched for
  glibc compatibility.
- On Linux, if the glibc compat runtime must be auto-downloaded and extracted
  (`--exe-patch-glibc-root` unset and no cached runtime yet), `tar` is also
  required.

Check the SSH/proxy prerequisites:

```bash
claude-proxy proxy doctor
```

## Install (no root)

### Linux / macOS (one-liner, auto-detects curl/wget)

```bash
sh -c 'url="https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.sh"; if command -v curl >/dev/null 2>&1; then curl -fsSL "$url" | sh; elif command -v wget >/dev/null 2>&1; then wget -qO- "$url" | sh; else echo "need curl or wget" >&2; exit 1; fi'
```

By default it installs to `~/.local/bin/claude-proxy`.

The installer drops a `clp` shim alongside `claude-proxy` and tries to prepare
PATH for both the chosen install directory and Claude Code's native launcher
directory (`~/.local/bin` by default), plus a `clp` alias where appropriate.
Open a new shell if the command is not found.
If you need to update PATH manually:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Install a specific version (example):

```bash
curl -fsSL https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.sh | sh -s -- --version vX.Y.Z
```

### Windows (PowerShell one-liner)

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "iwr -useb https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.ps1 | iex"
```

Install a specific version:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "$u='https://raw.githubusercontent.com/baaaaaaaka/claude_code_helper/main/install.ps1'; $p=Join-Path $env:TEMP 'claude-proxy-install.ps1'; iwr -useb $u -OutFile $p; & $p -Version vX.Y.Z; Remove-Item -Force $p"
```

## Claude Code compatibility

See [`docs/claude_code_compatibility.md`](docs/claude_code_compatibility.md) for
the automatically maintained table of Claude Code versions and per-platform
patch test results (linux/mac/windows + linux distros).

## Claude Code install and patching

- When opening Claude Code from the TUI or history commands, if `claude` is
  missing, `claude-proxy` can install it automatically with the official
  installer.
- On Windows, if that installer needs Git Bash, `claude-proxy` can bootstrap a
  private Git for Windows runtime and retry automatically.
- When launching `claude` with `--permission-mode bypassPermissions`,
  `claude-proxy` enables its built-in Claude Code byte patches by default.
  Without that argument, those Claude-specific byte patches are disabled and
  any previously-applied Claude patch is restored before launch.
- Built-in Claude patches target `policySettings` and
  `--permission-mode bypassPermissions` guards.
- On Linux, if `claude` fails with missing GLIBC symbols,
  `claude-proxy` can apply a `patchelf`-based glibc compatibility patch.
  If `--exe-patch-glibc-root` is not set, the compat runtime is auto-downloaded
  from GitHub release assets on supported linux/amd64 builds.
- Use `claude-proxy --help` to see the available `--exe-patch-*` flags if you
  want to tune or disable this behavior.

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
it as `[current]`. The Sessions column always includes a `(New Agent)` entry,
and sessions with subagents can be expanded or collapsed with `Ctrl+O`.

If you have multiple proxy profiles:

```bash
claude-proxy tui --profile <profile>
```

You can also change the auto-refresh interval (default `5s`, `0` disables it):

```bash
claude-proxy tui --refresh-interval 30s
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
- Subagents: `Ctrl+O` expand/collapse selected session
- Proxy mode: `Ctrl+P` toggle (status shows `Proxy mode (Ctrl+P): on/off`)
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
official installer and then continue. On Windows, if that installer needs
Git Bash, `claude-proxy` will install a private Git for Windows runtime and
retry automatically.

## Upgrade

Upgrade from GitHub Releases:

```bash
claude-proxy upgrade
```

Optional flags:

- `--repo owner/name` (override GitHub repo)
- `--version vX.Y.Z` (install a specific version)
- `--install-path /path/to/claude-proxy` (override install path)

Upgrade or install Claude Code explicitly:

```bash
claude-proxy upgrade-claude
# or, if your saved config is using proxy mode
claude-proxy upgrade-claude --profile <profile>
```

`upgrade-claude` expects `claude-proxy` to have already created its config
file, for example via a first run of `claude-proxy` or `claude-proxy init`.
If you previously chose direct mode, `--profile` is ignored until you
re-enable proxy mode first (for example with `Ctrl+P` in the TUI).

## Long-lived instances (optional)

Start a reusable daemon instance:

```bash
claude-proxy proxy start [profile]
claude-proxy proxy list
```

Stop an instance:

```bash
claude-proxy proxy stop <instance-id>
```

Clean up dead or unhealthy instances:

```bash
claude-proxy proxy prune
```
