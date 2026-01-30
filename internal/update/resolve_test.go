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
