//go:build darwin

package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAdhocCodesignBinary(t *testing.T) {
	if _, err := exec.LookPath("codesign"); err != nil {
		t.Skip("codesign not available")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	bin := filepath.Join(dir, "bin")
	build := exec.Command("go", "build", "-o", bin, src)
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, string(out))
	}

	var log bytes.Buffer
	if err := adhocCodesign(bin, &log); err != nil {
		t.Fatalf("adhocCodesign: %v", err)
	}

	verify := exec.Command("codesign", "--verify", "--verbose=2", bin)
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("codesign verify: %v: %s", err, string(out))
	}
}

func TestAdhocCodesignMissingFile(t *testing.T) {
	if _, err := exec.LookPath("codesign"); err != nil {
		t.Skip("codesign not available")
	}

	path := filepath.Join(t.TempDir(), "missing")
	if err := adhocCodesign(path, io.Discard); err == nil {
		t.Fatalf("expected codesign to fail for missing file")
	}
}
