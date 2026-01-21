package manager

import (
	"time"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/proc"
)

func FindReusableInstance(instances []config.Instance, profileID string, hc HealthClient) *config.Instance {
	var best *config.Instance
	for i := range instances {
		inst := &instances[i]
		if inst.ProfileID != profileID {
			continue
		}
		if inst.DaemonPID <= 0 || !proc.IsAlive(inst.DaemonPID) {
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

func IsInstanceStale(inst config.Instance, now time.Time, maxAge time.Duration) bool {
	if maxAge <= 0 {
		return false
	}
	if inst.LastSeenAt.IsZero() {
		return false
	}
	return now.Sub(inst.LastSeenAt) > maxAge
}
