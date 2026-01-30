package cli

import "testing"

func TestUpgradeCmdInvalidVersion(t *testing.T) {
	cmd := newUpgradeCmd(&rootOptions{})
	cmd.SetArgs([]string{"--version", "v"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected invalid version error")
	}
}
