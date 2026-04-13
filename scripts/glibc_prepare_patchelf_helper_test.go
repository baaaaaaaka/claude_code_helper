package scripts_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requireShellDeps(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s not available", name)
		}
	}
}

func writeTarGzFile(t *testing.T, tarPath string, entries map[string][]byte) {
	t.Helper()
	file, err := os.Create(tarPath)
	if err != nil {
		t.Fatalf("create tar.gz: %v", err)
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	for name, data := range entries {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write tar body %s: %v", name, err)
		}
	}
}

func writeShellStub(t *testing.T, dir string, name string, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
	return path
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestPreparePatchelfHelperScriptBuildsHelperAndChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is not supported on windows")
	}
	requireShellDeps(t, "bash", "curl", "tar")

	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "glibc", "prepare_patchelf_helper.sh")

	base := t.TempDir()
	tarPath := filepath.Join(base, "patchelf.tar.gz")
	payload := []byte("#!/bin/sh\necho helper\n")
	writeTarGzFile(t, tarPath, map[string][]byte{
		"./bin/patchelf": payload,
	})

	stubDir := t.TempDir()
	writeShellStub(t, stubDir, "ldd", "#!/bin/sh\necho 'not a dynamic executable'\nexit 1\n")

	outDir := filepath.Join(base, "out")
	cmd := exec.Command("bash", script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"OUT_DIR="+outDir,
		"PATCHELF_SOURCE_URL=file://"+filepath.ToSlash(tarPath),
		"PATCHELF_SOURCE_SHA256="+sha256Hex(mustReadFile(t, tarPath)),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare_patchelf_helper.sh failed: %v\n%s", err, string(out))
	}

	assetPath := filepath.Join(outDir, "patchelf-linux-x86_64-static")
	data, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatalf("read helper asset: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("unexpected helper asset payload: %q", string(data))
	}
	info, err := os.Stat(assetPath)
	if err != nil {
		t.Fatalf("stat helper asset: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected helper asset to be executable, got mode %v", info.Mode())
	}

	checksumPath := assetPath + ".sha256"
	raw, err := os.ReadFile(checksumPath)
	if err != nil {
		t.Fatalf("read checksum file: %v", err)
	}
	sum := sha256.Sum256(payload)
	if !strings.Contains(string(raw), hex.EncodeToString(sum[:])) {
		t.Fatalf("expected checksum file to contain %s, got %q", hex.EncodeToString(sum[:]), string(raw))
	}
}

func TestPreparePatchelfHelperScriptAcceptsStaticallyLinkedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is not supported on windows")
	}
	requireShellDeps(t, "bash", "curl", "tar")

	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "glibc", "prepare_patchelf_helper.sh")

	base := t.TempDir()
	tarPath := filepath.Join(base, "patchelf.tar.gz")
	payload := []byte("#!/bin/sh\necho helper\n")
	writeTarGzFile(t, tarPath, map[string][]byte{
		"./bin/patchelf": payload,
	})

	stubDir := t.TempDir()
	writeShellStub(t, stubDir, "ldd", "#!/bin/sh\necho 'statically linked'\n")

	outDir := filepath.Join(base, "out")
	cmd := exec.Command("bash", script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"OUT_DIR="+outDir,
		"PATCHELF_SOURCE_URL=file://"+filepath.ToSlash(tarPath),
		"PATCHELF_SOURCE_SHA256="+sha256Hex(mustReadFile(t, tarPath)),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare_patchelf_helper.sh failed: %v\n%s", err, string(out))
	}
	if _, err := os.Stat(filepath.Join(outDir, "patchelf-linux-x86_64-static")); err != nil {
		t.Fatalf("stat helper asset: %v", err)
	}
}

func TestPreparePatchelfHelperScriptRejectsDynamicBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is not supported on windows")
	}
	requireShellDeps(t, "bash", "curl", "tar")

	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "glibc", "prepare_patchelf_helper.sh")

	base := t.TempDir()
	tarPath := filepath.Join(base, "patchelf.tar.gz")
	writeTarGzFile(t, tarPath, map[string][]byte{
		"./bin/patchelf": []byte("#!/bin/sh\necho helper\n"),
	})

	stubDir := t.TempDir()
	writeShellStub(t, stubDir, "ldd", "#!/bin/sh\necho 'linux-vdso.so.1 =>  (0x00007ff...)'\n")

	cmd := exec.Command("bash", script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"OUT_DIR="+filepath.Join(base, "out"),
		"PATCHELF_SOURCE_URL=file://"+filepath.ToSlash(tarPath),
		"PATCHELF_SOURCE_SHA256="+sha256Hex(mustReadFile(t, tarPath)),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected dynamic helper rejection")
	}
	if !strings.Contains(string(out), "statically linked") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestPreparePatchelfHelperScriptRejectsArchiveWithoutBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is not supported on windows")
	}
	requireShellDeps(t, "bash", "curl", "tar")

	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "glibc", "prepare_patchelf_helper.sh")

	base := t.TempDir()
	tarPath := filepath.Join(base, "patchelf.tar.gz")
	writeTarGzFile(t, tarPath, map[string][]byte{
		"./share/doc/patchelf/README.md": []byte("missing binary"),
	})

	stubDir := t.TempDir()
	writeShellStub(t, stubDir, "ldd", "#!/bin/sh\necho 'not a dynamic executable'\n")

	cmd := exec.Command("bash", script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"OUT_DIR="+filepath.Join(base, "out"),
		"PATCHELF_SOURCE_URL=file://"+filepath.ToSlash(tarPath),
		"PATCHELF_SOURCE_SHA256="+sha256Hex(mustReadFile(t, tarPath)),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing helper binary error")
	}
	if !strings.Contains(string(out), "patchelf binary not found") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestPreparePatchelfHelperScriptRejectsSHA256Mismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is not supported on windows")
	}
	requireShellDeps(t, "bash", "curl", "tar")

	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "glibc", "prepare_patchelf_helper.sh")

	base := t.TempDir()
	tarPath := filepath.Join(base, "patchelf.tar.gz")
	writeTarGzFile(t, tarPath, map[string][]byte{
		"./bin/patchelf": []byte("#!/bin/sh\necho helper\n"),
	})

	stubDir := t.TempDir()
	writeShellStub(t, stubDir, "ldd", "#!/bin/sh\necho 'not a dynamic executable'\n")

	cmd := exec.Command("bash", script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"OUT_DIR="+filepath.Join(base, "out"),
		"PATCHELF_SOURCE_URL=file://"+filepath.ToSlash(tarPath),
		"PATCHELF_SOURCE_SHA256="+strings.Repeat("0", 64),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected source sha256 mismatch error")
	}
	if !strings.Contains(string(out), "PATCHELF_SOURCE_SHA256 mismatch") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}
