package scripts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sha256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func serveReleaseAssets(t *testing.T, assets map[string][]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, ok := assets[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestVerifyReleaseAssetsScriptDownloadsAndValidates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is not supported on windows")
	}
	requireShellDeps(t, "bash", "curl")

	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "glibc", "verify_release_assets.sh")
	repo := "example/repo"
	tag := "glibc-compat-vtest"
	glibcAsset := "glibc-2.31-centos7-runtime-x86_64.tar.xz"
	patchelfAsset := "patchelf-linux-x86_64-static"
	glibcPayload := []byte("fake-glibc-bundle")
	patchelfPayload := []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo 'patchelf 0.18.0'\n  exit 0\nfi\nexit 1\n")
	server := serveReleaseAssets(t, map[string][]byte{
		"/" + repo + "/releases/download/" + tag + "/" + glibcAsset:                glibcPayload,
		"/" + repo + "/releases/download/" + tag + "/" + glibcAsset + ".sha256":    []byte(sha256Bytes(glibcPayload) + "  " + glibcAsset + "\n"),
		"/" + repo + "/releases/download/" + tag + "/" + patchelfAsset:             patchelfPayload,
		"/" + repo + "/releases/download/" + tag + "/" + patchelfAsset + ".sha256": []byte(sha256Bytes(patchelfPayload) + "  " + patchelfAsset + "\n"),
	})

	outDir := t.TempDir()
	cmd := exec.Command("bash", script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"OUT_DIR="+outDir,
		"RELEASE_BASE_URL="+server.URL,
		"GLIBC_COMPAT_REPO="+repo,
		"GLIBC_COMPAT_TAG="+tag,
		"PATCHELF_REPO="+repo,
		"PATCHELF_TAG="+tag,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify_release_assets.sh failed: %v\n%s", err, string(out))
	}

	for _, path := range []string{
		filepath.Join(outDir, glibcAsset),
		filepath.Join(outDir, glibcAsset+".sha256"),
		filepath.Join(outDir, patchelfAsset),
		filepath.Join(outDir, patchelfAsset+".sha256"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected asset %s: %v", path, err)
		}
	}
}

func TestVerifyReleaseAssetsScriptRejectsChecksumMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is not supported on windows")
	}
	requireShellDeps(t, "bash", "curl")

	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "glibc", "verify_release_assets.sh")
	repo := "example/repo"
	tag := "glibc-compat-vtest"
	glibcAsset := "glibc-2.31-centos7-runtime-x86_64.tar.xz"
	patchelfAsset := "patchelf-linux-x86_64-static"
	glibcPayload := []byte("fake-glibc-bundle")
	patchelfPayload := []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\nexit 1\n")
	server := serveReleaseAssets(t, map[string][]byte{
		"/" + repo + "/releases/download/" + tag + "/" + glibcAsset:                glibcPayload,
		"/" + repo + "/releases/download/" + tag + "/" + glibcAsset + ".sha256":    []byte(strings.Repeat("0", 64) + "  " + glibcAsset + "\n"),
		"/" + repo + "/releases/download/" + tag + "/" + patchelfAsset:             patchelfPayload,
		"/" + repo + "/releases/download/" + tag + "/" + patchelfAsset + ".sha256": []byte(sha256Bytes(patchelfPayload) + "  " + patchelfAsset + "\n"),
	})

	cmd := exec.Command("bash", script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"OUT_DIR="+t.TempDir(),
		"RELEASE_BASE_URL="+server.URL,
		"GLIBC_COMPAT_REPO="+repo,
		"GLIBC_COMPAT_TAG="+tag,
		"PATCHELF_REPO="+repo,
		"PATCHELF_TAG="+tag,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected checksum verification failure")
	}
	if !strings.Contains(string(out), "checksum mismatch") {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestVerifyReleaseAssetsScriptRejectsBrokenPatchelf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is not supported on windows")
	}
	requireShellDeps(t, "bash", "curl")

	repoRoot := repoRootFromScripts(t)
	script := filepath.Join(repoRoot, "scripts", "glibc", "verify_release_assets.sh")
	repo := "example/repo"
	tag := "glibc-compat-vtest"
	glibcAsset := "glibc-2.31-centos7-runtime-x86_64.tar.xz"
	patchelfAsset := "patchelf-linux-x86_64-static"
	glibcPayload := []byte("fake-glibc-bundle")
	patchelfPayload := []byte("#!/bin/sh\nexit 1\n")
	server := serveReleaseAssets(t, map[string][]byte{
		"/" + repo + "/releases/download/" + tag + "/" + glibcAsset:                glibcPayload,
		"/" + repo + "/releases/download/" + tag + "/" + glibcAsset + ".sha256":    []byte(sha256Bytes(glibcPayload) + "  " + glibcAsset + "\n"),
		"/" + repo + "/releases/download/" + tag + "/" + patchelfAsset:             patchelfPayload,
		"/" + repo + "/releases/download/" + tag + "/" + patchelfAsset + ".sha256": []byte(sha256Bytes(patchelfPayload) + "  " + patchelfAsset + "\n"),
	})

	cmd := exec.Command("bash", script)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"OUT_DIR="+t.TempDir(),
		"RELEASE_BASE_URL="+server.URL,
		"GLIBC_COMPAT_REPO="+repo,
		"GLIBC_COMPAT_TAG="+tag,
		"PATCHELF_REPO="+repo,
		"PATCHELF_TAG="+tag,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected patchelf validation failure")
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Fatalf("expected error output for broken patchelf")
	}
}
