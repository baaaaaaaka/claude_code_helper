package cli

import (
	"os"
	"strconv"
	"strings"
)

const exePatchEnabledEnv = "CLAUDE_PROXY_EXE_PATCH"

func exePatchEnabledDefault() bool {
	raw := strings.TrimSpace(os.Getenv(exePatchEnabledEnv))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}
