package cli

import "testing"

func requireExePatchEnabled(t *testing.T) {
	t.Helper()
	if !exePatchEnabledDefault() {
		t.Skip("exe patch disabled; set CLAUDE_PROXY_EXE_PATCH=1 to enable")
	}
}
