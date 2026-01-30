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

func TestInstallerCandidatesAndFailures(t *testing.T) {
	t.Run("unknown os has no installers", func(t *testing.T) {
		if cmds := installerCandidates("plan9"); len(cmds) != 0 {
			t.Fatalf("expected no installers, got %d", len(cmds))
		}
	})

	t.Run("runClaudeInstaller with no candidates", func(t *testing.T) {
		t.Setenv("PATH", "")
		err := runClaudeInstaller(context.Background(), io.Discard, installProxyOptions{})
		if err == nil {
			t.Fatalf("expected error when no installer candidates available")
		}
	})

	t.Run("ensureClaudeInstalled propagates installer error", func(t *testing.T) {
		t.Setenv("PATH", "")
		_, err := ensureClaudeInstalled(context.Background(), "", io.Discard, installProxyOptions{})
		if err == nil {
			t.Fatalf("expected error when installer is unavailable")
		}
	})
}

func TestEnsureClaudeInstalledWithMissingPath(t *testing.T) {
	_, err := ensureClaudeInstalled(context.Background(), filepath.Join(t.TempDir(), "missing"), io.Discard, installProxyOptions{})
	if err == nil {
		t.Fatalf("expected error for missing claude path")
	}
}

func TestResolveInstallerProxyNoProxyAndCanceled(t *testing.T) {
	t.Run("use proxy disabled", func(t *testing.T) {
		url, cleanup, err := resolveInstallerProxy(context.Background(), installProxyOptions{UseProxy: false})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "" || cleanup != nil {
			t.Fatalf("expected empty proxy and cleanup, got %q cleanup=%v", url, cleanup != nil)
		}
	})

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := resolveInstallerProxy(ctx, installProxyOptions{
			UseProxy: true,
			Profile:  &config.Profile{ID: "p1"},
		})
		if err == nil {
			t.Fatalf("expected context error")
		}
	})
}

func TestResolveInstallerProxyUsesReusableInstance(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_claude_proxy/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"instanceId": "inst-1",
		})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	tcp, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	profile := &config.Profile{ID: "p1"}
	opts := installProxyOptions{
		UseProxy: true,
		Profile:  profile,
		Instances: []config.Instance{{
			ID:        "inst-1",
			ProfileID: profile.ID,
			HTTPPort:  tcp.Port,
			DaemonPID: os.Getpid(),
		}},
	}
	url, cleanup, err := resolveInstallerProxy(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolveInstallerProxy error: %v", err)
	}
	if cleanup != nil {
		t.Fatalf("expected no cleanup for reusable instance")
	}
	want := fmt.Sprintf("http://127.0.0.1:%d", tcp.Port)
	if url != want {
		t.Fatalf("expected proxy URL %q, got %q", want, url)
	}
}

func TestResolveInstallerProxyMissingProfile(t *testing.T) {
	_, _, err := resolveInstallerProxy(context.Background(), installProxyOptions{UseProxy: true})
	if err == nil {
		t.Fatalf("expected missing profile error")
	}
}

func TestEnsureClaudeInstalledUsesProvidedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("x"), 0o700); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	got, err := ensureClaudeInstalled(context.Background(), path, io.Discard, installProxyOptions{})
	if err != nil {
		t.Fatalf("ensureClaudeInstalled error: %v", err)
	}
	if got != path {
		t.Fatalf("expected path %q, got %q", path, got)
	}
}
