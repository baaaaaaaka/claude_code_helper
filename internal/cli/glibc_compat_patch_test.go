package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeGlibcCompatRuntimeFixture(t *testing.T, root string, loaderData string, libcData string) glibcCompatLayout {
	t.Helper()
	glibcRoot := filepath.Join(root, "glibc-2.31")
	libDir := filepath.Join(glibcRoot, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir glibc lib: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "ld-linux-x86-64.so.2"), []byte(loaderData), 0o755); err != nil {
		t.Fatalf("write loader: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "libc.so.6"), []byte(libcData), 0o644); err != nil {
		t.Fatalf("write libc: %v", err)
	}
	layout, err := resolveGlibcCompatLayout(root)
	if err != nil {
		t.Fatalf("resolveGlibcCompatLayout error: %v", err)
	}
	return layout
}

func assertGlibcCompatMirrorLaunchPrefix(t *testing.T, prefix []string, targetPath string, libDir string) {
	t.Helper()
	if len(prefix) != 3 {
		t.Fatalf("unexpected mirror launch prefix length: %#v", prefix)
	}
	if base := strings.ToLower(filepath.Base(prefix[0])); base != "env" && base != "env.exe" {
		t.Fatalf("expected env launcher, got %q", prefix[0])
	}
	if !strings.HasPrefix(prefix[1], "LD_LIBRARY_PATH=") {
		t.Fatalf("expected LD_LIBRARY_PATH assignment, got %q", prefix[1])
	}
	if !strings.Contains(prefix[1], libDir) {
		t.Fatalf("expected LD_LIBRARY_PATH to include %q, got %q", libDir, prefix[1])
	}
	if !sameFilePath(prefix[2], targetPath) {
		t.Fatalf("expected target path %q at end of launch prefix, got %#v", targetPath, prefix)
	}
}

func setCompatReleaseBaseURL(t *testing.T, url string) {
	t.Helper()
	prev := glibcCompatReleaseBaseURL
	glibcCompatReleaseBaseURL = url
	t.Cleanup(func() { glibcCompatReleaseBaseURL = prev })
}

func patchelfHelperScript(recordPath string) []byte {
	return []byte("#!/bin/sh\nif [ \"$1\" = \"--print-interpreter\" ]; then\n  echo /lib64/ld-linux-x86-64.so.2\n  exit 0\nfi\nif [ \"$1\" = \"--print-rpath\" ]; then\n  echo /usr/lib64\n  exit 0\nfi\nprintf '%s\n' \"$@\" > \"" + recordPath + "\"\nexit 0\n")
}

func serveCompatReleaseAsset(t *testing.T, repo string, tag string, asset string, payload []byte) (*httptest.Server, *int, *int) {
	t.Helper()
	sum := sha256.Sum256(payload)
	assetRequests := 0
	checksumRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + repo + "/releases/download/" + tag + "/" + asset:
			assetRequests++
			_, _ = w.Write(payload)
		case "/" + repo + "/releases/download/" + tag + "/" + asset + ".sha256":
			checksumRequests++
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &assetRequests, &checksumRequests
}

func patchelfHelperCachePaths(t *testing.T, repo string, tag string) (string, string) {
	t.Helper()
	cacheBase, err := resolveStableCacheBase()
	if err != nil {
		t.Fatalf("resolveStableCacheBase error: %v", err)
	}
	asset, err := patchelfAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("patchelfAssetName error: %v", err)
	}
	cacheRoot := filepath.Join(cacheBase, "claude-proxy", "tools", "patchelf", sanitizePathComponent(repo), sanitizePathComponent(tag))
	binaryPath := filepath.Join(cacheRoot, asset)
	return binaryPath, binaryPath + ".sha256"
}

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

func TestResolveStableCacheBaseFallsBackToTempDir(t *testing.T) {
	prevUserCacheDirFn := userCacheDirFn
	prevUserHomeDirFn := userHomeDirFn
	prevTempDirFn := tempDirFn
	userCacheDirFn = func() (string, error) { return "", errors.New("no user cache") }
	userHomeDirFn = func() (string, error) { return "", errors.New("no home") }
	tempDirFn = func() string { return "/tmp/claude-proxy-fallback" }
	t.Cleanup(func() {
		userCacheDirFn = prevUserCacheDirFn
		userHomeDirFn = prevUserHomeDirFn
		tempDirFn = prevTempDirFn
	})

	got, err := resolveStableCacheBase()
	if err != nil {
		t.Fatalf("resolveStableCacheBase error: %v", err)
	}
	if got != "/tmp/claude-proxy-fallback" {
		t.Fatalf("expected temp-dir fallback, got %q", got)
	}
}

func TestResolveHostIDUsesEnvOverride(t *testing.T) {
	t.Setenv(claudeProxyHostIDEnv, "host id/with spaces")
	if got := resolveHostID(); got != "host_id_with_spaces" {
		t.Fatalf("unexpected host id: %q", got)
	}
}

func TestLinuxOSReleaseFieldAndEL7HostDetection(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("host detection is only meaningful on linux/amd64")
	}
	prevReadOSReleaseFn := readLinuxOSReleaseFn
	t.Cleanup(func() { readLinuxOSReleaseFn = prevReadOSReleaseFn })

	readLinuxOSReleaseFn = func() ([]byte, error) {
		return []byte("ID=centos\nVERSION_ID=\"7\"\nID_LIKE=\"rhel fedora\"\n"), nil
	}
	if got := linuxOSReleaseField("ID"); got != "centos" {
		t.Fatalf("unexpected ID field: %q", got)
	}
	if !isEL7GlibcCompatHost() {
		t.Fatalf("expected EL7 host detection to match centos 7")
	}

	readLinuxOSReleaseFn = func() ([]byte, error) {
		return []byte("ID=rocky\nVERSION_ID=\"8.10\"\nID_LIKE=\"rhel centos fedora\"\n"), nil
	}
	if isEL7GlibcCompatHost() {
		t.Fatalf("did not expect rocky 8 host to match EL7 compat auto mode")
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

	t.Run("asset and release url", func(t *testing.T) {
		asset, err := glibcCompatAssetName("linux", "amd64")
		if err != nil {
			t.Fatalf("glibcCompatAssetName error: %v", err)
		}
		if asset != "glibc-2.31-centos7-runtime-x86_64.tar.xz" {
			t.Fatalf("unexpected asset name: %q", asset)
		}
		if _, err := glibcCompatAssetName("darwin", "arm64"); err == nil {
			t.Fatalf("expected unsupported platform error")
		}
		gotURL := glibcCompatReleaseURL("owner/repo", "tag", asset)
		wantURL := "https://github.com/owner/repo/releases/download/tag/" + asset
		if gotURL != wantURL {
			t.Fatalf("unexpected release url: %q", gotURL)
		}
	})
}

func TestResolvePatchelfRepoAndTag(t *testing.T) {
	t.Run("explicit patchelf repo and tag env", func(t *testing.T) {
		t.Setenv(patchelfRepoEnv, "foo/tools")
		t.Setenv(patchelfTagEnv, "patchelf-vX")
		if got := resolvePatchelfRepo(); got != "foo/tools" {
			t.Fatalf("expected repo foo/tools, got %q", got)
		}
		if got := resolvePatchelfTag(); got != "patchelf-vX" {
			t.Fatalf("expected custom patchelf tag, got %q", got)
		}
	})

	t.Run("default patchelf repo and tag follow glibc compat settings", func(t *testing.T) {
		t.Setenv(glibcCompatRepoEnv, "foo/bar")
		t.Setenv(glibcCompatTagEnv, "glibc-compat-vX")
		t.Setenv(patchelfRepoEnv, "")
		t.Setenv(patchelfTagEnv, "")
		if got := resolvePatchelfRepo(); got != "foo/bar" {
			t.Fatalf("expected patchelf repo to follow glibc compat repo, got %q", got)
		}
		if got := resolvePatchelfTag(); got != "glibc-compat-vX" {
			t.Fatalf("expected patchelf tag to follow glibc compat tag, got %q", got)
		}
	})

	t.Run("asset and release url", func(t *testing.T) {
		asset, err := patchelfAssetName("linux", "amd64")
		if err != nil {
			t.Fatalf("patchelfAssetName error: %v", err)
		}
		if asset != "patchelf-linux-x86_64-static" {
			t.Fatalf("unexpected patchelf asset name: %q", asset)
		}
		if _, err := patchelfAssetName("darwin", "arm64"); err == nil {
			t.Fatalf("expected unsupported patchelf platform error")
		}
		gotURL := patchelfReleaseURL("owner/repo", "tag", asset)
		wantURL := "https://github.com/owner/repo/releases/download/tag/" + asset
		if gotURL != wantURL {
			t.Fatalf("unexpected patchelf release url: %q", gotURL)
		}
	})
}

func TestResolvePatchelfBinarySupportsExplicitPathOverride(t *testing.T) {
	overrideDir := t.TempDir()
	overridePath := filepath.Join(overrideDir, "patchelf-custom")
	if err := os.WriteFile(overridePath, patchelfHelperScript(filepath.Join(overrideDir, "unused.args")), 0o755); err != nil {
		t.Fatalf("write override helper: %v", err)
	}
	t.Setenv(patchelfPathEnv, overridePath)
	t.Setenv("PATH", "")

	got, err := resolvePatchelfBinary(io.Discard)
	if err != nil {
		t.Fatalf("resolvePatchelfBinary error: %v", err)
	}
	if got != overridePath {
		t.Fatalf("expected explicit override %q, got %q", overridePath, got)
	}
}

func TestResolvePatchelfBinaryRejectsInvalidOverride(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing path", func(t *testing.T) {
		t.Setenv(patchelfPathEnv, filepath.Join(dir, "missing-patchelf"))
		if _, err := resolvePatchelfBinary(io.Discard); err == nil {
			t.Fatalf("expected missing override path error")
		}
	})

	t.Run("directory path", func(t *testing.T) {
		t.Setenv(patchelfPathEnv, dir)
		if _, err := resolvePatchelfBinary(io.Discard); err == nil || !strings.Contains(err.Error(), "points to a directory") {
			t.Fatalf("expected directory override error, got %v", err)
		}
	})

	t.Run("unusable executable", func(t *testing.T) {
		overridePath := filepath.Join(dir, "bad-patchelf")
		if err := os.WriteFile(overridePath, []byte("#!/bin/sh\necho unusable >&2\nexit 127\n"), 0o755); err != nil {
			t.Fatalf("write unusable override: %v", err)
		}
		t.Setenv(patchelfPathEnv, overridePath)
		if _, err := resolvePatchelfBinary(io.Discard); err == nil || !strings.Contains(err.Error(), "validate "+patchelfPathEnv) {
			t.Fatalf("expected unusable override validation error, got %v", err)
		}
	})
}

func TestResolvePatchelfBinaryPrefersSystemPathOverDownload(t *testing.T) {
	stubDir := t.TempDir()
	recordPath := filepath.Join(stubDir, "patchelf.args")
	writeStub(t, stubDir, patchelfBinaryName, string(patchelfHelperScript(recordPath)), "@echo off\r\nexit /b 1\r\n")
	setStubPath(t, stubDir)

	repo := "owner/repo"
	tag := "patchelf-test"
	t.Setenv(patchelfRepoEnv, repo)
	t.Setenv(patchelfTagEnv, tag)

	asset := "patchelf-linux-x86_64-static"
	server, assetRequests, checksumRequests := serveCompatReleaseAsset(t, repo, tag, asset, patchelfHelperScript(recordPath))
	setCompatReleaseBaseURL(t, server.URL)

	path, err := resolvePatchelfBinary(io.Discard)
	if err != nil {
		t.Fatalf("resolvePatchelfBinary error: %v", err)
	}
	wantPath, err := exec.LookPath(patchelfBinaryName)
	if err != nil {
		t.Fatalf("LookPath(%s) error: %v", patchelfBinaryName, err)
	}
	if !sameFilePath(path, wantPath) {
		t.Fatalf("expected PATH patchelf %q, got %q", wantPath, path)
	}
	if *assetRequests != 0 || *checksumRequests != 0 {
		t.Fatalf("expected no helper download when PATH patchelf exists, got asset=%d checksum=%d", *assetRequests, *checksumRequests)
	}
}

func TestResolvePatchelfBinaryFallsBackWhenSystemPathIsUnusable(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("automatic patchelf helper download only applies on linux/amd64")
	}
	cacheBase := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheBase)

	stubDir := t.TempDir()
	recordPath := filepath.Join(t.TempDir(), "patchelf.args")
	writeStub(t, stubDir, patchelfBinaryName, "#!/bin/sh\necho unusable >&2\nexit 127\n", "@echo off\r\necho unusable 1>&2\r\nexit /b 127\r\n")
	setStubPath(t, stubDir)

	repo := "owner/repo"
	tag := "patchelf-test"
	t.Setenv(patchelfRepoEnv, repo)
	t.Setenv(patchelfTagEnv, tag)

	asset, err := patchelfAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("patchelfAssetName error: %v", err)
	}
	server, assetRequests, checksumRequests := serveCompatReleaseAsset(t, repo, tag, asset, patchelfHelperScript(recordPath))
	setCompatReleaseBaseURL(t, server.URL)

	var log strings.Builder
	path, err := resolvePatchelfBinary(&log)
	if err != nil {
		t.Fatalf("resolvePatchelfBinary error: %v", err)
	}
	wantPath, _ := patchelfHelperCachePaths(t, repo, tag)
	if path != wantPath {
		t.Fatalf("expected helper download path %q, got %q", wantPath, path)
	}
	if *assetRequests != 1 || *checksumRequests != 1 {
		t.Fatalf("expected unusable PATH patchelf to trigger helper download, got asset=%d checksum=%d", *assetRequests, *checksumRequests)
	}
	if !strings.Contains(log.String(), "ignoring unusable patchelf") {
		t.Fatalf("expected unusable PATH patchelf log, got %q", log.String())
	}
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

func TestEnsureDefaultGlibcCompatRuntimeUsesSeededBundle(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("automatic glibc compat runtime only applies on linux/amd64")
	}
	cacheBase := t.TempDir()
	stubDir := t.TempDir()
	tag := "glibc-compat-test"
	t.Setenv("XDG_CACHE_HOME", cacheBase)
	t.Setenv(claudeProxyHostIDEnv, "host-a")
	t.Setenv(glibcCompatTagEnv, tag)

	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		t.Fatalf("resolveClaudeProxyHostRoot error: %v", err)
	}
	cacheRoot := filepath.Join(hostRoot, "glibc-compat", sanitizePathComponent(tag))
	asset, err := glibcCompatAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("glibcCompatAssetName error: %v", err)
	}
	bundlePayload := []byte("seeded bundle")
	bundlePath := filepath.Join(cacheRoot, asset)
	checksumPath := bundlePath + ".sha256"
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatalf("mkdir cache root: %v", err)
	}
	if err := os.WriteFile(bundlePath, bundlePayload, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	sum := sha256.Sum256(bundlePayload)
	if err := os.WriteFile(checksumPath, []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644); err != nil {
		t.Fatalf("write checksum: %v", err)
	}

	recordPath := filepath.Join(stubDir, "tar.args")
	unix := "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = \"-C\" ]; then\n    out=\"$2\"\n    shift 2\n    continue\n  fi\n  shift\n done\nprintf '%s\\n' \"$@\" > \"" + recordPath + "\"\nmkdir -p \"$out/glibc-2.31/lib\"\nprintf loader > \"$out/glibc-2.31/lib/ld-linux-x86-64.so.2\"\nprintf libc > \"$out/glibc-2.31/lib/libc.so.6\"\nexit 0\n"
	win := "@echo off\r\nexit /b 1\r\n"
	writeStub(t, stubDir, "tar", unix, win)
	setStubPath(t, stubDir)

	runtimeRoot, err := ensureDefaultGlibcCompatRuntime(io.Discard)
	if err != nil {
		t.Fatalf("ensureDefaultGlibcCompatRuntime error: %v", err)
	}
	layout, err := resolveGlibcCompatLayout(runtimeRoot)
	if err != nil {
		t.Fatalf("resolveGlibcCompatLayout error: %v", err)
	}
	if layout.RootDir == "" || !strings.HasPrefix(runtimeRoot, cacheRoot) {
		t.Fatalf("unexpected runtime root: %q", runtimeRoot)
	}

	secondRoot, err := ensureDefaultGlibcCompatRuntime(io.Discard)
	if err != nil {
		t.Fatalf("second ensureDefaultGlibcCompatRuntime error: %v", err)
	}
	if secondRoot != runtimeRoot {
		t.Fatalf("expected cached runtime root %q, got %q", runtimeRoot, secondRoot)
	}
}

func TestResolvePatchelfBinaryDownloadsHelperWhenMissing(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("automatic patchelf helper download only applies on linux/amd64")
	}
	cacheBase := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheBase)
	emptyPath := filepath.Join(t.TempDir(), "empty-bin")
	if err := os.MkdirAll(emptyPath, 0o755); err != nil {
		t.Fatalf("mkdir empty bin: %v", err)
	}
	t.Setenv("PATH", emptyPath)

	repo := "owner/repo"
	tag := "patchelf-test"
	t.Setenv(patchelfRepoEnv, repo)
	t.Setenv(patchelfTagEnv, tag)

	recordPath := filepath.Join(t.TempDir(), "patchelf.args")
	asset, err := patchelfAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("patchelfAssetName error: %v", err)
	}
	server, assetRequests, checksumRequests := serveCompatReleaseAsset(t, repo, tag, asset, patchelfHelperScript(recordPath))
	setCompatReleaseBaseURL(t, server.URL)

	path, err := resolvePatchelfBinary(io.Discard)
	if err != nil {
		t.Fatalf("resolvePatchelfBinary error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat downloaded patchelf helper: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected downloaded patchelf helper to be executable, got mode %v", info.Mode())
	}
	out, err := runPatchelfWithBinary(path, "--print-interpreter", "/tmp/claude")
	if err != nil {
		t.Fatalf("runPatchelfWithBinary error: %v", err)
	}
	if strings.TrimSpace(string(out)) != "/lib64/ld-linux-x86-64.so.2" {
		t.Fatalf("unexpected helper output: %q", string(out))
	}

	secondPath, err := resolvePatchelfBinary(io.Discard)
	if err != nil {
		t.Fatalf("second resolvePatchelfBinary error: %v", err)
	}
	if secondPath != path {
		t.Fatalf("expected cached patchelf helper path %q, got %q", path, secondPath)
	}
	if *assetRequests != 1 || *checksumRequests != 1 {
		t.Fatalf("expected one asset+checksum download, got asset=%d checksum=%d", *assetRequests, *checksumRequests)
	}
}

func TestResolvePatchelfBinaryRedownloadsCorruptCache(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("automatic patchelf helper download only applies on linux/amd64")
	}
	cacheBase := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheBase)
	emptyPath := filepath.Join(t.TempDir(), "empty-bin")
	if err := os.MkdirAll(emptyPath, 0o755); err != nil {
		t.Fatalf("mkdir empty bin: %v", err)
	}
	t.Setenv("PATH", emptyPath)

	repo := "owner/repo"
	tag := "patchelf-test"
	t.Setenv(patchelfRepoEnv, repo)
	t.Setenv(patchelfTagEnv, tag)

	binaryPath, checksumPath := patchelfHelperCachePaths(t, repo, tag)
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("mkdir helper cache: %v", err)
	}
	if err := os.WriteFile(binaryPath, []byte("corrupt-helper"), 0o644); err != nil {
		t.Fatalf("write corrupt helper: %v", err)
	}
	if err := os.WriteFile(checksumPath, []byte(strings.Repeat("0", 64)+"  "+filepath.Base(binaryPath)+"\n"), 0o644); err != nil {
		t.Fatalf("write corrupt checksum: %v", err)
	}

	recordPath := filepath.Join(t.TempDir(), "patchelf.args")
	asset, err := patchelfAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("patchelfAssetName error: %v", err)
	}
	server, assetRequests, checksumRequests := serveCompatReleaseAsset(t, repo, tag, asset, patchelfHelperScript(recordPath))
	setCompatReleaseBaseURL(t, server.URL)

	path, err := resolvePatchelfBinary(io.Discard)
	if err != nil {
		t.Fatalf("resolvePatchelfBinary error: %v", err)
	}
	if path != binaryPath {
		t.Fatalf("expected cached helper path %q, got %q", binaryPath, path)
	}
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read redownloaded helper: %v", err)
	}
	if string(data) == "corrupt-helper" {
		t.Fatalf("expected corrupt helper cache to be replaced")
	}
	if *assetRequests != 1 || *checksumRequests != 1 {
		t.Fatalf("expected corrupt cache to trigger one redownload, got asset=%d checksum=%d", *assetRequests, *checksumRequests)
	}
}

func TestGlibcCompatLayoutFingerprintChangesWhenRuntimeChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	layout := writeGlibcCompatRuntimeFixture(t, root, "loader-a", "libc-a")

	first, err := glibcCompatLayoutFingerprint(layout)
	if err != nil {
		t.Fatalf("glibcCompatLayoutFingerprint error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.LibDir, "libc.so.6"), []byte("libc-b"), 0o644); err != nil {
		t.Fatalf("rewrite libc: %v", err)
	}
	second, err := glibcCompatLayoutFingerprint(layout)
	if err != nil {
		t.Fatalf("glibcCompatLayoutFingerprint second error: %v", err)
	}
	if first == second {
		t.Fatalf("expected fingerprint to change when runtime contents change")
	}
}

func TestInstallPreparedGlibcCompatRuntimeReplacesInvalidCache(t *testing.T) {
	cacheRoot := t.TempDir()
	runtimeRoot := filepath.Join(cacheRoot, "runtime")
	if err := os.MkdirAll(filepath.Join(runtimeRoot, "broken"), 0o755); err != nil {
		t.Fatalf("mkdir broken runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "broken", "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale runtime marker: %v", err)
	}

	stageDir := filepath.Join(cacheRoot, "stage")
	writeGlibcCompatRuntimeFixture(t, stageDir, "loader", "libc")

	if err := installPreparedGlibcCompatRuntime(stageDir, runtimeRoot); err != nil {
		t.Fatalf("installPreparedGlibcCompatRuntime error: %v", err)
	}
	layout, err := resolveGlibcCompatLayout(runtimeRoot)
	if err != nil {
		t.Fatalf("resolve installed runtime: %v", err)
	}
	if layout.RootDir == "" {
		t.Fatalf("expected installed runtime root")
	}
	if _, err := os.Stat(filepath.Join(runtimeRoot, "broken", "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected invalid runtime contents to be replaced, got err=%v", err)
	}
	if _, err := os.Stat(stageDir); !os.IsNotExist(err) {
		t.Fatalf("expected stage dir to be moved into place, got err=%v", err)
	}
}

func TestPrepareGlibcCompatMirrorUsesHostScopedCopyAndPrunes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "host-a")

	layoutRoot := filepath.Join(t.TempDir(), "runtime")
	glibcRoot := filepath.Join(layoutRoot, "glibc-2.31")
	libDir := filepath.Join(glibcRoot, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir glibc lib: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "ld-linux-x86-64.so.2"), []byte("loader"), 0o755); err != nil {
		t.Fatalf("write loader: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "libc.so.6"), []byte("libc"), 0o644); err != nil {
		t.Fatalf("write libc: %v", err)
	}
	layout, err := resolveGlibcCompatLayout(layoutRoot)
	if err != nil {
		t.Fatalf("resolveGlibcCompatLayout error: %v", err)
	}

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "claude")
	sourceData := []byte("claude-binary")
	if err := os.WriteFile(sourcePath, sourceData, 0o700); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stubDir := t.TempDir()
	recordPath := filepath.Join(stubDir, "patchelf.args")
	unix := "#!/bin/sh\nif [ \"$1\" = \"--print-interpreter\" ]; then\n  echo /lib64/ld-linux-x86-64.so.2\n  exit 0\nfi\nif [ \"$1\" = \"--print-rpath\" ]; then\n  echo /usr/lib64\n  exit 0\nfi\nprintf '%s\n' \"$@\" > \"" + recordPath + "\"\nexit 0\n"
	win := "@echo off\r\nif \"%~1\"==\"--print-interpreter\" (\r\n  echo /lib64/ld-linux-x86-64.so.2\r\n  exit /b 0\r\n)\r\nif \"%~1\"==\"--print-rpath\" (\r\n  echo /usr/lib64\r\n  exit /b 0\r\n)\r\nbreak> \"" + recordPath + "\"\r\n:loop\r\nif \"%~1\"==\"\" exit /b 0\r\necho %~1>> \"" + recordPath + "\"\r\nshift\r\ngoto loop\r\n"
	writeStub(t, stubDir, patchelfBinaryName, unix, win)
	setStubPath(t, stubDir)

	hostRoot, _, err := resolveClaudeProxyHostRoot()
	if err != nil {
		t.Fatalf("resolveClaudeProxyHostRoot error: %v", err)
	}
	claudeRoot := filepath.Join(hostRoot, "claude")
	oldA := filepath.Join(claudeRoot, "old-a")
	oldB := filepath.Join(claudeRoot, "old-b")
	if err := os.MkdirAll(oldA, 0o755); err != nil {
		t.Fatalf("mkdir old-a: %v", err)
	}
	if err := os.MkdirAll(oldB, 0o755); err != nil {
		t.Fatalf("mkdir old-b: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	newerTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(oldA, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old-a: %v", err)
	}
	if err := os.Chtimes(oldB, newerTime, newerTime); err != nil {
		t.Fatalf("chtimes old-b: %v", err)
	}

	outcome, applied, err := prepareGlibcCompatMirror(sourcePath, layout, io.Discard, &patchOutcome{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("prepareGlibcCompatMirror error: %v", err)
	}
	if !applied {
		t.Fatalf("expected compat mirror to be reported as applied")
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	if outcome.Applied {
		t.Fatalf("expected compat mirror creation not to mark outcome as byte-patched")
	}
	assertGlibcCompatMirrorLaunchPrefix(t, outcome.LaunchArgsPrefix, outcome.TargetPath, layout.LibDir)
	if !strings.HasPrefix(outcome.TargetPath, claudeRoot) {
		t.Fatalf("expected mirror under host root %q, got %q", claudeRoot, outcome.TargetPath)
	}
	gotSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(gotSource) != string(sourceData) {
		t.Fatalf("expected source binary to remain unchanged")
	}
	if _, err := os.Stat(oldA); !os.IsNotExist(err) {
		t.Fatalf("expected oldest mirror to be pruned, got err=%v", err)
	}
	if _, err := os.Stat(oldB); err != nil {
		t.Fatalf("expected newer mirror to remain: %v", err)
	}
	recordedArgs, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read patchelf record: %v", err)
	}
	if !strings.Contains(string(recordedArgs), "--set-interpreter") {
		t.Fatalf("expected patchelf invocation, got %q", string(recordedArgs))
	}
}

func TestPrepareGlibcCompatMirrorUsesDistinctCacheKeysPerRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "host-a")

	layoutA := writeGlibcCompatRuntimeFixture(t, filepath.Join(t.TempDir(), "runtime-a"), "loader-a", "libc-a")
	layoutB := writeGlibcCompatRuntimeFixture(t, filepath.Join(t.TempDir(), "runtime-b"), "loader-b", "libc-b")

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "claude")
	if err := os.WriteFile(sourcePath, []byte("claude-binary"), 0o700); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stubDir := t.TempDir()
	recordPath := filepath.Join(stubDir, "patchelf.args")
	unix := "#!/bin/sh\nif [ \"$1\" = \"--print-interpreter\" ]; then\n  echo /lib64/ld-linux-x86-64.so.2\n  exit 0\nfi\nif [ \"$1\" = \"--print-rpath\" ]; then\n  echo /usr/lib64\n  exit 0\nfi\nprintf '%s\n' \"$@\" > \"" + recordPath + "\"\nexit 0\n"
	win := "@echo off\r\nif \"%~1\"==\"--print-interpreter\" (\r\n  echo /lib64/ld-linux-x86-64.so.2\r\n  exit /b 0\r\n)\r\nif \"%~1\"==\"--print-rpath\" (\r\n  echo /usr/lib64\r\n  exit /b 0\r\n)\r\nbreak> \"" + recordPath + "\"\r\n:loop\r\nif \"%~1\"==\"\" exit /b 0\r\necho %~1>> \"" + recordPath + "\"\r\nshift\r\ngoto loop\r\n"
	writeStub(t, stubDir, patchelfBinaryName, unix, win)
	setStubPath(t, stubDir)

	outcomeA, appliedA, err := prepareGlibcCompatMirror(sourcePath, layoutA, io.Discard, &patchOutcome{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("prepareGlibcCompatMirror layoutA error: %v", err)
	}
	outcomeB, appliedB, err := prepareGlibcCompatMirror(sourcePath, layoutB, io.Discard, &patchOutcome{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("prepareGlibcCompatMirror layoutB error: %v", err)
	}
	if !appliedA || !appliedB {
		t.Fatalf("expected both runtime-specific mirrors to be created, got appliedA=%v appliedB=%v", appliedA, appliedB)
	}
	if outcomeA == nil || outcomeB == nil {
		t.Fatalf("expected non-nil outcomes, got %#v %#v", outcomeA, outcomeB)
	}
	if outcomeA.TargetPath == outcomeB.TargetPath {
		t.Fatalf("expected distinct mirror paths for different runtimes")
	}
	if _, err := os.Stat(outcomeA.TargetPath); err != nil {
		t.Fatalf("expected first mirror to exist: %v", err)
	}
	if _, err := os.Stat(outcomeB.TargetPath); err != nil {
		t.Fatalf("expected second mirror to exist: %v", err)
	}
}

func TestPrepareGlibcCompatMirrorReusesExistingMirrorWithoutRepatching(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip shell script test on windows")
	}
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "host-a")

	layout := writeGlibcCompatRuntimeFixture(t, filepath.Join(t.TempDir(), "runtime"), "loader", "libc")

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "claude")
	if err := os.WriteFile(sourcePath, []byte("claude-binary"), 0o700); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stubDir := t.TempDir()
	recordPath := filepath.Join(stubDir, "patchelf.args")
	writeStub(t, stubDir, patchelfBinaryName, string(patchelfHelperScript(recordPath)), "@echo off\r\nexit /b 1\r\n")
	setStubPath(t, stubDir)

	firstOutcome, applied, err := prepareGlibcCompatMirror(sourcePath, layout, io.Discard, &patchOutcome{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("first prepareGlibcCompatMirror error: %v", err)
	}
	if !applied || firstOutcome == nil {
		t.Fatalf("expected first mirror prepare to apply, got applied=%v outcome=%#v", applied, firstOutcome)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatalf("remove first patchelf record: %v", err)
	}

	firstInfo, err := os.Stat(firstOutcome.TargetPath)
	if err != nil {
		t.Fatalf("stat first mirror: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	t.Setenv("PATH", t.TempDir())
	t.Setenv(patchelfPathEnv, filepath.Join(t.TempDir(), "missing-patchelf"))

	secondOutcome, applied, err := prepareGlibcCompatMirror(sourcePath, layout, io.Discard, &patchOutcome{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("second prepareGlibcCompatMirror error: %v", err)
	}
	if !applied || secondOutcome == nil {
		t.Fatalf("expected second mirror prepare to apply, got applied=%v outcome=%#v", applied, secondOutcome)
	}
	if secondOutcome.TargetPath != firstOutcome.TargetPath {
		t.Fatalf("expected mirror reuse at %q, got %q", firstOutcome.TargetPath, secondOutcome.TargetPath)
	}
	if _, err := os.Stat(recordPath); !os.IsNotExist(err) {
		t.Fatalf("expected second mirror prepare not to rerun patchelf, got err=%v", err)
	}
	secondInfo, err := os.Stat(secondOutcome.TargetPath)
	if err != nil {
		t.Fatalf("stat reused mirror: %v", err)
	}
	if !secondInfo.ModTime().After(firstInfo.ModTime()) && !secondInfo.ModTime().Equal(firstInfo.ModTime()) {
		t.Fatalf("expected reused mirror mtime to stay valid, before=%v after=%v", firstInfo.ModTime(), secondInfo.ModTime())
	}
}

func TestPrepareGlibcCompatMirrorDownloadsPatchelfHelperWhenPATHMissing(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("automatic patchelf helper download only applies on linux/amd64")
	}
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "host-a")

	emptyPath := filepath.Join(t.TempDir(), "empty-bin")
	if err := os.MkdirAll(emptyPath, 0o755); err != nil {
		t.Fatalf("mkdir empty bin: %v", err)
	}
	t.Setenv("PATH", emptyPath)

	repo := "owner/repo"
	tag := "patchelf-test"
	t.Setenv(patchelfRepoEnv, repo)
	t.Setenv(patchelfTagEnv, tag)

	recordPath := filepath.Join(t.TempDir(), "patchelf.args")
	asset, err := patchelfAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("patchelfAssetName error: %v", err)
	}
	server, _, _ := serveCompatReleaseAsset(t, repo, tag, asset, patchelfHelperScript(recordPath))
	setCompatReleaseBaseURL(t, server.URL)

	layout := writeGlibcCompatRuntimeFixture(t, filepath.Join(t.TempDir(), "runtime"), "loader", "libc")

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "claude")
	if err := os.WriteFile(sourcePath, []byte("claude-binary"), 0o700); err != nil {
		t.Fatalf("write source: %v", err)
	}

	var log strings.Builder
	outcome, applied, err := prepareGlibcCompatMirror(sourcePath, layout, &log, &patchOutcome{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("prepareGlibcCompatMirror error: %v", err)
	}
	if !applied {
		t.Fatalf("expected glibc compat mirror to report applied")
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	assertGlibcCompatMirrorLaunchPrefix(t, outcome.LaunchArgsPrefix, outcome.TargetPath, layout.LibDir)
	recordedArgs, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read patchelf record: %v", err)
	}
	if !strings.Contains(string(recordedArgs), "--set-interpreter") {
		t.Fatalf("expected downloaded patchelf helper to patch the mirror, got %q", string(recordedArgs))
	}
	if !strings.Contains(log.String(), "downloaded patchelf helper") {
		t.Fatalf("expected log to mention downloaded patchelf helper, got %q", log.String())
	}
}

func TestApplyClaudeGlibcCompatPatchRescuesNonEL7Hosts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skip linux-only glibc compat flow outside linux")
	}
	glibcCompatHostEligibleFn = func() bool { return false }
	t.Cleanup(func() { glibcCompatHostEligibleFn = isEL7GlibcCompatHost })

	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "host-a")

	dir := t.TempDir()
	root := filepath.Join(dir, "runtime")
	layout := writeGlibcCompatRuntimeFixture(t, root, "loader", "libc")

	sourcePath := filepath.Join(dir, "claude")
	if err := os.WriteFile(sourcePath, []byte("claude"), 0o700); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stubDir := t.TempDir()
	recordPath := filepath.Join(stubDir, "patchelf.args")
	unix := "#!/bin/sh\nif [ \"$1\" = \"--print-interpreter\" ]; then\n  echo /lib64/ld-linux-x86-64.so.2\n  exit 0\nfi\nif [ \"$1\" = \"--print-rpath\" ]; then\n  echo /usr/lib64\n  exit 0\nfi\nprintf '%s\n' \"$@\" > \"" + recordPath + "\"\nexit 0\n"
	win := "@echo off\r\nif \"%~1\"==\"--print-interpreter\" (\r\n  echo /lib64/ld-linux-x86-64.so.2\r\n  exit /b 0\r\n)\r\nif \"%~1\"==\"--print-rpath\" (\r\n  echo /usr/lib64\r\n  exit /b 0\r\n)\r\nbreak> \"" + recordPath + "\"\r\n:loop\r\nif \"%~1\"==\"\" exit /b 0\r\necho %~1>> \"" + recordPath + "\"\r\nshift\r\ngoto loop\r\n"
	writeStub(t, stubDir, patchelfBinaryName, unix, win)
	setStubPath(t, stubDir)

	outcome, applied, err := applyClaudeGlibcCompatPatch(sourcePath, exePatchOptions{
		enabledFlag:     true,
		glibcCompat:     true,
		glibcCompatRoot: layout.RootDir,
	}, io.Discard, false, &patchOutcome{SourcePath: sourcePath, TargetPath: sourcePath})
	if err != nil {
		t.Fatalf("applyClaudeGlibcCompatPatch error: %v", err)
	}
	if !applied {
		t.Fatalf("expected compat rescue to apply on non-EL7 host after probe failure")
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	if outcome.TargetPath == sourcePath {
		t.Fatalf("expected host-local mirror, got source path %q", outcome.TargetPath)
	}
	assertGlibcCompatMirrorLaunchPrefix(t, outcome.LaunchArgsPrefix, outcome.TargetPath, layout.LibDir)
	if _, err := os.Stat(outcome.TargetPath); err != nil {
		t.Fatalf("expected mirror to exist: %v", err)
	}
}

func TestApplyClaudeGlibcCompatPatchFallsBackToWrapperWithoutPatchelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skip linux-only glibc compat flow outside linux")
	}
	glibcCompatHostEligibleFn = func() bool { return true }
	t.Cleanup(func() { glibcCompatHostEligibleFn = isEL7GlibcCompatHost })
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "host-a")

	dir := t.TempDir()
	root := filepath.Join(dir, "runtime")
	glibcRoot := filepath.Join(root, "glibc-2.31")
	libDir := filepath.Join(glibcRoot, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "ld-linux-x86-64.so.2"), []byte("loader"), 0o755); err != nil {
		t.Fatalf("write loader: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "libc.so.6"), []byte("libc"), 0o644); err != nil {
		t.Fatalf("write libc: %v", err)
	}
	wrapperPath := filepath.Join(root, "run-with-glibc-2.31.sh")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/sh\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	sourcePath := filepath.Join(dir, "claude")
	if err := os.WriteFile(sourcePath, []byte("claude"), 0o700); err != nil {
		t.Fatalf("write source: %v", err)
	}

	emptyPath := filepath.Join(dir, "empty-bin")
	if err := os.MkdirAll(emptyPath, 0o755); err != nil {
		t.Fatalf("mkdir empty bin: %v", err)
	}
	t.Setenv("PATH", emptyPath)
	t.Setenv(patchelfPathEnv, filepath.Join(dir, "missing-patchelf"))

	outcome, applied, err := applyClaudeGlibcCompatPatch(sourcePath, exePatchOptions{
		enabledFlag:     true,
		glibcCompat:     true,
		glibcCompatRoot: root,
	}, io.Discard, false, &patchOutcome{SourcePath: sourcePath, TargetPath: sourcePath})
	if err != nil {
		t.Fatalf("applyClaudeGlibcCompatPatch error: %v", err)
	}
	if !applied {
		t.Fatalf("expected wrapper fallback to report applied")
	}
	if outcome == nil {
		t.Fatalf("expected non-nil outcome")
	}
	if outcome.TargetPath == sourcePath {
		t.Fatalf("expected wrapper fallback to use a host-local mirror, got source path %q", outcome.TargetPath)
	}
	if len(outcome.LaunchArgsPrefix) != 2 || outcome.LaunchArgsPrefix[1] != outcome.TargetPath {
		t.Fatalf("unexpected launch prefix: %#v", outcome.LaunchArgsPrefix)
	}
	if outcome.LaunchArgsPrefix[0] == wrapperPath {
		t.Fatalf("expected host-local wrapper path, got runtime wrapper %q", outcome.LaunchArgsPrefix[0])
	}
	wrapperData, err := os.ReadFile(outcome.LaunchArgsPrefix[0])
	if err != nil {
		t.Fatalf("read generated wrapper: %v", err)
	}
	if !strings.Contains(string(wrapperData), `exec -a "$1"`) {
		t.Fatalf("expected generated wrapper to preserve argv0, got %q", string(wrapperData))
	}
	wrapperInfo, err := os.Stat(outcome.LaunchArgsPrefix[0])
	if err != nil {
		t.Fatalf("stat generated wrapper: %v", err)
	}
	if wrapperInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected generated wrapper to be executable, got mode %v", wrapperInfo.Mode())
	}
	if _, err := os.Stat(outcome.TargetPath); err != nil {
		t.Fatalf("expected wrapper mirror to exist: %v", err)
	}
	gotSource, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(gotSource) != "claude" {
		t.Fatalf("expected source to remain unchanged")
	}
}

func TestResolveGlibcCompatWrapperPathFindsAdjacentWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wrapper shell script lookup is only meaningful outside windows")
	}
	layout := writeGlibcCompatRuntimeFixture(t, filepath.Join(t.TempDir(), "runtime"), "loader", "libc")
	wrapperPath := filepath.Join(filepath.Dir(layout.RootDir), "run-with-glibc-2.31.sh")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	got, err := resolveGlibcCompatWrapperPath(layout)
	if err != nil {
		t.Fatalf("resolveGlibcCompatWrapperPath error: %v", err)
	}
	if !sameFilePath(got, wrapperPath) {
		t.Fatalf("expected wrapper path %q, got %q", wrapperPath, got)
	}
}

func TestEnsureGlibcCompatWrapperPathRepairsNonExecutableWrapper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("wrapper execute-bit repair is only meaningful on linux")
	}
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv(claudeProxyHostIDEnv, "host-a")

	layout := writeGlibcCompatRuntimeFixture(t, filepath.Join(t.TempDir(), "runtime"), "loader", "libc")
	wrapperPath, err := ensureGlibcCompatWrapperPath(layout)
	if err != nil {
		t.Fatalf("ensureGlibcCompatWrapperPath error: %v", err)
	}
	if err := os.Chmod(wrapperPath, 0o600); err != nil {
		t.Fatalf("chmod wrapper non-executable: %v", err)
	}

	wrapperPath, err = ensureGlibcCompatWrapperPath(layout)
	if err != nil {
		t.Fatalf("ensureGlibcCompatWrapperPath repair error: %v", err)
	}
	info, err := os.Stat(wrapperPath)
	if err != nil {
		t.Fatalf("stat repaired wrapper: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected repaired wrapper to be executable, got mode %v", info.Mode())
	}
	wrapperData, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("read repaired wrapper: %v", err)
	}
	if !strings.Contains(string(wrapperData), `exec -a "$1"`) {
		t.Fatalf("expected repaired wrapper to preserve argv0, got %q", string(wrapperData))
	}
}
