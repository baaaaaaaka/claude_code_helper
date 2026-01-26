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

func (c Config) HasPatchFailure(proxyVersion, claudeVersion, claudeSHA string) bool {
	proxyVersion = strings.TrimSpace(proxyVersion)
	claudeVersion = strings.TrimSpace(claudeVersion)
	claudeSHA = strings.TrimSpace(claudeSHA)
	if proxyVersion == "" {
		return false
	}
	for _, entry := range c.PatchFailures {
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

func samePatchFailureKey(a, b PatchFailure) bool {
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
