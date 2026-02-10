package cli

import (
	"os"
	"strconv"
	"strings"
)

const exePatchEnabledEnv = "CLAUDE_PROXY_EXE_PATCH"
const exePatchGlibcCompatEnv = "CLAUDE_PROXY_GLIBC_COMPAT"
const exePatchGlibcCompatRootEnv = "CLAUDE_PROXY_GLIBC_COMPAT_ROOT"

func exePatchEnabledDefault() bool {
	return parseBoolEnv(exePatchEnabledEnv, true)
}

func exePatchGlibcCompatDefault() bool {
	return parseBoolEnv(exePatchGlibcCompatEnv, true)
}

func exePatchGlibcCompatRootDefault() string {
	return strings.TrimSpace(os.Getenv(exePatchGlibcCompatRootEnv))
}

func parseBoolEnv(name string, defaultValue bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}
