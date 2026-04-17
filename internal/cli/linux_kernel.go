package cli

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

const (
	bunMinimumLinuxKernelMajor     = 5
	bunMinimumLinuxKernelMinor     = 1
	bunRecommendedLinuxKernelMajor = 5
	bunRecommendedLinuxKernelMinor = 6
)

var readLinuxKernelReleaseFn = func() ([]byte, error) { return os.ReadFile("/proc/sys/kernel/osrelease") }

func bunLinuxKernelCompatibilityProblem() (string, bool) {
	if runtime.GOOS != "linux" {
		return "", false
	}
	release := currentLinuxKernelRelease()
	major, minor, ok := linuxKernelMajorMinor(release)
	if !ok || linuxKernelVersionAtLeast(major, minor, bunMinimumLinuxKernelMajor, bunMinimumLinuxKernelMinor) {
		return "", false
	}
	if release == "" {
		release = "unknown"
	}
	return fmt.Sprintf(
		"this host runs Linux kernel %s, but Claude Code's bundled Bun runtime requires Linux kernel >= %d.%d (Bun recommends %d.%d+)",
		release,
		bunMinimumLinuxKernelMajor,
		bunMinimumLinuxKernelMinor,
		bunRecommendedLinuxKernelMajor,
		bunRecommendedLinuxKernelMinor,
	), true
}

func bunLinuxKernelStartupError(output string, err error) error {
	if err == nil || !exitDueToFatalSignal(err) || !looksLikeBunCrashOutput(output) {
		return nil
	}
	reason, unsupported := bunLinuxKernelCompatibilityProblem()
	if !unsupported {
		return nil
	}
	return fmt.Errorf("%s", reason)
}

func looksLikeBunCrashOutput(output string) bool {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "bun") {
		return false
	}
	if strings.Contains(lower, "bun has crashed") {
		return true
	}
	return strings.Contains(lower, "panic(main thread)")
}

func currentLinuxKernelRelease() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	raw, err := readLinuxKernelReleaseFn()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func linuxKernelMajorMinor(release string) (int, int, bool) {
	release = strings.TrimSpace(release)
	if release == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(release, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(leadingDigits(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(leadingDigits(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

func linuxKernelVersionAtLeast(major int, minor int, wantMajor int, wantMinor int) bool {
	if major != wantMajor {
		return major > wantMajor
	}
	return minor >= wantMinor
}

func leadingDigits(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	var b strings.Builder
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			break
		}
		b.WriteRune(ch)
	}
	return b.String()
}
