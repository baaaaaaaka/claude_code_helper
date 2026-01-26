package cli

import "testing"

func TestInstallerCandidatesLinux(t *testing.T) {
	cmds := installerCandidates("linux")
	if len(cmds) != 1 {
		t.Fatalf("expected 1 linux installer, got %d", len(cmds))
	}
	if cmds[0].path != "bash" {
		t.Fatalf("expected bash installer, got %s", cmds[0].path)
	}
}

func TestInstallerCandidatesWindows(t *testing.T) {
	cmds := installerCandidates("windows")
	if len(cmds) < 2 {
		t.Fatalf("expected at least 2 windows installers, got %d", len(cmds))
	}
}
