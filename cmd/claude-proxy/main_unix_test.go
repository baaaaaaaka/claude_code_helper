//go:build !windows

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestMainTuiWithoutTtyRejectsCleanly is an end-to-end guard on the TTY
// precheck. The subprocess is started with SysProcAttr.Setsid=true, which
// detaches it from the controlling terminal; `open("/dev/tty", O_RDWR)`
// then fails with ENXIO, exactly reproducing the crash the precheck was
// added for. The precheck must intercept this before tcell does, and the
// user-facing error must point at `clp history list`.
//
// This locks the error message shape; before the fix the same setup would
// have crashed deep inside tcell with a cryptic "open /dev/tty: no such
// device or address".
func TestMainTuiWithoutTtyRejectsCleanly(t *testing.T) {
	const envVar = "CLP_HELPER_TUI_NO_TTY_CFG"
	if cfg := os.Getenv(envVar); cfg != "" {
		os.Args = []string{"claude-proxy", "--config", cfg, "tui"}
		main()
		return
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"version":1,"profiles":[],"instances":[]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stderr bytes.Buffer
	cmd := exec.Command(os.Args[0], "-test.run=TestMainTuiWithoutTtyRejectsCleanly")
	cmd.Env = append(os.Environ(), envVar+"="+cfgPath)
	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err := cmd.Run()

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v. stderr:\n%s", err, err, stderr.String())
	}
	if exitErr.ExitCode() == 0 {
		t.Fatalf("expected non-zero exit for no-tty launch, got 0. stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "requires a terminal") {
		t.Fatalf("expected stderr to mention \"requires a terminal\", got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "clp history list") {
		t.Fatalf("expected stderr to point at `clp history list` as the scripted alternative, got:\n%s", stderr.String())
	}
}
