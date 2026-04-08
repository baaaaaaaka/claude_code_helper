package config

import "strings"

func (c Config) FindProfile(ref string) (Profile, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Profile{}, false
	}
	for _, p := range c.Profiles {
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			return p, true
		}
	}
	return Profile{}, false
}

func (c *Config) UpsertProfile(p Profile) {
	for i := range c.Profiles {
		if c.Profiles[i].ID == p.ID {
			c.Profiles[i] = p
			return
		}
	}
	c.Profiles = append(c.Profiles, p)
}

func (c Config) InstancesForProfile(profileID string) []Instance {
	var out []Instance
	for _, inst := range c.Instances {
		if inst.ProfileID == profileID {
			out = append(out, inst)
		}
	}
	return out
}

func (c *Config) UpsertInstance(inst Instance) {
	for i := range c.Instances {
		if c.Instances[i].ID == inst.ID {
			c.Instances[i] = inst
			return
		}
	}
	c.Instances = append(c.Instances, inst)
}

func (c *Config) RemoveInstance(id string) bool {
	for i := range c.Instances {
		if c.Instances[i].ID != id {
			continue
		}
		c.Instances = append(c.Instances[:i], c.Instances[i+1:]...)
		return true
	}
	return false
}

func (c Config) HasPatchFailure(hostID, proxyVersion, claudeVersion, claudeSHA string) bool {
	hostID = strings.TrimSpace(hostID)
	proxyVersion = strings.TrimSpace(proxyVersion)
	claudeVersion = strings.TrimSpace(claudeVersion)
	claudeSHA = strings.TrimSpace(claudeSHA)
	if proxyVersion == "" {
		return false
	}
	for _, entry := range c.PatchFailures {
		entryHostID := strings.TrimSpace(entry.HostID)
		if entryHostID != "" && hostID != entryHostID {
			continue
		}
		if strings.TrimSpace(entry.ProxyVersion) != proxyVersion {
			continue
		}
		if claudeVersion != "" && strings.TrimSpace(entry.ClaudeVersion) == claudeVersion {
			return true
		}
		if claudeVersion == "" && claudeSHA != "" && strings.EqualFold(entry.ClaudeSHA256, claudeSHA) {
			return true
		}
	}
	return false
}

func (c *Config) UpsertPatchFailure(entry PatchFailure) {
	for i := range c.PatchFailures {
		if samePatchFailureKey(c.PatchFailures[i], entry) {
			c.PatchFailures[i] = entry
			return
		}
	}
	c.PatchFailures = append(c.PatchFailures, entry)
}

// PurgeStalePatchFailures removes failure entries whose ProxyVersion
// does not match currentVersion.  This ensures that upgrading the proxy
// automatically gives patches a fresh chance to succeed.
func (c *Config) PurgeStalePatchFailures(currentVersion string) bool {
	currentVersion = strings.TrimSpace(currentVersion)
	if currentVersion == "" {
		return false
	}
	n := 0
	for _, entry := range c.PatchFailures {
		if strings.TrimSpace(entry.ProxyVersion) == currentVersion {
			c.PatchFailures[n] = entry
			n++
		}
	}
	if n == len(c.PatchFailures) {
		return false
	}
	// Clear trailing references for GC.
	for i := n; i < len(c.PatchFailures); i++ {
		c.PatchFailures[i] = PatchFailure{}
	}
	c.PatchFailures = c.PatchFailures[:n]
	return true
}

func (c Config) FindYoloBypassProbe(proxyVersion, claudeVersion, claudePath string) ([]string, bool) {
	proxyVersion = strings.TrimSpace(proxyVersion)
	claudeVersion = strings.TrimSpace(claudeVersion)
	claudePath = strings.TrimSpace(claudePath)
	if proxyVersion == "" {
		return nil, false
	}
	if claudeVersion != "" {
		for _, entry := range c.YoloBypassProbes {
			if strings.TrimSpace(entry.ProxyVersion) != proxyVersion {
				continue
			}
			if strings.TrimSpace(entry.ClaudeVersion) != claudeVersion {
				continue
			}
			return append([]string(nil), entry.Args...), true
		}
		return nil, false
	}
	if claudePath == "" {
		return nil, false
	}
	for _, entry := range c.YoloBypassProbes {
		if strings.TrimSpace(entry.ProxyVersion) != proxyVersion {
			continue
		}
		if strings.TrimSpace(entry.ClaudeVersion) != "" {
			continue
		}
		if strings.TrimSpace(entry.ClaudePath) != claudePath {
			continue
		}
		return append([]string(nil), entry.Args...), true
	}
	return nil, false
}

func (c *Config) UpsertYoloBypassProbe(entry YoloBypassProbe) {
	for i := range c.YoloBypassProbes {
		if sameYoloBypassProbeKey(c.YoloBypassProbes[i], entry) {
			c.YoloBypassProbes[i] = cloneYoloBypassProbe(entry)
			return
		}
	}
	c.YoloBypassProbes = append(c.YoloBypassProbes, cloneYoloBypassProbe(entry))
}

func (c *Config) PurgeStaleYoloBypassProbes(currentVersion string) bool {
	currentVersion = strings.TrimSpace(currentVersion)
	if currentVersion == "" {
		return false
	}
	n := 0
	for _, entry := range c.YoloBypassProbes {
		if strings.TrimSpace(entry.ProxyVersion) == currentVersion {
			c.YoloBypassProbes[n] = entry
			n++
		}
	}
	if n == len(c.YoloBypassProbes) {
		return false
	}
	for i := n; i < len(c.YoloBypassProbes); i++ {
		c.YoloBypassProbes[i] = YoloBypassProbe{}
	}
	c.YoloBypassProbes = c.YoloBypassProbes[:n]
	return true
}

func samePatchFailureKey(a, b PatchFailure) bool {
	aHostID := strings.TrimSpace(a.HostID)
	bHostID := strings.TrimSpace(b.HostID)
	if aHostID != "" && bHostID != "" && aHostID != bHostID {
		return false
	}
	if strings.TrimSpace(a.ProxyVersion) != strings.TrimSpace(b.ProxyVersion) {
		return false
	}
	if strings.TrimSpace(a.ClaudeVersion) != "" || strings.TrimSpace(b.ClaudeVersion) != "" {
		return strings.TrimSpace(a.ClaudeVersion) == strings.TrimSpace(b.ClaudeVersion)
	}
	if strings.TrimSpace(a.ClaudeSHA256) != "" || strings.TrimSpace(b.ClaudeSHA256) != "" {
		return strings.EqualFold(strings.TrimSpace(a.ClaudeSHA256), strings.TrimSpace(b.ClaudeSHA256))
	}
	if strings.TrimSpace(a.ClaudePath) != "" && strings.TrimSpace(b.ClaudePath) != "" {
		return strings.TrimSpace(a.ClaudePath) == strings.TrimSpace(b.ClaudePath)
	}
	return false
}

func sameYoloBypassProbeKey(a, b YoloBypassProbe) bool {
	if strings.TrimSpace(a.ProxyVersion) != strings.TrimSpace(b.ProxyVersion) {
		return false
	}
	if strings.TrimSpace(a.ClaudeVersion) != "" || strings.TrimSpace(b.ClaudeVersion) != "" {
		return strings.TrimSpace(a.ClaudeVersion) == strings.TrimSpace(b.ClaudeVersion)
	}
	aPath := strings.TrimSpace(a.ClaudePath)
	bPath := strings.TrimSpace(b.ClaudePath)
	if aPath != "" && bPath != "" {
		return aPath == bPath
	}
	return false
}

func cloneYoloBypassProbe(entry YoloBypassProbe) YoloBypassProbe {
	entry.Args = append([]string(nil), entry.Args...)
	return entry
}
