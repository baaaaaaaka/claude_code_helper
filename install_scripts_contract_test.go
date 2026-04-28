package installtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPs1ChecksumFallbackContract(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, "install.ps1"))
	if err != nil {
		t.Fatalf("read install.ps1: %v", err)
	}
	script := string(data)

	for _, needle := range []string{
		`function Get-SHA256Hex`,
		`Get-Command Get-FileHash -ErrorAction SilentlyContinue`,
		`[System.Security.Cryptography.SHA256]::Create()`,
		`[System.BitConverter]::ToString($hashBytes) -replace "-", ""`,
		`$actual = Get-SHA256Hex -path $tmp`,
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("expected install.ps1 to contain %q", needle)
		}
	}
}

func TestWorkflowSmokeCoverageContracts(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	ci := readTextFile(t, filepath.Join(repoRoot, ".github", "workflows", "ci.yml"))
	for _, needle := range []string{
		"actionlint:",
		"uses: rhysd/actionlint@v1",
	} {
		if !strings.Contains(ci, needle) {
			t.Fatalf("expected ci.yml to contain %q", needle)
		}
	}

	release := readTextFile(t, filepath.Join(repoRoot, ".github", "workflows", "release.yml"))
	installSmoke := sectionBetween(t, release, "  install-smoke:", "  install-container-smoke:")
	for _, forbidden := range []string{
		"continue-on-error:",
		"allowFailure:",
	} {
		if strings.Contains(installSmoke, forbidden) {
			t.Fatalf("release install smoke must not allow failures, found %q", forbidden)
		}
	}
	for _, needle := range []string{
		`"$install_dir/clp" --version | grep -q "${RELEASE_TAG#v}"`,
		`clp --version | grep -q "${RELEASE_TAG#v}"`,
		`$clpCmd = Join-Path $installDir "clp.cmd"`,
		`& $clpCmd --version | Select-String ($env:RELEASE_TAG.TrimStart("v")) | Out-Null`,
		`& clp.cmd --version | Select-String ($env:RELEASE_TAG.TrimStart("v")) | Out-Null`,
	} {
		if !strings.Contains(installSmoke, needle) {
			t.Fatalf("expected release install smoke to contain %q", needle)
		}
	}

	containerSmoke := readTextFile(t, filepath.Join(repoRoot, "scripts", "ci", "container_install_smoke.sh"))
	for _, needle := range []string{
		"resolve_clp_entrypoint()",
		"command -v clp",
		`[[ -x "$install_dir/clp" ]]`,
		`"$clp_entrypoint" --version | grep -q "${tag#v}"`,
	} {
		if !strings.Contains(containerSmoke, needle) {
			t.Fatalf("expected container install smoke to contain %q", needle)
		}
	}

	glibc := readTextFile(t, filepath.Join(repoRoot, ".github", "workflows", "glibc-compat-build.yml"))
	for _, needle := range []string{
		"Smoke test local bundle on CentOS7",
		"BUNDLE: ${{ github.workspace }}/dist/glibc-compat/${{ env.GLIBC_COMPAT_BUNDLE }}",
		`export CLAUDE_VERSION="$(tr -d '\r\n' < scripts/claude_patch_version.txt)"`,
		"bash scripts/glibc/test_centos7_claude_with_glibc_compat.sh",
	} {
		if !strings.Contains(glibc, needle) {
			t.Fatalf("expected glibc compat build workflow to contain %q", needle)
		}
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func sectionBetween(t *testing.T, text string, startMarker string, endMarker string) string {
	t.Helper()

	start := strings.Index(text, startMarker)
	if start < 0 {
		t.Fatalf("missing start marker %q", startMarker)
	}
	rest := text[start:]
	end := strings.Index(rest, endMarker)
	if end < 0 {
		t.Fatalf("missing end marker %q", endMarker)
	}
	return rest[:end]
}
