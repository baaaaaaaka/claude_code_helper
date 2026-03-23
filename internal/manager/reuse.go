package manager

import (
	"time"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
	"github.com/baaaaaaaka/claude_code_helper/internal/proc"
)

var (
	procCommandLine = proc.CommandLine
	procIsAlive     = proc.IsAlive
)

func FindReusableInstance(instances []config.Instance, profileID string, hc HealthClient) *config.Instance {
	var best *config.Instance
	for i := range instances {
		inst := &instances[i]
		if inst.ProfileID != profileID {
			continue
		}
		if inst.DaemonPID <= 0 || !procIsAlive(inst.DaemonPID) {
			continue
		}
		if !isReusableDaemonInstance(*inst) {
			continue
		}
		if err := hc.CheckHTTPProxy(inst.HTTPPort, inst.ID); err != nil {
			continue
		}

		if best == nil || inst.LastSeenAt.After(best.LastSeenAt) || best.LastSeenAt.IsZero() {
			copy := *inst
			best = &copy
		}
	}
	return best
}

func isReusableDaemonInstance(inst config.Instance) bool {
	if inst.Kind == config.InstanceKindDaemon {
		return true
	}
	if inst.Kind != "" {
		return false
	}
	args, err := procCommandLine(inst.DaemonPID)
	if err != nil {
		return false
	}
	return proc.LooksLikeProxyDaemon(args)
}

func IsInstanceStale(inst config.Instance, now time.Time, maxAge time.Duration) bool {
	if maxAge <= 0 {
		return false
	}
	if inst.LastSeenAt.IsZero() {
		return false
	}
	return now.Sub(inst.LastSeenAt) > maxAge
}
