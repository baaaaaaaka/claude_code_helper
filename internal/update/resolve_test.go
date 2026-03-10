package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveInstallPath(t *testing.T) {
	t.Run("explicit directory", func(t *testing.T) {
		dir := t.TempDir()
		got, err := ResolveInstallPath(dir)
		if err != nil {
			t.Fatalf("ResolveInstallPath error: %v", err)
		}
		want := filepath.Join(dir, binaryName())
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("explicit file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "bin")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		got, err := ResolveInstallPath(path)
		if err != nil {
			t.Fatalf("ResolveInstallPath error: %v", err)
		}
		if got != filepath.Clean(path) {
			t.Fatalf("expected %q, got %q", filepath.Clean(path), got)
		}
	})

	t.Run("env directory", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(EnvInstallDir, dir)
		got, err := ResolveInstallPath("")
		if err != nil {
			t.Fatalf("ResolveInstallPath error: %v", err)
		}
		want := filepath.Join(dir, binaryName())
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("falls back to current executable", func(t *testing.T) {
		t.Setenv(EnvInstallDir, "")
		got, err := ResolveInstallPath("")
		if err != nil {
			t.Fatalf("ResolveInstallPath error: %v", err)
		}
		exe, err := os.Executable()
		if err != nil {
			t.Fatalf("os.Executable error: %v", err)
		}
		if got != filepath.Clean(exe) {
			t.Fatalf("expected executable path %q, got %q", filepath.Clean(exe), got)
		}
	})
}

func TestBinaryName(t *testing.T) {
	if runtime.GOOS == "windows" {
		if got := binaryName(); got != "claude-proxy.exe" {
			t.Fatalf("expected windows binary name, got %q", got)
		}
		return
	}
	if got := binaryName(); got != "claude-proxy" {
		t.Fatalf("expected unix binary name, got %q", got)
	}
}

func TestResolveRepoAndVersion(t *testing.T) {
	t.Run("ResolveRepo prefers explicit then env", func(t *testing.T) {
		t.Setenv(EnvRepo, "env/repo")
		if got := ResolveRepo("explicit/repo"); got != "explicit/repo" {
			t.Fatalf("expected explicit repo, got %q", got)
		}
		if got := ResolveRepo(""); got != "env/repo" {
			t.Fatalf("expected env repo, got %q", got)
		}
	})

	t.Run("ResolveRepo falls back to default", func(t *testing.T) {
		t.Setenv(EnvRepo, "")
		if got := ResolveRepo(""); got != DefaultRepo {
			t.Fatalf("expected default repo, got %q", got)
		}
	})

	t.Run("ResolveVersion prefers explicit then env", func(t *testing.T) {
		t.Setenv(EnvVersion, "1.2.3")
		if got := ResolveVersion("2.0.0"); got != "2.0.0" {
			t.Fatalf("expected explicit version, got %q", got)
		}
		if got := ResolveVersion(""); got != "1.2.3" {
			t.Fatalf("expected env version, got %q", got)
		}
	})

	t.Run("ResolveVersion falls back to latest", func(t *testing.T) {
		t.Setenv(EnvVersion, "")
		if got := ResolveVersion(""); got != "latest" {
			t.Fatalf("expected latest, got %q", got)
		}
	})
}

func TestLatestTagFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/owner/repo/releases/tag/v1.2.3", "v1.2.3"},
		{"/owner/repo/releases/latest", "latest"},
		{"/v2.0.0", "v2.0.0"},
		{"", ""},
	}

	for _, tc := range cases {
		if got := latestTagFromPath(tc.path); got != tc.want {
			t.Fatalf("latestTagFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestIsVersionNewer(t *testing.T) {
	cases := []struct {
		remote  string
		local   string
		wantNew bool
		wantOK  bool
	}{
		{"1.2.4", "1.2.3", true, true},
		{"1.2.3", "1.2.3", false, true},
		{"1.2.3", "1.2.4", false, true},
		{"v2.0.0-beta.1", "1.9.9", true, true},
		{"bad", "1.0.0", false, false},
		{"1.0.0", "bad", false, false},
	}

	for _, tc := range cases {
		gotNew, gotOK := isVersionNewer(tc.remote, tc.local)
		if gotNew != tc.wantNew || gotOK != tc.wantOK {
			t.Fatalf("isVersionNewer(%q, %q) = (%v, %v), want (%v, %v)", tc.remote, tc.local, gotNew, gotOK, tc.wantNew, tc.wantOK)
		}
	}
}
