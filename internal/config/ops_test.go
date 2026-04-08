package config

import (
	"testing"
	"time"
)

func TestConfigProfileOps(t *testing.T) {
	now := time.Now()
	cfg := Config{Version: CurrentVersion}

	p := Profile{ID: "p1", Name: "MyProfile", Host: "h", Port: 22, User: "u", CreatedAt: now}
	cfg.UpsertProfile(p)

	if got, ok := cfg.FindProfile("p1"); !ok || got.ID != "p1" {
		t.Fatalf("FindProfile by id failed: ok=%v got=%#v", ok, got)
	}
	if got, ok := cfg.FindProfile("myprofile"); !ok || got.ID != "p1" {
		t.Fatalf("FindProfile by name failed: ok=%v got=%#v", ok, got)
	}

	p2 := p
	p2.Host = "h2"
	cfg.UpsertProfile(p2)
	if got, _ := cfg.FindProfile("p1"); got.Host != "h2" {
		t.Fatalf("UpsertProfile did not update: %#v", got)
	}
}

func TestConfigInstanceOps(t *testing.T) {
	cfg := Config{Version: CurrentVersion}

	a := Instance{ID: "a", ProfileID: "p1", HTTPPort: 1, SocksPort: 2}
	b := Instance{ID: "b", ProfileID: "p2", HTTPPort: 3, SocksPort: 4}
	cfg.UpsertInstance(a)
	cfg.UpsertInstance(b)

	if got := cfg.InstancesForProfile("p1"); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("InstancesForProfile=%#v", got)
	}

	a2 := a
	a2.HTTPPort = 9
	cfg.UpsertInstance(a2)
	if got := cfg.InstancesForProfile("p1"); got[0].HTTPPort != 9 {
		t.Fatalf("UpsertInstance did not update: %#v", got[0])
	}

	if ok := cfg.RemoveInstance("missing"); ok {
		t.Fatalf("RemoveInstance(missing) = true")
	}
	if ok := cfg.RemoveInstance("a"); !ok {
		t.Fatalf("RemoveInstance(a) = false")
	}
	if got := cfg.InstancesForProfile("p1"); len(got) != 0 {
		t.Fatalf("expected p1 instances removed, got %#v", got)
	}
}

func TestConfigPatchFailureOps(t *testing.T) {
	requireExePatchEnabled(t)
	cfg := Config{Version: CurrentVersion}
	entry := PatchFailure{
		ProxyVersion:  "v1.2.3",
		HostID:        "host-a",
		ClaudeVersion: "2.1.19",
		ClaudeSHA256:  "abc",
		ClaudePath:    "/tmp/claude",
	}

	if cfg.HasPatchFailure("host-a", "v1.2.3", "2.1.19", "") {
		t.Fatalf("expected no patch failure initially")
	}
	cfg.UpsertPatchFailure(entry)
	if !cfg.HasPatchFailure("host-a", "v1.2.3", "2.1.19", "") {
		t.Fatalf("expected patch failure by version")
	}
	if !cfg.HasPatchFailure("host-a", "v1.2.3", "", "abc") {
		t.Fatalf("expected patch failure by sha fallback")
	}
	if cfg.HasPatchFailure("host-a", "v1.2.4", "2.1.19", "") {
		t.Fatalf("expected mismatch by proxy version")
	}
	if cfg.HasPatchFailure("host-b", "v1.2.3", "2.1.19", "") {
		t.Fatalf("expected mismatch by host id")
	}

	updated := entry
	updated.Reason = "failed"
	cfg.UpsertPatchFailure(updated)
	if len(cfg.PatchFailures) != 1 {
		t.Fatalf("expected 1 patch failure, got %d", len(cfg.PatchFailures))
	}
	if cfg.PatchFailures[0].Reason != "failed" {
		t.Fatalf("expected updated patch failure, got %#v", cfg.PatchFailures[0])
	}
}

func TestConfigProfileAndPatchFailureEdges(t *testing.T) {
	t.Run("FindProfile trims input", func(t *testing.T) {
		cfg := Config{Profiles: []Profile{{ID: "p1", Name: "Name"}}}
		if _, ok := cfg.FindProfile("  "); ok {
			t.Fatalf("expected empty ref to return false")
		}
		if got, ok := cfg.FindProfile("  p1 "); !ok || got.ID != "p1" {
			t.Fatalf("expected trimmed id match, got %#v ok=%v", got, ok)
		}
	})

	t.Run("UpsertProfile does not merge by name", func(t *testing.T) {
		cfg := Config{}
		cfg.UpsertProfile(Profile{ID: "p1", Name: "Same"})
		cfg.UpsertProfile(Profile{ID: "p2", Name: "Same"})
		if len(cfg.Profiles) != 2 {
			t.Fatalf("expected distinct ids to append, got %d", len(cfg.Profiles))
		}
	})

	t.Run("HasPatchFailure requires proxy version", func(t *testing.T) {
		cfg := Config{PatchFailures: []PatchFailure{{ProxyVersion: "v1", HostID: "host-a", ClaudeVersion: "2.0"}}}
		if cfg.HasPatchFailure("host-a", "", "2.0", "") {
			t.Fatalf("expected empty proxy version to return false")
		}
	})

	t.Run("HasPatchFailure treats legacy empty host id as current host", func(t *testing.T) {
		cfg := Config{PatchFailures: []PatchFailure{{ProxyVersion: "v1", ClaudeVersion: "2.0"}}}
		if !cfg.HasPatchFailure("host-a", "v1", "2.0", "") {
			t.Fatalf("expected legacy empty host id entry to match current host")
		}
	})

	t.Run("PurgeStalePatchFailures removes old proxy versions", func(t *testing.T) {
		cfg := Config{PatchFailures: []PatchFailure{
			{ProxyVersion: "v0.0.41", ClaudeVersion: "2.0"},
			{ProxyVersion: "v0.0.42", ClaudeVersion: "2.0"},
			{ProxyVersion: "v0.0.42", ClaudeVersion: "2.1"},
		}}
		changed := cfg.PurgeStalePatchFailures("v0.0.42")
		if !changed {
			t.Fatalf("expected purge to report changes")
		}
		if len(cfg.PatchFailures) != 2 {
			t.Fatalf("expected 2 remaining, got %d", len(cfg.PatchFailures))
		}
		for _, f := range cfg.PatchFailures {
			if f.ProxyVersion != "v0.0.42" {
				t.Fatalf("unexpected entry: %#v", f)
			}
		}
	})

	t.Run("PurgeStalePatchFailures no-op when all current", func(t *testing.T) {
		cfg := Config{PatchFailures: []PatchFailure{
			{ProxyVersion: "v1", ClaudeVersion: "2.0"},
		}}
		if cfg.PurgeStalePatchFailures("v1") {
			t.Fatalf("expected no change")
		}
		if len(cfg.PatchFailures) != 1 {
			t.Fatalf("expected 1 remaining, got %d", len(cfg.PatchFailures))
		}
	})

	t.Run("PurgeStalePatchFailures empty version is no-op", func(t *testing.T) {
		cfg := Config{PatchFailures: []PatchFailure{
			{ProxyVersion: "v1", ClaudeVersion: "2.0"},
		}}
		if cfg.PurgeStalePatchFailures("") {
			t.Fatalf("expected no change for empty version")
		}
	})

	t.Run("samePatchFailureKey matches sha and path", func(t *testing.T) {
		a := PatchFailure{ProxyVersion: "v1", HostID: "host-a", ClaudeSHA256: "ABC"}
		b := PatchFailure{ProxyVersion: "v1", HostID: "host-a", ClaudeSHA256: "abc"}
		if !samePatchFailureKey(a, b) {
			t.Fatalf("expected sha to match case-insensitively")
		}
		a = PatchFailure{ProxyVersion: "v1", HostID: "host-a", ClaudePath: "/tmp/claude"}
		b = PatchFailure{ProxyVersion: "v1", HostID: "host-a", ClaudePath: "/tmp/claude"}
		if !samePatchFailureKey(a, b) {
			t.Fatalf("expected path match when versions missing")
		}
	})

	t.Run("samePatchFailureKey matches legacy empty host id", func(t *testing.T) {
		a := PatchFailure{ProxyVersion: "v1", ClaudeVersion: "2.0"}
		b := PatchFailure{ProxyVersion: "v1", HostID: "host-a", ClaudeVersion: "2.0"}
		if !samePatchFailureKey(a, b) {
			t.Fatalf("expected empty host id to match legacy entry")
		}
	})

	t.Run("YoloBypassProbe lookup and purge", func(t *testing.T) {
		cfg := Config{Version: CurrentVersion}
		cfg.UpsertYoloBypassProbe(YoloBypassProbe{
			ProxyVersion:  "v1",
			ClaudeVersion: "2.1.96",
			Args:          []string{"--dangerously-skip-permissions"},
		})
		cfg.UpsertYoloBypassProbe(YoloBypassProbe{
			ProxyVersion: "v0",
			ClaudePath:   "/tmp/claude",
			Args:         []string{"--permission-mode", "bypassPermissions"},
		})
		if got, ok := cfg.FindYoloBypassProbe("v1", "2.1.96", "/ignored"); !ok || len(got) != 1 || got[0] != "--dangerously-skip-permissions" {
			t.Fatalf("unexpected version probe lookup: %#v ok=%v", got, ok)
		}
		if got, ok := cfg.FindYoloBypassProbe("v0", "", "/tmp/claude"); !ok || len(got) != 2 || got[0] != "--permission-mode" {
			t.Fatalf("unexpected path probe lookup: %#v ok=%v", got, ok)
		}
		if !cfg.PurgeStaleYoloBypassProbes("v1") {
			t.Fatalf("expected stale probe purge to report changes")
		}
		if len(cfg.YoloBypassProbes) != 1 {
			t.Fatalf("expected one probe after purge, got %d", len(cfg.YoloBypassProbes))
		}
		if _, ok := cfg.FindYoloBypassProbe("v0", "", "/tmp/claude"); ok {
			t.Fatalf("expected stale yolo probe entry to be removed")
		}
	})

	t.Run("YoloBypassProbe upsert overwrites matching key", func(t *testing.T) {
		cfg := Config{Version: CurrentVersion}
		cfg.UpsertYoloBypassProbe(YoloBypassProbe{
			ProxyVersion:  "v1",
			ClaudeVersion: "2.1.96",
			Args:          []string{"--permission-mode", "bypassPermissions"},
		})
		cfg.UpsertYoloBypassProbe(YoloBypassProbe{
			ProxyVersion:  "v1",
			ClaudeVersion: "2.1.96",
			Args:          []string{"--dangerously-skip-permissions"},
		})
		if len(cfg.YoloBypassProbes) != 1 {
			t.Fatalf("expected overwrite instead of append, got %d entries", len(cfg.YoloBypassProbes))
		}
		if got, ok := cfg.FindYoloBypassProbe("v1", "2.1.96", ""); !ok || len(got) != 1 || got[0] != "--dangerously-skip-permissions" {
			t.Fatalf("unexpected overwritten args: %#v ok=%v", got, ok)
		}
	})

	t.Run("sameYoloBypassProbeKey distinguishes version and path scopes", func(t *testing.T) {
		if !sameYoloBypassProbeKey(
			YoloBypassProbe{ProxyVersion: "v1", ClaudeVersion: "2.1.96"},
			YoloBypassProbe{ProxyVersion: "v1", ClaudeVersion: "2.1.96", ClaudePath: "/ignored"},
		) {
			t.Fatalf("expected matching version-scoped keys")
		}
		if sameYoloBypassProbeKey(
			YoloBypassProbe{ProxyVersion: "v1", ClaudeVersion: "2.1.96"},
			YoloBypassProbe{ProxyVersion: "v1", ClaudeVersion: "2.1.97"},
		) {
			t.Fatalf("expected version-scoped keys to differ")
		}
		if !sameYoloBypassProbeKey(
			YoloBypassProbe{ProxyVersion: "v1", ClaudePath: "/tmp/claude"},
			YoloBypassProbe{ProxyVersion: "v1", ClaudePath: "/tmp/claude"},
		) {
			t.Fatalf("expected matching path-scoped keys")
		}
		if sameYoloBypassProbeKey(
			YoloBypassProbe{ProxyVersion: "v1", ClaudePath: "/tmp/claude-a"},
			YoloBypassProbe{ProxyVersion: "v1", ClaudePath: "/tmp/claude-b"},
		) {
			t.Fatalf("expected path-scoped keys to differ")
		}
	})
}
