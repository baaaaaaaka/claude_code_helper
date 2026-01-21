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
