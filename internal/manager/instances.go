package manager

import (
	"fmt"
	"time"

	"gitlab-master.nvidia.com/jawei/claude_code_helper/internal/config"
)

func RecordInstance(store *config.Store, inst config.Instance) error {
	return store.Update(func(cfg *config.Config) error {
		cfg.UpsertInstance(inst)
		return nil
	})
}

func RemoveInstance(store *config.Store, instanceID string) error {
	return store.Update(func(cfg *config.Config) error {
		cfg.RemoveInstance(instanceID)
		return nil
	})
}

func Heartbeat(store *config.Store, instanceID string, now time.Time) error {
	return store.Update(func(cfg *config.Config) error {
		for i := range cfg.Instances {
			if cfg.Instances[i].ID == instanceID {
				cfg.Instances[i].LastSeenAt = now
				return nil
			}
		}
		return fmt.Errorf("instance %q not found", instanceID)
	})
}
