package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const claudeTUIProbeTimeout = 20 * time.Second

var errClaudeTUIProbeUnsupported = errors.New("claude TUI probe unsupported")

var ansiControlSequenceRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) Snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func assertClaudeTUIStarts(t *testing.T, path string) {
	t.Helper()

	env, cwd := newClaudeTUIProbeEnv(t)
	out, err := runClaudeTUIProbe(path, cwd, env, claudeTUIProbeTimeout)
	if errors.Is(err, errClaudeTUIProbeUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("claude TUI probe failed: %v\noutput: %s", err, summarizeClaudeTUIOutput(out))
	}
	if !looksLikeClaudeTUI(out) {
		t.Fatalf("claude TUI markers not observed\noutput: %s", summarizeClaudeTUIOutput(out))
	}
}

func newClaudeTUIProbeEnv(t *testing.T) ([]string, string) {
	t.Helper()

	homeDir := t.TempDir()
	cwd := t.TempDir()
	configHome := filepath.Join(homeDir, ".config")
	appData := filepath.Join(homeDir, "AppData", "Roaming")
	localAppData := filepath.Join(homeDir, "AppData", "Local")
	for _, dir := range []string{configHome, appData, localAppData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	env := os.Environ()
	env = setEnvValue(env, "HOME", homeDir)
	env = setEnvValue(env, "USERPROFILE", homeDir)
	env = setEnvValue(env, "XDG_CONFIG_HOME", configHome)
	env = setEnvValue(env, "APPDATA", appData)
	env = setEnvValue(env, "LOCALAPPDATA", localAppData)
	env = setEnvValue(env, "TERM", "xterm-256color")
	env = setEnvValue(env, "COLORTERM", "truecolor")
	return env, cwd
}

func setEnvValue(env []string, key string, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		k, _, ok := strings.Cut(entry, "=")
		if ok && sameEnvKey(k, key) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func sameEnvKey(a string, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func looksLikeClaudeTUI(output string) bool {
	lower := strings.ToLower(normalizeClaudeTUIOutput(output))
	switch {
	case strings.Contains(lower, "welcome to claude code"):
		return true
	case strings.Contains(lower, "claude code") && strings.Contains(lower, "let's get started."):
		return true
	case strings.Contains(lower, "claude code") && strings.Contains(lower, "choose the text style"):
		return true
	case strings.Contains(lower, "claude code") && strings.Contains(lower, "checking connectivity"):
		return true
	case strings.Contains(lower, "claude") && strings.Contains(lower, "sign in") && strings.Contains(lower, "code"):
		return true
	case strings.Contains(lower, "claude code") && strings.Contains(lower, "syntax theme:"):
		return true
	case strings.Contains(lower, "claude code") && strings.Contains(output, "\x1b["):
		return true
	default:
		return false
	}
}

func normalizeClaudeTUIOutput(output string) string {
	output = ansiControlSequenceRE.ReplaceAllString(output, " ")
	output = strings.ReplaceAll(output, "\r", " ")
	output = strings.ReplaceAll(output, "\n", " ")
	return strings.Join(strings.Fields(output), " ")
}

func summarizeClaudeTUIOutput(output string) string {
	if output == "" {
		return "<empty>"
	}

	const edge = 900
	trimmed := output
	if len(trimmed) > edge*2 {
		trimmed = trimmed[:edge] + "\n...\n" + trimmed[len(trimmed)-edge:]
	}
	return strconv.QuoteToASCII(trimmed)
}

func TestLooksLikeClaudeTUI(t *testing.T) {
	if !looksLikeClaudeTUI("\x1b[?2026hWelcome\x1b[1Cto\x1b[1CClaude\x1b[1CCode v2.1.72\r\n") {
		t.Fatalf("expected welcome banner to match")
	}
	if !looksLikeClaudeTUI("Claude Code\r\nLet's get started.\r\n") {
		t.Fatalf("expected onboarding prompt to match")
	}
	if !looksLikeClaudeTUI("Claude Code Syntax theme: Monokai Extended") {
		t.Fatalf("expected theme picker to match")
	}
	if looksLikeClaudeTUI("2.1.72 (Claude Code)\n") {
		t.Fatalf("did not expect plain version output to match")
	}
}
