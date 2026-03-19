package cli

import "github.com/baaaaaaaka/claude_code_helper/internal/config"

func resolveYoloEnabled(cfg config.Config) bool {
	return cfg.YoloEnabled != nil && *cfg.YoloEnabled
}

// A nil pointer means we have no persisted evidence that the user ever
// enabled YOLO in the TUI, so the toggle should stay hidden until they
// explicitly opt in with Ctrl+Y.
func resolveYoloVisible(cfg config.Config) bool {
	return cfg.YoloEnabled != nil
}

func persistYoloEnabled(store *config.Store, enabled bool) error {
	return store.Update(func(cfg *config.Config) error {
		cfg.YoloEnabled = &enabled
		return nil
	})
}
