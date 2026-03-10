package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestDownloadAndVerifyGlibcCompatBundle(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("glibc bundle")
	sum := sha256.Sum256(payload)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	bundlePath := filepath.Join(dir, "bundle.tar.xz")
	if err := downloadURLToFile(server.URL, bundlePath, time.Second); err != nil {
		t.Fatalf("downloadURLToFile error: %v", err)
	}
	checksumPath := filepath.Join(dir, "bundle.tar.xz.sha256")
	if err := os.WriteFile(checksumPath, []byte(hex.EncodeToString(sum[:])+"  bundle.tar.xz\n"), 0o644); err != nil {
		t.Fatalf("write checksum: %v", err)
	}

	if err := verifyBundleSHA256(bundlePath, checksumPath); err != nil {
		t.Fatalf("verifyBundleSHA256 error: %v", err)
	}
	gotSum, err := sha256File(bundlePath)
	if err != nil {
		t.Fatalf("sha256File error: %v", err)
	}
	if gotSum != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected sha256: %s", gotSum)
	}

	if err := os.WriteFile(checksumPath, []byte(strings.Repeat("0", 64)+"  bundle.tar.xz\n"), 0o644); err != nil {
		t.Fatalf("rewrite checksum: %v", err)
	}
	if err := verifyBundleSHA256(bundlePath, checksumPath); err == nil {
		t.Fatalf("expected checksum mismatch")
	}

	if err := downloadIfMissing(server.URL, bundlePath, time.Second); err != nil {
		t.Fatalf("downloadIfMissing should skip existing file: %v", err)
	}
}

func TestReadPatchelfValueAndPatchElfInterpreterAndRPath(t *testing.T) {
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "patchelf.args")
	unix := "#!/bin/sh\nif [ \"$1\" = \"--print-interpreter\" ]; then\n  echo /lib64/ld-linux-x86-64.so.2\n  exit 0\nfi\nif [ \"$1\" = \"--print-rpath\" ]; then\n  echo /usr/lib64\n  exit 0\nfi\nprintf '%s\n' \"$@\" > \"" + recordPath + "\"\nexit 0\n"
	win := "@echo off\r\nif \"%~1\"==\"--print-interpreter\" (\r\n  echo /lib64/ld-linux-x86-64.so.2\r\n  exit /b 0\r\n)\r\nif \"%~1\"==\"--print-rpath\" (\r\n  echo /usr/lib64\r\n  exit /b 0\r\n)\r\nbreak> \"" + recordPath + "\"\r\n:loop\r\nif \"%~1\"==\"\" exit /b 0\r\necho %~1>> \"" + recordPath + "\"\r\nshift\r\ngoto loop\r\n"
	writeStub(t, dir, patchelfBinaryName, unix, win)
	setStubPath(t, dir)

	loader, err := readPatchelfValue("/tmp/claude", "--print-interpreter")
	if err != nil {
		t.Fatalf("readPatchelfValue interpreter error: %v", err)
	}
	if loader != "/lib64/ld-linux-x86-64.so.2" {
		t.Fatalf("unexpected loader path: %q", loader)
	}

	rpath, err := readPatchelfValue("/tmp/claude", "--print-rpath")
	if err != nil {
		t.Fatalf("readPatchelfValue rpath error: %v", err)
	}
	if rpath != "/usr/lib64" {
		t.Fatalf("unexpected rpath: %q", rpath)
	}

	if err := patchElfInterpreterAndRPath("/tmp/claude", "/opt/glibc/lib/ld-linux-x86-64.so.2", "/opt/glibc/lib:/usr/lib64"); err != nil {
		t.Fatalf("patchElfInterpreterAndRPath error: %v", err)
	}
	got, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read patchelf args: %v", err)
	}
	text := string(got)
	if !strings.Contains(text, "--set-interpreter") || !strings.Contains(text, "/opt/glibc/lib:/usr/lib64") {
		t.Fatalf("unexpected patchelf args: %s", text)
	}
}

func TestExtractGlibcCompatBundleUsesTarCommand(t *testing.T) {
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "tar.args")
	bundlePath := filepath.Join(dir, "bundle.tar.xz")
	if err := os.WriteFile(bundlePath, []byte("bundle"), 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	runtimeRoot := filepath.Join(dir, "runtime")
	unix := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"" + recordPath + "\"\nexit 0\n"
	win := "@echo off\r\nbreak> \"" + recordPath + "\"\r\n:loop\r\nif \"%~1\"==\"\" exit /b 0\r\necho %~1>> \"" + recordPath + "\"\r\nshift\r\ngoto loop\r\n"
	writeStub(t, dir, "tar", unix, win)
	setStubPath(t, dir)

	if err := extractGlibcCompatBundle(bundlePath, runtimeRoot); err != nil {
		t.Fatalf("extractGlibcCompatBundle error: %v", err)
	}
	if _, err := os.Stat(runtimeRoot); err != nil {
		t.Fatalf("expected runtime root to exist: %v", err)
	}
	got, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read tar args: %v", err)
	}
	text := string(got)
	if !strings.Contains(text, "-xJf") || !strings.Contains(text, bundlePath) || !strings.Contains(text, runtimeRoot) {
		t.Fatalf("unexpected tar args: %s", text)
	}
}
