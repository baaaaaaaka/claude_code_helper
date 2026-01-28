package config

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func requireExePatchEnabled(t *testing.T) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("CLAUDE_PROXY_EXE_PATCH"))
	if raw == "" {
		return
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil || !enabled {
		t.Skip("exe patch disabled; set CLAUDE_PROXY_EXE_PATCH=1 to enable")
	}
}
