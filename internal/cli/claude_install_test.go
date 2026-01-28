package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestInstallerCandidatesLinux(t *testing.T) {
	cmds := installerCandidates("linux")
	if len(cmds) != 1 {
		t.Fatalf("expected 1 linux installer, got %d", len(cmds))
	}
	if cmds[0].path != "bash" {
		t.Fatalf("expected bash installer, got %s", cmds[0].path)
	}
}

func TestInstallerCandidatesWindows(t *testing.T) {
	cmds := installerCandidates("windows")
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 windows installers, got %d", len(cmds))
	}
}

func TestResolveInstallerProxyRequiresProfile(t *testing.T) {
	if _, _, err := resolveInstallerProxy(context.Background(), installProxyOptions{UseProxy: true}); err == nil {
		t.Fatalf("expected error when proxy enabled without profile")
	}
}

func TestRunClaudeInstallerUsesProxyEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	instanceID := "inst-1"
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": instanceID,
		})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })

	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "bash")
	scriptBody := "#!/bin/sh\nprintf \"%s\\n%s\\n\" \"$HTTP_PROXY\" \"$HTTPS_PROXY\" > \"$OUT_FILE\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatalf("write bash script: %v", err)
	}

	t.Setenv("OUT_FILE", outFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	profile := &config.Profile{ID: "profile-1"}
	opts := installProxyOptions{
		UseProxy:  true,
		Profile:   profile,
		Instances: []config.Instance{{ID: instanceID, ProfileID: profile.ID, HTTPPort: port, DaemonPID: os.Getpid()}},
	}

	if err := runClaudeInstaller(context.Background(), io.Discard, opts); err != nil {
		t.Fatalf("runClaudeInstaller: %v", err)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if lines[0] != proxyURL || lines[1] != proxyURL {
		t.Fatalf("expected proxy env %q, got %q", proxyURL, strings.Join(lines, ","))
	}
}
