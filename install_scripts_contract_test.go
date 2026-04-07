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
