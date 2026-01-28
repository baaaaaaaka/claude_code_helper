package installtest

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGofmtClean(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	cmd := exec.Command("gofmt", "-l", ".")
	cmd.Dir = repoRoot
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("gofmt -l failed: %v: %s", err, strings.TrimSpace(output.String()))
	}

	if bad := strings.TrimSpace(output.String()); bad != "" {
		t.Fatalf("gofmt required for:\n%s", bad)
	}
}
