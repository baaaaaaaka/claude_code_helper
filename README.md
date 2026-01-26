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

### 2) Browse Claude Code history

```bash
claude-proxy
```

This opens the TUI. Press Enter to open the selected session in Claude Code
through the SSH proxy (and apply the optional exe patch if enabled).

If you have multiple profiles, select one:

```bash
claude-proxy <profile>
```

### 3) Run any command through the proxy

```bash
claude-proxy run <profile> -- <cmd> [args...]
```

Example:

```bash
claude-proxy run pdx -- curl https://example.com
```

### Optional: patch the target executable before run

You can apply 3-stage regex patches to the target executable before it starts (for example, the default `claude`):

```bash
claude-proxy \
  --exe-patch-regex-1 '<stage-1>' \
  --exe-patch-regex-2 '<stage-2>' \
  --exe-patch-regex-3 '<stage-3>' \
  --exe-patch-replace '<replacement>' \
  -- claude
```

- Stage 1 selects candidate code blocks in the executable.
- Stage 2 checks whether a stage 1 block should be patched (repeatable).
- Stage 3 runs a regex replacement inside the stage 1 block (repeatable, supports `$1`-style expansions).

Built-in policySettings patch (length-preserving by replacing a statement inside the block, prints before/after matches):

```bash
claude-proxy --exe-patch-policy-settings --exe-patch-preview -- claude
```

## Claude Code history

Browse Claude Code history in an interactive terminal UI:

```bash
claude-proxy tui
# or
claude-proxy history tui
```

If you have multiple proxy profiles:

```bash
claude-proxy tui --profile <profile>
```

Default data dir is `~/.claude`. You can override it with:

```bash
claude-proxy history --claude-dir /path/to/.claude tui
```

Controls:

- Navigation: Up/Down, PageUp/PageDown
- Switch pane: Tab / Left / Right
- Search: `/` then type, Enter apply, Esc cancel
- Open: Enter (opens in Claude Code and sets cwd)
- Refresh: `r`
- Quit: `q`
- In-app update: `Ctrl+U` (when an update is available)

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

