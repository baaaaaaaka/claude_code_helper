package cli

import "github.com/baaaaaaaka/claude_code_helper/internal/config"

func resolveYoloEnabled(cfg config.Config) bool {
	return cfg.YoloEnabled != nil && *cfg.YoloEnabled
}

func persistYoloEnabled(store *config.Store, enabled bool) error {
	return store.Update(func(cfg *config.Config) error {
		cfg.YoloEnabled = &enabled
		return nil
	})
}
