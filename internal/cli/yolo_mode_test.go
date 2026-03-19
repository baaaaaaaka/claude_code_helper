package cli

import "testing"

func TestResolveYoloEnabledDefaultsFalse(t *testing.T) {
	store := newTempStore(t)
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if resolveYoloEnabled(cfg) {
		t.Fatalf("expected yolo disabled by default")
	}
	if resolveYoloVisible(cfg) {
		t.Fatalf("expected yolo to stay hidden by default")
	}
}

func TestPersistYoloEnabledStoresValue(t *testing.T) {
	store := newTempStore(t)
	if err := persistYoloEnabled(store, true); err != nil {
		t.Fatalf("persist yolo: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !resolveYoloEnabled(cfg) {
		t.Fatalf("expected yolo enabled from config")
	}
	if !resolveYoloVisible(cfg) {
		t.Fatalf("expected yolo to be visible after enable")
	}
}

func TestPersistYoloDisabledKeepsVisibilityEvidence(t *testing.T) {
	store := newTempStore(t)
	if err := persistYoloEnabled(store, false); err != nil {
		t.Fatalf("persist yolo disabled: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if resolveYoloEnabled(cfg) {
		t.Fatalf("expected yolo disabled from config")
	}
	if !resolveYoloVisible(cfg) {
		t.Fatalf("expected yolo to remain visible after prior use")
	}
}
