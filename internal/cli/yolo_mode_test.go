package cli

import (
	"testing"

	"github.com/baaaaaaaka/claude_code_helper/internal/config"
)

func TestResolveYoloModeDefaultsHidden(t *testing.T) {
	store := newTempStore(t)
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := resolveYoloMode(cfg); got != config.YoloMode("") {
		t.Fatalf("expected hidden yolo mode, got %q", got)
	}
	if resolveYoloEnabled(cfg) {
		t.Fatalf("expected bypass mode disabled by default")
	}
	if resolveYoloVisible(cfg) {
		t.Fatalf("expected yolo to stay hidden by default")
	}
}

func TestResolveYoloModeFallsBackToLegacyBool(t *testing.T) {
	enabled := true
	cfg := config.Config{Version: config.CurrentVersion, YoloEnabled: &enabled}
	if got := resolveYoloMode(cfg); got != config.YoloModeBypass {
		t.Fatalf("expected legacy bool to map to bypass mode, got %q", got)
	}

	enabled = false
	cfg = config.Config{Version: config.CurrentVersion, YoloEnabled: &enabled}
	if got := resolveYoloMode(cfg); got != config.YoloModeOff {
		t.Fatalf("expected legacy bool=false to map to off mode, got %q", got)
	}
	if !resolveYoloVisible(cfg) {
		t.Fatalf("expected legacy bool=false to keep yolo visible")
	}
}

func TestPersistYoloModeStoresBypassCompatibilityState(t *testing.T) {
	store := newTempStore(t)
	if err := persistYoloMode(store, config.YoloModeBypass); err != nil {
		t.Fatalf("persist yolo mode: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := resolveYoloMode(cfg); got != config.YoloModeBypass {
		t.Fatalf("expected bypass mode from config, got %q", got)
	}
	if !resolveYoloEnabled(cfg) {
		t.Fatalf("expected bypass mode enabled from config")
	}
	if !resolveYoloVisible(cfg) {
		t.Fatalf("expected yolo to be visible after enable")
	}
	if cfg.YoloEnabled == nil || !*cfg.YoloEnabled {
		t.Fatalf("expected legacy yoloEnabled compatibility flag to stay true")
	}
	if cfg.YoloMode == nil || *cfg.YoloMode != string(config.YoloModeBypass) {
		t.Fatalf("expected yoloMode to persist bypass, got %#v", cfg.YoloMode)
	}
}

func TestPersistYoloModeStoresRulesAsVisibleButLegacyOff(t *testing.T) {
	store := newTempStore(t)
	if err := persistYoloMode(store, config.YoloModeRules); err != nil {
		t.Fatalf("persist yolo rules mode: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := resolveYoloMode(cfg); got != config.YoloModeRules {
		t.Fatalf("expected rules mode from config, got %q", got)
	}
	if !resolveYoloVisible(cfg) {
		t.Fatalf("expected rules mode to remain visible")
	}
	if resolveYoloEnabled(cfg) {
		t.Fatalf("expected legacy bypass flag to stay disabled in rules mode")
	}
	if cfg.YoloEnabled == nil || *cfg.YoloEnabled {
		t.Fatalf("expected legacy yoloEnabled compatibility flag to persist false")
	}
}

func TestPersistYoloModeStoresOffWithVisibilityEvidence(t *testing.T) {
	store := newTempStore(t)
	if err := persistYoloMode(store, config.YoloModeOff); err != nil {
		t.Fatalf("persist yolo off: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := resolveYoloMode(cfg); got != config.YoloModeOff {
		t.Fatalf("expected off mode from config, got %q", got)
	}
	if !resolveYoloVisible(cfg) {
		t.Fatalf("expected yolo to remain visible after prior use")
	}
}

func TestNextYoloModeCyclesThroughThreeStates(t *testing.T) {
	if got := nextYoloMode(config.YoloModeOff); got != config.YoloModeBypass {
		t.Fatalf("expected off -> bypass, got %q", got)
	}
	if got := nextYoloMode(config.YoloModeBypass); got != config.YoloModeRules {
		t.Fatalf("expected bypass -> rules, got %q", got)
	}
	if got := nextYoloMode(config.YoloModeRules); got != config.YoloModeOff {
		t.Fatalf("expected rules -> off, got %q", got)
	}
	if got := nextYoloMode(config.YoloMode("weird")); got != config.YoloModeBypass {
		t.Fatalf("expected invalid -> bypass, got %q", got)
	}
}
