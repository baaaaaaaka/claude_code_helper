package cli

import "github.com/baaaaaaaka/claude_code_helper/internal/config"

func normalizeYoloMode(mode config.YoloMode) config.YoloMode {
	switch mode {
	case config.YoloModeOff, config.YoloModeBypass, config.YoloModeRules:
		return mode
	default:
		return config.YoloModeOff
	}
}

func resolveYoloMode(cfg config.Config) config.YoloMode {
	if cfg.YoloMode != nil {
		mode := config.YoloMode(*cfg.YoloMode)
		return normalizeYoloMode(mode)
	}
	if cfg.YoloEnabled == nil {
		return config.YoloMode("")
	}
	if *cfg.YoloEnabled {
		return config.YoloModeBypass
	}
	return config.YoloModeOff
}

func resolveYoloEnabled(cfg config.Config) bool {
	return resolveYoloMode(cfg) == config.YoloModeBypass
}

// A nil persisted mode means we have no evidence that the user ever enabled a
// YOLO-related launch mode in the TUI, so the toggle should stay hidden until
// they explicitly opt in with Ctrl+Y.
func resolveYoloVisible(cfg config.Config) bool {
	return resolveYoloMode(cfg) != config.YoloMode("")
}

func nextYoloMode(mode config.YoloMode) config.YoloMode {
	switch normalizeYoloMode(mode) {
	case config.YoloModeOff:
		return config.YoloModeBypass
	case config.YoloModeBypass:
		return config.YoloModeRules
	case config.YoloModeRules:
		return config.YoloModeOff
	default:
		return config.YoloModeBypass
	}
}

func isBypassYoloMode(mode config.YoloMode) bool {
	return normalizeYoloMode(mode) == config.YoloModeBypass
}

func usesPatchedClaudeMode(mode config.YoloMode) bool {
	mode = normalizeYoloMode(mode)
	return mode == config.YoloModeBypass || mode == config.YoloModeRules
}

func withYoloModePatchOptions(opts exePatchOptions, mode config.YoloMode) exePatchOptions {
	if normalizeYoloMode(mode) == config.YoloModeRules {
		opts.allowBuiltInWithoutBypass = true
	}
	return opts
}

func persistYoloMode(store *config.Store, mode config.YoloMode) error {
	mode = normalizeYoloMode(mode)
	encodedMode := string(mode)
	legacyEnabled := mode == config.YoloModeBypass
	return store.Update(func(cfg *config.Config) error {
		cfg.YoloMode = &encodedMode
		cfg.YoloEnabled = &legacyEnabled
		return nil
	})
}

func persistYoloEnabled(store *config.Store, enabled bool) error {
	mode := config.YoloModeOff
	if enabled {
		mode = config.YoloModeBypass
	}
	return persistYoloMode(store, mode)
}
