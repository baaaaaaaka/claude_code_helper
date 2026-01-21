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
