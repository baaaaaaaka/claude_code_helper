package cli

import "testing"

func TestExePatchEnabledDefaultEnvParsing(t *testing.T) {
	t.Run("empty defaults true", func(t *testing.T) {
		t.Setenv(exePatchEnabledEnv, "")
		if !exePatchEnabledDefault() {
			t.Fatalf("expected default enabled when env empty")
		}
	})

	t.Run("invalid defaults false", func(t *testing.T) {
		t.Setenv(exePatchEnabledEnv, "not-a-bool")
		if exePatchEnabledDefault() {
			t.Fatalf("expected disabled when env invalid")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		t.Setenv(exePatchEnabledEnv, "false")
		if exePatchEnabledDefault() {
			t.Fatalf("expected disabled when env false")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		t.Setenv(exePatchEnabledEnv, "true")
		if !exePatchEnabledDefault() {
			t.Fatalf("expected enabled when env true")
		}
	})
}

func TestExePatchGlibcCompatDefaultEnvParsing(t *testing.T) {
	t.Run("empty defaults true", func(t *testing.T) {
		t.Setenv(exePatchGlibcCompatEnv, "")
		if !exePatchGlibcCompatDefault() {
			t.Fatalf("expected glibc compat enabled when env empty")
		}
	})

	t.Run("invalid defaults false", func(t *testing.T) {
		t.Setenv(exePatchGlibcCompatEnv, "bad")
		if exePatchGlibcCompatDefault() {
			t.Fatalf("expected glibc compat disabled when env invalid")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		t.Setenv(exePatchGlibcCompatEnv, "false")
		if exePatchGlibcCompatDefault() {
			t.Fatalf("expected glibc compat disabled when env false")
		}
	})
}

func TestExePatchGlibcCompatRootDefault(t *testing.T) {
	t.Setenv(exePatchGlibcCompatRootEnv, "  /tmp/glibc-root  ")
	if got := exePatchGlibcCompatRootDefault(); got != "/tmp/glibc-root" {
		t.Fatalf("expected trimmed root, got %q", got)
	}
}

func TestGlibcCompatHostEnabled(t *testing.T) {
	prev := glibcCompatHostEligibleFn
	t.Cleanup(func() { glibcCompatHostEligibleFn = prev })

	t.Run("uses detected host by default", func(t *testing.T) {
		t.Setenv(exePatchGlibcCompatForceHostEnv, "")
		glibcCompatHostEligibleFn = func() bool { return false }
		if glibcCompatHostEnabled() {
			t.Fatalf("expected host eligibility to remain false without force env")
		}
	})

	t.Run("force env overrides host detection", func(t *testing.T) {
		t.Setenv(exePatchGlibcCompatForceHostEnv, "true")
		glibcCompatHostEligibleFn = func() bool { return false }
		if !glibcCompatHostEnabled() {
			t.Fatalf("expected force env to enable glibc compat host mode")
		}
	})
}
