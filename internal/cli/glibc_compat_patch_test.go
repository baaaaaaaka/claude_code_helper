package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGlibcCompatLayout(t *testing.T) {
	t.Run("direct root layout", func(t *testing.T) {
		root := t.TempDir()
		libDir := filepath.Join(root, "lib")
		if err := os.MkdirAll(libDir, 0o755); err != nil {
			t.Fatalf("mkdir lib: %v", err)
		}
		if err := os.WriteFile(filepath.Join(libDir, "ld-linux-x86-64.so.2"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write loader: %v", err)
		}
		if err := os.WriteFile(filepath.Join(libDir, "libc.so.6"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write libc: %v", err)
		}

		layout, err := resolveGlibcCompatLayout(root)
		if err != nil {
			t.Fatalf("resolve layout: %v", err)
		}
		if layout.RootDir != root {
			t.Fatalf("expected root %q, got %q", root, layout.RootDir)
		}
	})

	t.Run("bundle root with glibc subdir", func(t *testing.T) {
		root := t.TempDir()
		glibcRoot := filepath.Join(root, "glibc-2.31")
		libDir := filepath.Join(glibcRoot, "lib")
		if err := os.MkdirAll(libDir, 0o755); err != nil {
			t.Fatalf("mkdir lib: %v", err)
		}
		if err := os.WriteFile(filepath.Join(libDir, "ld-linux-x86-64.so.2"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write loader: %v", err)
		}
		if err := os.WriteFile(filepath.Join(libDir, "libc.so.6"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write libc: %v", err)
		}

		layout, err := resolveGlibcCompatLayout(root)
		if err != nil {
			t.Fatalf("resolve layout: %v", err)
		}
		if layout.RootDir != glibcRoot {
			t.Fatalf("expected root %q, got %q", glibcRoot, layout.RootDir)
		}
	})
}

func TestResolveGlibcCompatLayoutMissing(t *testing.T) {
	_, err := resolveGlibcCompatLayout(t.TempDir())
	if err == nil {
		t.Fatalf("expected missing layout error")
	}
}

func TestIsMissingGlibcSymbolError(t *testing.T) {
	if !isMissingGlibcSymbolError("/tmp/claude: /lib64/libc.so.6: version `GLIBC_2.25' not found") {
		t.Fatalf("expected GLIBC missing symbol output to be detected")
	}
	if isMissingGlibcSymbolError("claude failed with unknown error") {
		t.Fatalf("did not expect non-GLIBC output to match")
	}
}

func TestMergeRPathAndContains(t *testing.T) {
	merged := mergeRPath("/opt/glibc/lib", "/usr/lib:/opt/glibc/lib:/lib64")
	if merged != "/opt/glibc/lib:/usr/lib:/lib64" {
		t.Fatalf("unexpected merged rpath: %q", merged)
	}
	if !pathListContains(merged, "/opt/glibc/lib") {
		t.Fatalf("expected merged rpath to contain glibc lib dir")
	}
}

func TestFirstSHA256Token(t *testing.T) {
	raw := "42c5a00561352e4e7504f38bd1d15e7a4da1fca2288558981e14b25bbf91b344  /out/glibc.tar.xz\n"
	got := firstSHA256Token(raw)
	if got != "42c5a00561352e4e7504f38bd1d15e7a4da1fca2288558981e14b25bbf91b344" {
		t.Fatalf("unexpected checksum token: %q", got)
	}
}

func TestResolveGlibcCompatRepoAndTag(t *testing.T) {
	t.Run("explicit glibc repo and tag env", func(t *testing.T) {
		t.Setenv(glibcCompatRepoEnv, "foo/bar")
		t.Setenv(glibcCompatTagEnv, "glibc-compat-vX")
		if got := resolveGlibcCompatRepo(); got != "foo/bar" {
			t.Fatalf("expected repo foo/bar, got %q", got)
		}
		if got := resolveGlibcCompatTag(); got != "glibc-compat-vX" {
			t.Fatalf("expected custom tag, got %q", got)
		}
	})

	t.Run("default glibc tag", func(t *testing.T) {
		t.Setenv(glibcCompatTagEnv, "")
		if got := resolveGlibcCompatTag(); got != glibcCompatDefaultTag {
			t.Fatalf("expected default tag %q, got %q", glibcCompatDefaultTag, got)
		}
	})
}
