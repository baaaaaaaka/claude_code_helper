package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestMainVersionExitZero(t *testing.T) {
	if os.Getenv("CLAUDE_PROXY_HELPER") == "1" {
		os.Args = []string{"claude-proxy", "--version"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainVersionExitZero")
	cmd.Env = append(os.Environ(), "CLAUDE_PROXY_HELPER=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected exit 0, got error: %v", err)
	}
}
